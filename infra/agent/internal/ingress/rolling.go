package ingress

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RollingWriter appends to a per-instance log, rotating when size exceeds max.
type RollingWriter struct {
	path string
	max  int64
	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewRollingWriter creates or opens path with max bytes (default 50 MiB).
func NewRollingWriter(path string, max int64) (*RollingWriter, error) {
	if max <= 0 {
		max = 50 << 20
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &RollingWriter{path: path, max: max, f: f, size: st.Size()}, nil
}

func (w *RollingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, fmt.Errorf("rolling writer closed")
	}
	if w.size+int64(len(p)) > w.max {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RollingWriter) rotateLocked() error {
	_ = w.f.Close()
	bak := w.path + ".1"
	_ = os.Remove(bak)
	if err := os.Rename(w.path, bak); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}

// Close closes the underlying file.
func (w *RollingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
