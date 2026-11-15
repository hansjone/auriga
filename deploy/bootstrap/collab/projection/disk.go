package projection

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func readFile(path string, limit int) ([]byte, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > int64(limit) || st.Mode()&os.ModeSymlink != 0 {
		return nil, Fault("invalid_chain")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(st, actual) {
		return nil, Fault("invalid_chain")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) > limit {
		return nil, Fault("invalid_chain")
	}
	return b, nil
}
func readPrivateFile(path string, limit int) ([]byte, error) {
	if e := privateMetadata(path, false); e != nil {
		return nil, e
	}
	return readFile(path, limit)
}
func directory(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return Fault("invalid_chain")
	}
	return nil
}
func privateDir(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	if e := directory(path); e != nil {
		return e
	}
	return nil
}
func writeSync(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	c := f.Close()
	if e != nil {
		return e
	}
	return c
}
func atomicFile(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".replace-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	c := f.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return c
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}

type Generation struct {
	Selector api.LocalProjectionSelector
	Manifest api.LocalProjectionManifest
	Files    map[string][]byte
}

func (g *Generation) Clear() {
	if g != nil {
		for _, b := range g.Files {
			clear(b)
		}
	}
}
func ReadGeneration(root string) (*Generation, error) {
	if e := privateMetadata(root, true); e != nil {
		return nil, e
	}
	if e := directory(root); e != nil {
		return nil, e
	}
	b, e := readPrivateFile(filepath.Join(root, "selector.json"), 16384)
	if e != nil {
		return nil, e
	}
	var s api.LocalProjectionSelector
	if e = Decode(b, &s, true); e != nil {
		return nil, e
	}
	return ReadSelected(root, s)
}
func ReadSelected(root string, s api.LocalProjectionSelector) (*Generation, error) {
	if e := CheckSelector(s); e != nil {
		return nil, e
	}
	if e := directory(root); e != nil {
		return nil, e
	}
	parent := filepath.Join(root, "generations")
	if e := directory(parent); e != nil {
		return nil, e
	}
	dir := filepath.Join(parent, strings.TrimPrefix(s.ManifestDigest, "sha256:"))
	if e := directory(dir); e != nil {
		return nil, e
	}
	for _, path := range []string{root, parent, dir} {
		if e := privateMetadata(path, true); e != nil {
			return nil, e
		}
	}
	raw, e := readPrivateFile(filepath.Join(dir, "manifest.json"), 65536)
	if e != nil {
		return nil, e
	}
	var m api.LocalProjectionManifest
	if e = Decode(raw, &m, true); e != nil {
		return nil, e
	}
	if Digest(raw) != s.ManifestDigest {
		return nil, Fault("invalid_chain")
	}
	g := &Generation{Selector: s, Manifest: m, Files: map[string][]byte{}}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, e
	}
	if len(entries) != len(m.Files)+1 {
		return nil, Fault("invalid_chain")
	}
	for _, f := range m.Files {
		if !fileName.MatchString(f.Name) {
			g.Clear()
			return nil, Fault("invalid_chain")
		}
		b, e := readPrivateFile(filepath.Join(dir, f.Name), MaxFile)
		if e != nil {
			g.Clear()
			return nil, e
		}
		g.Files[f.Name] = b
	}
	selected, e := readPrivateFile(filepath.Join(root, "selector.json"), 16384)
	if e != nil {
		g.Clear()
		return nil, e
	}
	var current api.LocalProjectionSelector
	if e = Decode(selected, &current, true); e == nil {
		e = CheckFiles(s, m, g.Files, current, nil)
	}
	if e != nil {
		g.Clear()
		return nil, e
	}
	return g, nil
}

// Publish durably records the complete transaction before exposing its selector.
// A crash is resumed from precisely these bytes, including nonce and revision.
// The caller must verify the trust chain and all private/public pairs first.
func Publish(root string, g *Generation, verify func(*Generation) error) error {
	if verify == nil {
		return errors.New("projection: verifier required")
	}
	if e := verify(g); e != nil {
		return e
	}
	if e := CheckFiles(g.Selector, g.Manifest, g.Files, g.Selector, nil); e != nil {
		return e
	}
	if e := privateDir(root); e != nil {
		return e
	}
	old, e := ReadGeneration(root)
	if e == nil {
		defer old.Clear()
		if e = CheckProgress(g.Selector, &old.Selector); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	journal := filepath.Join(root, "transaction.json")
	b, e := Bytes(g)
	if e != nil {
		return e
	}
	defer clear(b)
	pending, e := readPrivateFile(journal, 180*MaxFile)
	if e == nil {
		defer clear(pending)
		if !bytes.Equal(pending, b) {
			return Fault("projection_pending")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if e = atomicFile(journal, b); e != nil {
		return e
	}
	return finishPublish(root, g)
}
func Recover(root string, verify func(*Generation) error) error {
	if verify == nil {
		return errors.New("projection: verifier required")
	}
	b, e := readPrivateFile(filepath.Join(root, "transaction.json"), 180*MaxFile)
	if e != nil {
		return e
	}
	defer clear(b)
	var g Generation
	if e = Decode(b, &g, true); e != nil {
		return e
	}
	defer g.Clear()
	if e = verify(&g); e != nil {
		return e
	}
	if e = CheckFiles(g.Selector, g.Manifest, g.Files, g.Selector, nil); e != nil {
		return e
	}
	old, e := ReadGeneration(root)
	if e == nil {
		defer old.Clear()
		if e = CheckProgress(g.Selector, &old.Selector); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return finishPublish(root, &g)
}
func finishPublish(root string, g *Generation) error {
	parent := filepath.Join(root, "generations")
	if e := privateDir(parent); e != nil {
		return e
	}
	dir := filepath.Join(parent, strings.TrimPrefix(g.Selector.ManifestDigest, "sha256:"))
	if e := privateDir(dir); e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	allowed := map[string]bool{"manifest.json": true}
	for n := range g.Files {
		allowed[n] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return Fault("invalid_chain")
		}
	}
	put := func(name string, b []byte) error {
		path := filepath.Join(dir, name)
		old, e := readFile(path, len(b))
		if e == nil {
			if !bytes.Equal(old, b) {
				return Fault("same_version_conflict")
			}
			return nil
		}
		if !os.IsNotExist(e) {
			return e
		}
		return writeSync(path, b)
	}
	for n, b := range g.Files {
		if e = put(n, b); e != nil {
			return e
		}
	}
	b, e := Bytes(g.Manifest)
	if e != nil {
		return e
	}
	if e = put("manifest.json", b); e != nil {
		return e
	}
	if e = syncDir(dir); e != nil {
		return e
	}
	if e = syncDir(parent); e != nil {
		return e
	}
	b, e = Bytes(g.Selector)
	if e != nil {
		return e
	}
	if e = atomicFile(filepath.Join(root, "selector.json"), b); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(root, "transaction.json")); e != nil {
		return e
	}
	return syncDir(root)
}
