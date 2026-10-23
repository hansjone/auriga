// Package releaseproof reads public, content-addressed original OCI evidence.
// The installation directory is transport only; VerifyOCI establishes trust.
package releaseproof

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
)

var manifestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Read never accepts a locator from a spec except the already signed digest.
// Root handles pin the directories during reads; identity checks reject swaps.
func Read(dir, digest string) (manifest, config []byte, err error) {
	if !manifestDigest.MatchString(digest) {
		return nil, nil, fmt.Errorf("invalid proof digest")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	for p := absolute; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return nil, nil, err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("proof directory is not a plain directory")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	name := digest[7:]
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("proof entry is not a plain directory")
	}
	entry, err := root.OpenRoot(name)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = entry.Close() }()
	after, err := entry.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return nil, nil, fmt.Errorf("proof directory changed")
	}
	manifest, err = readFile(entry, "manifest.json", releaseverify.MaxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	config, err = readFile(entry, "config.json", releaseverify.MaxConfigBytes)
	return manifest, config, err
}

func readFile(root *os.Root, name string, limit int) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, fmt.Errorf("proof must be a bounded ordinary file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("proof file changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > limit {
		return nil, fmt.Errorf("proof exceeds size limit")
	}
	return b, nil
}
