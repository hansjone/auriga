// Package artifacts downloads, verifies, and caches content-addressed artifacts.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	digestPrefix = "sha256:"
	refsFileName = "refs.json"
	retainAge    = 7 * 24 * time.Hour
)

// Store is a content-addressed cache under <data>/artifacts/sha256/<hex>.
type Store struct {
	Root   string
	Client *http.Client
	Now    func() time.Time

	mu sync.Mutex
}

type refEntry struct {
	Count    int       `json:"count"`
	LastUsed time.Time `json:"lastUsed"`
}

// New returns a Store rooted at artifactsDir (typically cfg.ArtifactsDir()).
func New(artifactsDir string) *Store {
	return &Store{
		Root: artifactsDir,
		Client: &http.Client{
			Timeout: 30 * time.Minute,
		},
		Now: time.Now,
	}
}

// Path returns the on-disk path for a digest (sha256:<hex> or bare hex).
func (s *Store) Path(digest string) (string, error) {
	hexDig, err := NormalizeDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "sha256", hexDig), nil
}

// Ensure downloads url if needed, verifies SHA-256, and acquires a reference.
// On any verification failure the incomplete file is discarded and never used.
func (s *Store) Ensure(ctx context.Context, url, digest string) (string, error) {
	hexDig, err := NormalizeDigest(digest)
	if err != nil {
		return "", err
	}
	if url == "" {
		return "", fmt.Errorf("artifacts: empty url")
	}
	path := filepath.Join(s.Root, "sha256", hexDig)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		if err := s.verifyFile(path, hexDig); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		if err := s.acquire(hexDig); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+hexDig+".partial.*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("artifacts: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artifacts: download status %d", resp.StatusCode)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if err != nil {
		return "", fmt.Errorf("artifacts: download interrupted after %d bytes: %w", n, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != hexDig {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("artifacts: digest mismatch: want sha256:%s got sha256:%s", hexDig, got)
	}
	if err := os.Chmod(tmpPath, 0o444); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// Another worker may have won the race; verify winner.
		if _, statErr := os.Stat(path); statErr == nil {
			if vErr := s.verifyFile(path, hexDig); vErr != nil {
				return "", vErr
			}
		} else {
			return "", err
		}
	}
	if err := s.acquire(hexDig); err != nil {
		return "", err
	}
	return path, nil
}

// Release decrements the reference count for digest.
func (s *Store) Release(digest string) error {
	hexDig, err := NormalizeDigest(digest)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.loadRefsLocked()
	if err != nil {
		return err
	}
	e := refs[hexDig]
	if e.Count > 0 {
		e.Count--
	}
	e.LastUsed = s.Now().UTC()
	refs[hexDig] = e
	return s.saveRefsLocked(refs)
}

// GC removes unreferenced artifacts older than retainAge (7 days),
// or all unreferenced ones when force is true (disk pressure).
func (s *Store) GC(force bool) (removed int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.loadRefsLocked()
	if err != nil {
		return 0, err
	}
	now := s.Now().UTC()
	dir := filepath.Join(s.Root, "sha256")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, ent := range entries {
		if ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		hexDig := ent.Name()
		e := refs[hexDig]
		if e.Count > 0 {
			continue
		}
		if !force && !e.LastUsed.IsZero() && now.Sub(e.LastUsed) < retainAge {
			continue
		}
		if e.LastUsed.IsZero() && !force {
			// Never referenced in this refs file: treat mtime.
			info, iErr := ent.Info()
			if iErr != nil || now.Sub(info.ModTime()) < retainAge {
				continue
			}
		}
		if rErr := os.Remove(filepath.Join(dir, hexDig)); rErr == nil {
			removed++
			delete(refs, hexDig)
		}
	}
	if err := s.saveRefsLocked(refs); err != nil {
		return removed, err
	}
	return removed, nil
}

// NormalizeDigest accepts "sha256:<hex>" or bare lowercase/uppercase hex.
func NormalizeDigest(digest string) (string, error) {
	d := strings.TrimSpace(digest)
	if d == "" {
		return "", errors.New("artifacts: empty digest")
	}
	if strings.HasPrefix(strings.ToLower(d), digestPrefix) {
		d = d[len(digestPrefix):]
	}
	d = strings.ToLower(d)
	if len(d) != 64 {
		return "", fmt.Errorf("artifacts: invalid sha256 digest length %d", len(d))
	}
	for _, c := range d {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("artifacts: invalid sha256 digest")
		}
	}
	return d, nil
}

func (s *Store) verifyFile(path, hexDig string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != hexDig {
		return fmt.Errorf("artifacts: cached digest mismatch: want sha256:%s got sha256:%s", hexDig, got)
	}
	return nil
}

func (s *Store) acquire(hexDig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.loadRefsLocked()
	if err != nil {
		return err
	}
	e := refs[hexDig]
	e.Count++
	e.LastUsed = s.Now().UTC()
	refs[hexDig] = e
	return s.saveRefsLocked(refs)
}

func (s *Store) loadRefsLocked() (map[string]refEntry, error) {
	path := filepath.Join(s.Root, refsFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]refEntry{}, nil
		}
		return nil, err
	}
	var refs map[string]refEntry
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, fmt.Errorf("artifacts: refs: %w", err)
	}
	if refs == nil {
		refs = map[string]refEntry{}
	}
	return refs, nil
}

func (s *Store) saveRefsLocked(refs map[string]refEntry) error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.Root, refsFileName)
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
