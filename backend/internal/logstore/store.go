// Package logstore persists task output to local files and records log index
// rows in MySQL. Large output lives on disk; the database only stores indexes.
package logstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"linux-ops-platform/backend/internal/models"

	"gorm.io/gorm"
)

const indexFlushBytes = 64 * 1024

type Store struct {
	baseDir string
	db      *gorm.DB
	mu      sync.Mutex
	pending map[string]pendingSeg // key targetID|stream
}

type pendingSeg struct {
	start  int64
	length int64
	path   string
}

func New(baseDir string, db *gorm.DB) *Store {
	return &Store{baseDir: baseDir, db: db, pending: map[string]pendingSeg{}}
}

// TargetDir returns the on-disk directory for a target's logs.
func (s *Store) TargetDir(taskID, targetID string) string {
	return filepath.Join(s.baseDir, taskID, targetID)
}

// Init prepares the target log directory and returns its path.
func (s *Store) Init(taskID, targetID string) (string, error) {
	dir := s.TargetDir(taskID, targetID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// Append writes a chunk to {stream}.log and inserts an index row once the
// accumulated segment exceeds indexFlushBytes (or force is true).
func (s *Store) Append(targetID, stream, taskID string, data []byte, startOffset, seq int64, force bool) error {
	if len(data) == 0 {
		return nil
	}
	dir, err := s.Init(taskID, targetID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, stream+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	key := targetID + "|" + stream
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pending[key]; ok {
		p.length += int64(len(data))
		s.pending[key] = p
		if p.length < indexFlushBytes && !force {
			return nil
		}
		delete(s.pending, key)
		return s.insertIndex(targetID, stream, path, p.start, p.length, seq)
	}
	if !force && int64(len(data)) < indexFlushBytes {
		s.pending[key] = pendingSeg{start: startOffset, length: int64(len(data)), path: path}
		return nil
	}
	return s.insertIndex(targetID, stream, path, startOffset, int64(len(data)), seq)
}

func (s *Store) insertIndex(targetID, stream, path string, start, length, seq int64) error {
	return s.db.Create(&models.ExecutionLogIndex{
		TargetID: targetID, Stream: stream, FilePath: path,
		ByteOffset: start, Length: length, Seq: seq,
	}).Error
}

// Flush forces any pending index segment for the target's streams.
func (s *Store) Flush(targetID string, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range []string{"stdout", "stderr"} {
		key := targetID + "|" + stream
		if p, ok := s.pending[key]; ok {
			delete(s.pending, key)
			if err := s.insertIndex(targetID, stream, p.path, p.start, p.length, seq); err != nil {
				return err
			}
		}
	}
	return nil
}

// Read returns up to limit bytes from a stream file starting at offset.
func (s *Store) Read(taskID, targetID, stream string, offset int64, limit int) ([]byte, bool, error) {
	path := filepath.Join(s.TargetDir(taskID, targetID), stream+".log")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	info, statErr := f.Stat()
	eof := true
	if statErr == nil {
		eof = offset+int64(n) >= info.Size()
	}
	return buf[:n], eof, nil
}

// DownloadPath returns the absolute path to a stream log file.
func (s *Store) DownloadPath(taskID, targetID, stream string) (string, error) {
	path := filepath.Join(s.TargetDir(taskID, targetID), stream+".log")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("log not found: %w", err)
	}
	return path, nil
}
