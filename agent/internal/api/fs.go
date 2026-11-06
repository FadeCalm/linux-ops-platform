// File-system endpoints used by the management UI (browse, read, write,
// upload, download and simple metadata operations).
//
// Phase 1 has no authentication and the agent typically runs as root, so these
// endpoints are equivalent to unrestricted remote file access. They must only
// be reachable from the management backend on an isolated network.
package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	fsMaxReadBytes   = 2 << 20  // 2 MiB: largest file the editor may load
	fsMaxWriteBytes  = 4 << 20  // base64 overhead + JSON envelope
	fsMaxUploadChunk = 32 << 20 // largest single upload chunk
	fsUploadPrefix   = ".fs-upload-"
)

type fsEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"` // dir | file | symlink | other
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModTime    time.Time `json:"mtime"`
	UID        uint32    `json:"uid"`
	GID        uint32    `json:"gid"`
	LinkTarget string    `json:"link_target,omitempty"`
	Editable   bool      `json:"editable"`
}

func absPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(p), nil
}

func isFilesystemRoot(p string) bool {
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p, vol)
	// On Windows the separator is `\` but callers may still send `/`.
	return rest == "" || rest == string(filepath.Separator) || rest == "/"
}

func parseMode(s string, def os.FileMode) (os.FileMode, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "0o"), 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q", s)
	}
	return os.FileMode(v), nil
}

func (s *Server) fsList(w http.ResponseWriter, r *http.Request) {
	dir, err := absPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	des, err := os.ReadDir(dir)
	if err != nil {
		writeFsError(w, dir, err)
		return
	}
	entries := make([]fsEntry, 0, len(des))
	for _, d := range des {
		entries = append(entries, buildEntry(dir, d))
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].Type == "dir", entries[j].Type == "dir"
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	total := len(entries)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := entries[offset:end]

	parent := ""
	if !isFilesystemRoot(dir) {
		parent = filepath.Dir(dir)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path": dir, "parent": parent, "entries": page,
		"total": total, "offset": offset, "limit": limit,
	})
}

func buildEntry(dir string, d os.DirEntry) fsEntry {
	p := filepath.Join(dir, d.Name())
	e := fsEntry{Name: d.Name(), Path: p, Type: "other"}
	info, err := d.Info()
	if err != nil {
		return e
	}
	e.Size = info.Size()
	e.Mode = info.Mode().String()
	e.ModTime = info.ModTime()
	e.UID, e.GID = fileOwner(info)
	switch {
	case d.IsDir():
		e.Type = "dir"
	case info.Mode()&os.ModeSymlink != 0:
		e.Type = "symlink"
		if target, err := os.Readlink(p); err == nil {
			e.LinkTarget = target
		}
	case info.Mode().IsRegular():
		e.Type = "file"
		e.Editable = info.Size() <= fsMaxReadBytes
	}
	return e
}

func (s *Server) fsStat(w http.ResponseWriter, r *http.Request) {
	p, err := absPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		writeFsError(w, p, err)
		return
	}
	entry := fsEntry{
		Name: filepath.Base(p), Path: p, Size: info.Size(),
		Mode: info.Mode().String(), ModTime: info.ModTime(),
	}
	entry.UID, entry.GID = fileOwner(info)
	switch {
	case info.IsDir():
		entry.Type = "dir"
	case info.Mode().IsRegular():
		entry.Type = "file"
		entry.Editable = info.Size() <= fsMaxReadBytes
	default:
		entry.Type = "other"
	}
	binary := false
	if entry.Type == "file" && entry.Editable {
		binary, _ = isBinaryFile(p)
	}
	entry.Editable = entry.Editable && !binary
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entry": entry, "binary": binary,
	})
}

func (s *Server) fsRead(w http.ResponseWriter, r *http.Request) {
	p, err := absPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeFsError(w, p, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "not a regular file")
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if offset < 0 {
		offset = 0
	}
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit <= 0 || limit > fsMaxReadBytes {
		limit = fsMaxReadBytes
	}

	binary, err := isBinaryFile(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"path": p, "size": info.Size(), "offset": offset,
		"mod_time": info.ModTime(), "binary": binary, "eof": true, "data": "",
	}
	if !binary && offset < info.Size() {
		buf := make([]byte, limit)
		n, err := f.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp["data"] = base64.StdEncoding.EncodeToString(buf[:n])
		resp["eof"] = offset+int64(n) >= info.Size()
	}
	writeJSON(w, http.StatusOK, resp)
}

// isBinaryFile samples the beginning of a file looking for NUL bytes.
func isBinaryFile(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	sample := make([]byte, 8192)
	n, err := f.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.IndexByte(sample[:n], 0) >= 0, nil
}

type fsWriteRequest struct {
	Path      string `json:"path"`
	Data      string `json:"data"`
	BaseMtime string `json:"base_mtime"`
	Mode      string `json:"mode"`
}

