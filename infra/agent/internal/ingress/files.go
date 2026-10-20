package ingress

import (
	"archive/zip"
	"encoding/json"
	"errors"
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
	rel, err := ValidateRelPath(rel)
	if err != nil {
		code, status := pathError(err)
		writeProblem(w, status, code, "Error", err.Error())
		return
	}
	_, err = s.cfg.Tickets.Verify(token, instanceID, op, rel)
	if err != nil {
		code, status := ticketError(err)
		writeProblem(w, status, code, "Forbidden", err.Error())
		return
	}

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
	case errors.Is(err, os.ErrNotExist):
		return CodePathInvalid, http.StatusNotFound
	case strings.Contains(msg, CodePathEscape):
		return CodePathEscape, http.StatusBadRequest
	case strings.Contains(msg, CodePathInvalid):
		return CodePathInvalid, http.StatusBadRequest
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
	f, err := OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
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
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return s.listDir(w, root, rel)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", CodePathInvalid)
	}
	http.ServeContent(w, r, filepath.Base(rel), st.ModTime(), f)
	return nil
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request, root, rel string) error {
	if r.ContentLength > s.cfg.MaxUploadBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, CodePathInvalid, "Too Large", "upload limit exceeded")
		return nil
	}
	f, err := createBeneath(root, rel)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", CodePathInvalid)
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeProblem(w, http.StatusRequestEntityTooLarge, CodePathInvalid, "Too Large", "upload limit exceeded")
		return nil
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deletePath(w http.ResponseWriter, root, rel string) error {
	if err := RemoveBeneath(root, rel); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) zipTree(w http.ResponseWriter, root, rel string) error {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return err
	}
	// Build before sending headers so errors never produce a successful partial zip.
	tmp, err := os.CreateTemp("", "auriga-export-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	zw := zip.NewWriter(tmp)
	var total int64
	count := 0
	var walk func(string, string) error
	walk = func(path, name string) error {
		count++
		if count > s.cfg.MaxZipEntries {
			return fmt.Errorf("%s: zip entry limit", CodePathInvalid)
		}
		f, err := OpenBeneath(root, path, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.IsDir() {
			// Bounded batches prevent a huge directory from exhausting memory.
			for {
				entries, err := f.ReadDir(128)
				for _, entry := range entries {
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if isLink(info) || !info.IsDir() && !info.Mode().IsRegular() {
						count++
						if count > s.cfg.MaxZipEntries {
							return fmt.Errorf("%s: zip entry limit", CodePathInvalid)
						}
						continue
					}
					if err := walk(filepath.ToSlash(filepath.Join(path, entry.Name())), filepath.ToSlash(filepath.Join(name, entry.Name()))); err != nil {
						return err
					}
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		if st.Size() > s.cfg.MaxZipBytes-total {
			return fmt.Errorf("%s: zip size limit", CodePathInvalid)
		}
		hdr, err := zip.FileInfoHeader(st)
		if err != nil {
			return err
		}
		if name == "." {
			name = filepath.Base(path)
		}
		hdr.Name, hdr.Method = name, zip.Deflate
		if utf8.ValidString(name) {
			hdr.Flags |= 0x800
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		n, err := io.Copy(fw, io.LimitReader(f, s.cfg.MaxZipBytes-total+1))
		total += n
		if err != nil {
			return err
		}
		if total > s.cfg.MaxZipBytes {
			return fmt.Errorf("%s: zip size limit", CodePathInvalid)
		}
		return nil
	}
	err = walk(clean, ".")
	closeErr := zw.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="export.zip"`)
	_, err = io.Copy(w, tmp)
	return err
}
