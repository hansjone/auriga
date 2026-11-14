package projection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Reader owns only one installation's private material/control volumes.
// Verify checks root control, publication and TLS scope before Activate can see
// private bytes. It must never fetch trust from the material being verified.
type Reader struct {
	Root, Control      string
	Scope              api.LocalProjectionSelector
	Verify             func(api.LocalProjectionSelector, map[string][]byte) error
	Activate           func(api.LocalProjectionSelector, map[string][]byte) error
	mu                 sync.Mutex
	committed, pending *api.LocalProjectionSelector
	loaded             bool
}

type journal struct {
	Committed *api.LocalProjectionSelector `json:"committed"`
	Pending   *api.LocalProjectionSelector `json:"pending"`
}

func (r *Reader) Restore() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, e := openFixedRoot(r.Control)
	if e != nil {
		return e
	}
	defer func() { _ = root.Close() }()
	_, e = root.Lstat("reader-state.json")
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	raw, e := readRegular(root, "reader-state.json", 65536)
	if e != nil {
		return e
	}
	var j journal
	if _, e = Canonical(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&j); e != nil {
		return e
	}
	for _, s := range []*api.LocalProjectionSelector{j.Committed, j.Pending} {
		if s != nil && (!SelectorValid(*s) || !r.scope(*s)) {
			return fmt.Errorf("invalid reader journal")
		}
	}
	r.committed, r.pending = j.Committed, j.Pending
	// Durable success is not proof of in-memory installation after a restart.
	r.loaded = false
	return nil
}

func openFixedRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("projection root must be absolute")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("linked or invalid projection root")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return os.OpenRoot(path)
}

func (r *Reader) scope(s api.LocalProjectionSelector) bool {
	return s.TenantId == r.Scope.TenantId && s.Realm == r.Scope.Realm && s.InstallationId == r.Scope.InstallationId && s.Component == r.Scope.Component
}

func readRegular(root *os.Root, name string, max int64) ([]byte, error) {
	info, e := root.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Size() > max {
		return nil, fmt.Errorf("invalid projection file")
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("projection changed during open")
	}
	raw, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(raw)) > max {
		return nil, fmt.Errorf("oversize projection file")
	}
	after, e := root.Lstat(name)
	if e != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) {
		return nil, fmt.Errorf("projection changed during read")
	}
	return raw, nil
}

func strictModel(raw []byte, target any) error {
	canonical, e := Canonical(raw)
	if e != nil || !bytes.Equal(canonical, raw) {
		return fmt.Errorf("noncanonical projection JSON")
	}
	if e = json.Unmarshal(raw, target); e != nil {
		return e
	}
	if !knownFields(reflect.ValueOf(target)) {
		return fmt.Errorf("unknown projection fields")
	}
	encoded, e := Bytes(target)
	if e != nil || !bytes.Equal(encoded, raw) {
		return fmt.Errorf("unknown projection fields")
	}
	return nil
}

func knownFields(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		return v.IsNil() || knownFields(v.Elem())
	}
	switch v.Kind() {
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			if v.Type().Field(n).Name == "AdditionalProperties" && v.Field(n).Len() != 0 {
				return false
			}
			if !knownFields(v.Field(n)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for n := 0; n < v.Len(); n++ {
			if !knownFields(v.Index(n)) {
				return false
			}
		}
	}
	return true
}

func (r *Reader) disk() (api.LocalProjectionSelector, api.LocalProjectionManifest, map[string][]byte, error) {
	var s api.LocalProjectionSelector
	var m api.LocalProjectionManifest
	root, e := openFixedRoot(r.Root)
	if e != nil {
		return s, m, nil, e
	}
	defer func() { _ = root.Close() }()
	raw, e := readRegular(root, "selector.json", 16384)
	if e != nil {
		return s, m, nil, e
	}
	if e = strictModel(raw, &s); e != nil || !SelectorValid(s) || !r.scope(s) {
		return s, m, nil, fmt.Errorf("invalid projection scope")
	}
	for _, dir := range []string{"generations", "generations/" + s.ManifestDigest[7:]} {
		info, e := root.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return s, m, nil, fmt.Errorf("invalid generation directory")
		}
	}
	generation, e := root.OpenRoot("generations/" + s.ManifestDigest[7:])
	if e != nil {
		return s, m, nil, e
	}
	defer func() { _ = generation.Close() }()
	raw, e = readRegular(generation, "manifest.json", 65536)
	if e != nil {
		return s, m, nil, e
	}
	if e = strictModel(raw, &m); e != nil {
		return s, m, nil, e
	}
	dir, e := generation.Open(".")
	if e != nil {
		return s, m, nil, e
	}
	names, e := dir.Readdirnames(130)
	_ = dir.Close()
	if e != nil && e != io.EOF {
		return s, m, nil, e
	}
	if len(names) != len(m.Files)+1 {
		return s, m, nil, fmt.Errorf("extra or missing generation files")
	}
	files := map[string][]byte{}
	for _, entry := range m.Files {
		if !filePattern.MatchString(entry.Name) {
			return s, m, nil, fmt.Errorf("invalid projection name")
		}
		data, e := readRegular(generation, entry.Name, 1048576)
		if e != nil {
			return s, m, nil, e
		}
		files[entry.Name] = data
	}
	for _, name := range names {
		if name != "manifest.json" {
			if _, ok := files[name]; !ok {
				return s, m, nil, fmt.Errorf("extra file")
			}
		}
	}
	return s, m, files, nil
}

func (r *Reader) persist(committed, pending *api.LocalProjectionSelector) error {
	raw, e := Bytes(journal{committed, pending})
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(r.Control, ".reader-state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, filepath.Join(r.Control, "reader-state.json")); e != nil {
		return e
	}
	return syncDirectory(r.Control)
}

// Handle never accepts path/exec arguments and echoes exactly the requested
// selector. Pending state fences rollback even if activation or journaling fails.
func (r *Reader) Handle(request api.LocalProjectionRequest) (api.LocalProjectionSelector, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := request.Selector
	fail := func(code string) (api.LocalProjectionSelector, string, string) { return s, "failed", code }
	if !r.scope(s) {
		return fail("invalid_chain")
	}
	published, m, files, e := r.disk()
	if e != nil {
		return fail("invalid_chain")
	}
	previous := r.committed
	if r.pending != nil {
		previous = r.pending
	}
	result := Validate(request, m, files, published, previous, Facts{Persisted: true, Loaded: r.loaded && r.committed != nil && Equal(s, *r.committed)})
	if result != "accept" {
		return fail(result)
	}
	if r.Verify == nil {
		return fail("projection_pending")
	}
	if e = r.Verify(s, files); e != nil {
		return fail("invalid_chain")
	}
	if request.Operation == "confirm" {
		return s, "loaded", "none"
	}
	if r.Activate == nil {
		return fail("projection_pending")
	}
	if e = r.persist(r.committed, &s); e != nil {
		return fail("projection_pending")
	}
	r.pending = &s
	if e = r.Activate(s, files); e != nil {
		return fail("projection_pending")
	}
	if e = r.persist(&s, nil); e != nil {
		return fail("projection_pending")
	}
	r.committed = &s
	r.pending = nil
	r.loaded = true
	return s, "loaded", "none"
}
