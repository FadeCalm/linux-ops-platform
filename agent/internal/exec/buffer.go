package exec

import "sync"

// cappedBuffer is a concurrency-safe byte buffer with a hard size cap. Data
// beyond the cap is discarded and the truncated flag is set.
type cappedBuffer struct {
	mu        sync.RWMutex
	data      []byte
	limit     int64
	truncated bool
}

func newCappedBuffer(limit int64) *cappedBuffer {
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Report the full input as written even when the tail is dropped:
	// io.Writer requires n == len(p) on a nil error, otherwise io.Copy stops
	// reading the pipe and the child process can block on a full stdout pipe.
	n := len(p)
	remaining := b.limit - int64(len(b.data))
	if remaining <= 0 {
		b.truncated = true
		return n, nil
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

// Slice returns up to max bytes starting at offset.
func (b *cappedBuffer) Slice(offset int64, max int) []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if offset < 0 || offset >= int64(len(b.data)) {
		return nil
	}
	end := offset + int64(max)
	if end > int64(len(b.data)) {
		end = int64(len(b.data))
	}
	out := make([]byte, end-offset)
	copy(out, b.data[offset:end])
	return out
}

func (b *cappedBuffer) Len() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return int64(len(b.data))
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.truncated
}
