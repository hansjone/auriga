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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
)

const (
	digestPrefix = "sha256:"
	refsFileName = "refs.json"
	retainAge    = 7 * 24 * time.Hour
	maxRedirects = 3
)

// Store is a content-addressed cache under <data>/artifacts/sha256/<hex>.
type Store struct {
	Root   string
	Policy policy.Policy
	Client *http.Client
	Now    func() time.Time

	mu sync.Mutex
}

// refEntry tracks which instances hold a digest.
type refEntry struct {
	Holders  map[string]struct{} `json:"holders"`
	LastUsed time.Time           `json:"lastUsed"`
}

func (e *refEntry) count() int {
	if e == nil || e.Holders == nil {
		return 0
	}
	return len(e.Holders)
}

// New returns a Store rooted at artifactsDir.
func New(artifactsDir string, pol policy.Policy) *Store {
	if pol.ArtifactConnectTimeout <= 0 {
		pol.ArtifactConnectTimeout = 30 * time.Second
	}
	if pol.ArtifactTotalTimeout <= 0 {
		pol.ArtifactTotalTimeout = 10 * time.Minute
	}
	if pol.MaxArtifactBytes <= 0 {
		pol.MaxArtifactBytes = policy.DefaultMaxArtifactBytes
	}
	s := &Store{
		Root:   artifactsDir,
		Policy: pol,
		Now:    time.Now,
	}
	s.Client = s.newHTTPClient()
	return s
}

func (s *Store) newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: s.Policy.ArtifactConnectTimeout}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   s.Policy.ArtifactConnectTimeout,
		ResponseHeaderTimeout: s.Policy.ArtifactConnectTimeout,
		MaxIdleConns:          4,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   s.Policy.ArtifactTotalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("artifacts: too many redirects")
			}
			if err := s.Policy.AllowArtifactURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// Path returns the on-disk path for a digest.
func (s *Store) Path(digest string) (string, error) {
	hexDig, err := NormalizeDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "sha256", hexDig), nil
}

// Ensure downloads url if needed, verifies SHA-256, and records holder.
// maxBytes <= 0 uses policy.MaxArtifactBytes. On cache tamper, re-downloads.
func (s *Store) Ensure(ctx context.Context, holder, url, digest string, maxBytes int64) (string, error) {
	if err := validateHolder(holder); err != nil {
		return "", err
	}
	hexDig, err := NormalizeDigest(digest)
	if err != nil {
		return "", err
	}
	if url == "" {
		return "", fmt.Errorf("artifacts: empty url")
	}
	if err := s.Policy.AllowArtifactURL(url); err != nil {
		return "", err
	}
	limit := maxBytes
	if limit <= 0 {
		limit = s.Policy.MaxArtifactBytes
	}

	path := filepath.Join(s.Root, "sha256", hexDig)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		if err := s.verifyFile(path, hexDig); err != nil {
			_ = os.Chmod(path, 0o600)
			_ = os.Remove(path)
			// fall through to re-download
		} else {
			if err := s.acquire(holder, hexDig); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(s.Root, 0o700)

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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artifacts: download status %d", resp.StatusCode)
	}

	h := sha256.New()
	limited := io.LimitReader(resp.Body, limit+1)
	n, err := io.Copy(io.MultiWriter(tmp, h), limited)
	if err != nil {
		return "", fmt.Errorf("artifacts: download interrupted after %d bytes: %w", n, err)
	}
	if n > limit {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("artifacts: exceeds max size %d", limit)
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
		if _, statErr := os.Stat(path); statErr == nil {
			if vErr := s.verifyFile(path, hexDig); vErr != nil {
				_ = os.Remove(path)
				return "", vErr
			}
		} else {
			return "", err
		}
	}
	if err := s.acquire(holder, hexDig); err != nil {
		return "", err
	}
	return path, nil
}

// Release removes holder from digest. If no holders remain, LastUsed is updated for GC.
func (s *Store) Release(holder, digest string) error {
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
	if e.Holders == nil {
		e.Holders = map[string]struct{}{}
	}
	delete(e.Holders, holder)
	e.LastUsed = s.Now().UTC()
	refs[hexDig] = e
	return s.saveRefsLocked(refs)
}

// ReleaseAll drops every digest held by instanceID.
func (s *Store) ReleaseAll(holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.loadRefsLocked()
	if err != nil {
		return err
	}
	now := s.Now().UTC()
	for dig, e := range refs {
		if e.Holders == nil {
			continue
		}
		if _, ok := e.Holders[holder]; ok {
			delete(e.Holders, holder)
			e.LastUsed = now
			refs[dig] = e
		}
	}
	return s.saveRefsLocked(refs)
}

// GC removes unreferenced artifacts older than retainAge, or all when force.
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
		if e.count() > 0 {
			continue
		}
		if !force && !e.LastUsed.IsZero() && now.Sub(e.LastUsed) < retainAge {
			continue
		}
		if e.LastUsed.IsZero() && !force {
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

// NormalizeDigest accepts "sha256:<hex>" or bare hex.
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

func validateHolder(holder string) error {
	if holder == "" {
		return fmt.Errorf("artifacts: empty holder")
	}
	if strings.ContainsAny(holder, "/\\\x00") {
		return fmt.Errorf("artifacts: invalid holder")
	}
	return nil
}

func (s *Store) verifyFile(path, hexDig string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
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

func (s *Store) acquire(holder, hexDig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.loadRefsLocked()
	if err != nil {
		return err
	}
	e := refs[hexDig]
	if e.Holders == nil {
		e.Holders = map[string]struct{}{}
	}
	e.Holders[holder] = struct{}{}
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
		// Corrupt refs: rebuild empty (artifacts remain; next Ensure re-acquires).
		_ = os.Rename(path, path+".corrupt")
		return map[string]refEntry{}, nil
	}
	if refs == nil {
		refs = map[string]refEntry{}
	}
	for dig, e := range refs {
		if e.Holders == nil {
			e.Holders = map[string]struct{}{}
			refs[dig] = e
		}
	}
	return refs, nil
}

func (s *Store) saveRefsLocked(refs map[string]refEntry) error {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.Root, refsFileName)
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
