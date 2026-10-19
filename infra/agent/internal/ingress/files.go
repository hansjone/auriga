package ingress

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (s *Server) serveFiles(w http.ResponseWriter, r *http.Request, instanceID, rest string) {
	inst, ok := s.cfg.Registry.Lookup(instanceID)
	if !ok || inst.RootDir == "" {
		writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "instance not found")
		return
	}
	token := ExtractTicketToken(r.Header.Get("Authorization"), r.URL.Query().Get("ticket"))
	if token == "" {
		writeProblem(w, http.StatusUnauthorized, CodeTicketInvalid, "Unauthorized", "missing ticket")
		return
	}

	rel := strings.TrimPrefix(rest, "/")
	op := fileOpForRequest(r, rel)
	claims, err := s.cfg.Tickets.Verify(token, instanceID, op, rel)
	if err != nil {
		code, status := ticketError(err)
		writeProblem(w, status, code, "Forbidden", err.Error())
		return
	}
	_ = claims

	exec := s.cfg.Executor
	if exec == nil {
		exec = DirectExecutor{}
	}
	err = exec.Run(instanceID, func() error {
		return s.handleFileOp(w, r, inst, rel, op)
	})
	if err != nil {
		code, status := pathError(err)
		if !headerWritten(w) {
			writeProblem(w, status, code, "Error", err.Error())
		}
	}
}

func fileOpForRequest(r *http.Request, rel string) FileOp {
	q := r.URL.Query()
	if q.Get("zip") == "1" || strings.HasSuffix(rel, ".zip") && r.Method == http.MethodGet && q.Get("download") == "zip" {
		return OpZip
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if strings.HasSuffix(rel, "/") || rel == "" || rel == "." {
			return OpList
		}
		return OpRead
	case http.MethodPut, http.MethodPost:
		return OpWrite
	case http.MethodDelete:
		return OpDelete
	default:
		return OpRead
	}
}

func ticketError(err error) (code string, status int) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, CodeTicketExpired):
		return CodeTicketExpired, http.StatusUnauthorized
	case strings.Contains(msg, CodeTicketInstanceMismatch):
		return CodeTicketInstanceMismatch, http.StatusForbidden
	case strings.Contains(msg, CodeTicketPathDenied):
		return CodeTicketPathDenied, http.StatusForbidden
	case strings.Contains(msg, CodeTicketOpDenied):
		return CodeTicketOpDenied, http.StatusForbidden
	default:
		return CodeTicketInvalid, http.StatusUnauthorized
	}
}

func pathError(err error) (code string, status int) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, CodePathEscape):
		return CodePathEscape, http.StatusBadRequest
	case strings.Contains(msg, CodePathInvalid):
		return CodePathInvalid, http.StatusBadRequest
	case os.IsNotExist(err):
		return CodePathInvalid, http.StatusNotFound
	default:
		return CodeInternal, http.StatusInternalServerError
	}
}

type statusRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func headerWritten(w http.ResponseWriter) bool {
	if sr, ok := w.(*statusRecorder); ok {
		return sr.wrote
	}
	return false
}

func (s *Server) handleFileOp(w http.ResponseWriter, r *http.Request, inst *Instance, rel string, op FileOp) error {
	switch op {
	case OpList:
		return s.listDir(w, inst.RootDir, rel)
	case OpRead:
		return s.readFile(w, r, inst.RootDir, rel)
	case OpWrite:
		return s.writeFile(w, r, inst.RootDir, rel)
	case OpDelete:
		return s.deletePath(w, inst.RootDir, rel)
	case OpZip:
		return s.zipTree(w, inst.RootDir, rel)
	default:
		writeProblem(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method Not Allowed", "unsupported")
		return nil
	}
}

func (s *Server) listDir(w http.ResponseWriter, root, rel string) error {
	path, err := ResolveUnderRoot(root, rel)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	type ent struct {
		Name  string `json:"name"`
		IsDir bool   `json:"isDir"`
		Size  int64  `json:"size,omitempty"`
	}
	out := make([]ent, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		var sz int64
		if info != nil && !e.IsDir() {
			sz = info.Size()
		}
		out = append(out, ent{Name: e.Name(), IsDir: e.IsDir(), Size: sz})
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request, root, rel string) error {
	f, err := OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return s.listDir(w, root, rel)
	}
	http.ServeContent(w, r, filepath.Base(rel), st.ModTime(), f)
	return nil
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request, root, rel string) error {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return err
	}
	path, err := ResolveUnderRoot(root, clean)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Create via open-beneath when possible; fall back to resolved path.
	f, err := OpenBeneath(root, clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	if _, err := io.Copy(f, r.Body); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deletePath(w http.ResponseWriter, root, rel string) error {
	path, err := ResolveUnderRoot(root, rel)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) zipTree(w http.ResponseWriter, root, rel string) error {
	base, err := ResolveUnderRoot(root, rel)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="export.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	return filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if _, err := ValidateRelPath(relPath); err != nil && relPath != "." {
			return fmt.Errorf("%s: zip entry", CodePathEscape)
		}
		// Reject if file is a symlink escaping (Walk follows; check Lstat).
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlink in zip", CodePathEscape)
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = relPath
		hdr.Method = zip.Deflate
		// UTF-8 filename flag (bit 11) so CJK names extract correctly.
		if utf8.ValidString(relPath) {
			hdr.Flags |= 0x800
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(fw, f)
		_ = f.Close()
		return copyErr
	})
}