func (s *Server) fsWrite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, fsMaxWriteBytes)
	var req fsWriteRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	p, err := absPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	if int64(len(raw)) > fsMaxReadBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("content exceeds %d bytes", fsMaxReadBytes))
		return
	}

	mode, err := parseMode(req.Mode, 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, statErr := os.Stat(p)
	switch {
	case statErr == nil:
		if info.IsDir() {
			writeError(w, http.StatusBadRequest, "path is a directory")
			return
		}
		if req.BaseMtime != "" {
			base, err := time.Parse(time.RFC3339Nano, req.BaseMtime)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid base_mtime")
				return
			}
			if !base.Equal(info.ModTime()) {
				writeError(w, http.StatusConflict, "file changed on disk since it was opened; reload before saving")
				return
			}
		}
		if mode == 0 {
			mode = info.Mode().Perm()
		}
	case !os.IsNotExist(statErr):
		writeFsError(w, p, statErr)
		return
	default:
		if mode == 0 {
			mode = 0o644
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), ".fs-write-*")
	if err != nil {
		writeFsError(w, filepath.Dir(p), err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmpName, p); err != nil {
		writeFsError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"path": p, "size": len(raw)})
}

func (s *Server) fsDownload(w http.ResponseWriter, r *http.Request) {
	p, err := absPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeFsError(w, p, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is a directory")
		return
	}
	if ctype := mime.TypeByExtension(filepath.Ext(info.Name())); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Content-Disposition",
		"attachment; filename*=UTF-8''"+url.PathEscape(info.Name()))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (s *Server) fsUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := absPath(q.Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := q.Get("upload_id")
	if !validUploadID(id) {
		writeError(w, http.StatusBadRequest, "invalid upload_id")
		return
	}
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid offset")
		return
	}
	total, err := strconv.ParseInt(q.Get("total"), 10, 64)
	if err != nil || total <= 0 {
		writeError(w, http.StatusBadRequest, "invalid total")
		return
	}
	mode, err := parseMode(q.Get("mode"), 0o644)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dir := filepath.Dir(p)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "parent directory does not exist")
		return
	}
	tmp := filepath.Join(dir, fsUploadPrefix+id+".part")

	var f *os.File
	if offset == 0 {
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	} else {
		f, err = os.OpenFile(tmp, os.O_WRONLY, 0o600)
		if err == nil {
			var info os.FileInfo
			if info, err = f.Stat(); err == nil && info.Size() != offset {
				f.Close()
				writeError(w, http.StatusConflict, "upload offset mismatch; restart the transfer")
				return
			}
			if err == nil {
				if _, err = f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
	}
	if err != nil {
		writeFsError(w, tmp, err)
		return
	}

	body := http.MaxBytesReader(w, r.Body, fsMaxUploadChunk+1)
	n, copyErr := io.Copy(f, body)
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		writeError(w, http.StatusInternalServerError, copyErr.Error())
		return
	}
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, closeErr.Error())
		return
	}

	received := offset + n
	if received > total {
		os.Remove(tmp)
		writeError(w, http.StatusBadRequest, "received more bytes than expected")
		return
	}
	done := received == total
	if done {
		if err := os.Chmod(tmp, mode); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := os.Rename(tmp, p); err != nil {
			writeFsError(w, p, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path": p, "received": received, "total": total, "done": done,
	})
}

func validUploadID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

type fsOpRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
	Mode    string `json:"mode"`
}

func (s *Server) fsOp(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req fsOpRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	p, err := absPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, err := parseMode(req.Mode, 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch strings.ToLower(req.Op) {
	case "mkdir":
		if mode == 0 {
			mode = 0o755
		}
		if err := os.Mkdir(p, mode); err != nil {
			writeFsError(w, p, err)
			return
		}
	case "touch":
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			writeFsError(w, p, err)
			return
		}
		f.Close()
	case "rename":
		np, err := absPath(req.NewPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "new_path: "+err.Error())
			return
		}
		if isFilesystemRoot(p) || isFilesystemRoot(np) {
			writeError(w, http.StatusBadRequest, "refusing to rename the filesystem root")
			return
		}
		if err := os.Rename(p, np); err != nil {
			writeFsError(w, p, err)
			return
		}
	case "chmod":
		if mode == 0 {
			writeError(w, http.StatusBadRequest, "mode is required")
			return
		}
		if err := os.Chmod(p, mode); err != nil {
			writeFsError(w, p, err)
			return
		}
	case "delete":
		if isFilesystemRoot(p) {
			writeError(w, http.StatusBadRequest, "refusing to delete the filesystem root")
			return
		}
		if err := os.RemoveAll(p); err != nil {
			writeFsError(w, p, err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "unsupported op: "+req.Op)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": p})
}

func writeFsError(w http.ResponseWriter, p string, err error) {
	status := http.StatusInternalServerError
	msg := err.Error()
	switch {
	case os.IsNotExist(err):
		status, msg = http.StatusNotFound, "not found: "+p
	case os.IsPermission(err):
		status, msg = http.StatusForbidden, "permission denied: "+p
	}
	writeError(w, status, msg)
}
