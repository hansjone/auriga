package ingress

import (
	"io"
	"net/http"
	"os"
	"strconv"
)

const defaultTailBytes = 64 * 1024

func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request, instanceID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeProblem(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method Not Allowed", "GET only")
		return
	}
	inst, ok := s.cfg.Registry.Lookup(instanceID)
	if !ok || inst.LogPath == "" {
		writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "instance not found")
		return
	}
	n := defaultTailBytes
	if v := r.URL.Query().Get("bytes"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 8<<20 {
			n = parsed
		}
	}
	data, err := tailFile(inst.LogPath, int64(n))
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeProblem(w, http.StatusInternalServerError, CodeInternal, "Error", "read log failed")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

func tailFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	var start int64
	if size > max {
		start = size - max
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
