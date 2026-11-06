package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFSTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer((&Server{}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, u string, body interface{}, out interface{}) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s %s: %v (%s)", method, u, err, data)
		}
	}
	return resp.StatusCode
}

func TestFSLifecycle(t *testing.T) {
	ts := newFSTestServer(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")

	if code := doJSON(t, http.MethodPost, ts.URL+"/api/v1/fs/op",
		map[string]string{"op": "mkdir", "path": sub}, nil); code != 200 {
		t.Fatalf("mkdir: status %d", code)
	}

	file := filepath.Join(sub, "hello.txt")
	if code := doJSON(t, http.MethodPut, ts.URL+"/api/v1/fs/write",
		map[string]string{
			"path": file,
			"data": base64.StdEncoding.EncodeToString([]byte("hello\nworld\n")),
		}, nil); code != 200 {
		t.Fatalf("write: status %d", code)
	}

	// read back
	var readResp struct {
		Data string `json:"data"`
		Size int64  `json:"size"`
		EOF  bool   `json:"eof"`
	}
	q := url.Values{"path": {file}}
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/v1/fs/read?"+q.Encode(), nil, &readResp); code != 200 {
		t.Fatalf("read: status %d", code)
	}
	got, _ := base64.StdEncoding.DecodeString(readResp.Data)
	if string(got) != "hello\nworld\n" || !readResp.EOF {
		t.Fatalf("read = %q eof=%v", got, readResp.EOF)
	}

	// list the parent and find the subdirectory
	var listResp struct {
		Total   int       `json:"total"`
		Entries []fsEntry `json:"entries"`
	}
	q = url.Values{"path": {root}}
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/v1/fs/list?"+q.Encode(), nil, &listResp); code != 200 {
		t.Fatalf("list: status %d", code)
	}
	if listResp.Total != 1 || listResp.Entries[0].Name != "sub" || listResp.Entries[0].Type != "dir" {
		t.Fatalf("list = %+v", listResp)
	}

	// chunked upload (two chunks)
	content := []byte("chunked upload payload")
	up := filepath.Join(sub, "upload.bin")
	uploadID := "test-upload"
	mid := len(content) / 2
	for _, part := range []struct {
		offset int
		data   []byte
	}{{0, content[:mid]}, {mid, content[mid:]}} {
		q := url.Values{
			"path": {up}, "upload_id": {uploadID},
			"offset": {fmt.Sprint(part.offset)}, "total": {fmt.Sprint(len(content))},
		}
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/fs/upload?"+q.Encode(),
			bytes.NewReader(part.data))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Received int64 `json:"received"`
			Done     bool  `json:"done"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("upload chunk @%d: status %d", part.offset, resp.StatusCode)
		}
		if part.offset+len(part.data) == len(content) && !out.Done {
			t.Fatalf("upload: final chunk not marked done: %+v", out)
		}
	}
	if data, err := os.ReadFile(up); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("uploaded content mismatch: %q, %v", data, err)
	}

	// download
	resp, err := http.Get(ts.URL + "/api/v1/fs/download?" + url.Values{"path": {up}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, content) {
		t.Fatalf("download = %q status %d", body, resp.StatusCode)
	}

	// binary detection
	binPath := filepath.Join(sub, "blob.bin")
	os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0o644)
	var binResp struct {
		Binary bool `json:"binary"`
	}
	q = url.Values{"path": {binPath}}
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/v1/fs/read?"+q.Encode(), nil, &binResp); code != 200 || !binResp.Binary {
		t.Fatalf("binary read: status %d binary=%v", code, binResp.Binary)
	}

	// rename
	renamed := filepath.Join(sub, "renamed.txt")
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/v1/fs/op",
		map[string]string{"op": "rename", "path": file, "new_path": renamed}, nil); code != 200 {
		t.Fatalf("rename: status %d", code)
	}
	if _, err := os.Stat(renamed); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	// delete the whole directory tree
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/v1/fs/op",
		map[string]string{"op": "delete", "path": sub}, nil); code != 200 {
		t.Fatalf("delete: status %d", code)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestFSWriteConflict(t *testing.T) {
	ts := newFSTestServer(t)
	file := filepath.Join(t.TempDir(), "edit.txt")

	write := func(content, baseMtime string) int {
		return doJSON(t, http.MethodPut, ts.URL+"/api/v1/fs/write", map[string]string{
			"path": file, "data": base64.StdEncoding.EncodeToString([]byte(content)),
			"base_mtime": baseMtime,
		}, nil)
	}
	if code := write("v1", ""); code != 200 {
		t.Fatalf("initial write: %d", code)
	}
	var readResp struct {
		ModTime string `json:"mod_time"`
	}
	q := url.Values{"path": {file}}
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/v1/fs/read?"+q.Encode(), nil, &readResp); code != 200 {
		t.Fatalf("read: %d", code)
	}
	t.Logf("mod_time = %s", readResp.ModTime)

	if code := write("v2", readResp.ModTime); code != 200 {
		t.Fatalf("write with matching mtime: %d", code)
	}
	if code := write("v3", readResp.ModTime); code != http.StatusConflict {
		t.Fatalf("write with stale mtime: %d, want 409", code)
	}
}

func TestFSGuards(t *testing.T) {
	ts := newFSTestServer(t)

	// Relative paths are rejected.
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/v1/fs/list?path=relative",
		nil, nil); code != http.StatusBadRequest {
		t.Fatalf("relative path status = %d, want 400", code)
	}

	// Deleting the filesystem root is refused.
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/v1/fs/op",
		map[string]string{"op": "delete", "path": string(filepath.Separator)}, nil); code != http.StatusBadRequest {
		t.Fatalf("root delete status = %d, want 400", code)
	}

	// Unknown ops are refused.
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/v1/fs/op",
		map[string]string{"op": "nuke", "path": t.TempDir()}, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown op status = %d, want 400", code)
	}

	// An upload with a bogus id is refused.
	q := url.Values{"path": {filepath.Join(t.TempDir(), "x")}, "upload_id": {"../evil"},
		"offset": {"0"}, "total": {"1"}}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/fs/upload?"+q.Encode(),
		strings.NewReader("x"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus upload_id status = %d, want 400", resp.StatusCode)
	}
}
