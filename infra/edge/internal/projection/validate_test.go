package projection

import (
	"encoding/json"
	"errors"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type vector struct {
	ID       string `json:"id"`
	Expected string `json:"expected"`
	Input    struct {
		Request                   api.LocalProjectionRequest   `json:"request"`
		Manifest                  api.LocalProjectionManifest  `json:"manifest"`
		Files                     map[string]string            `json:"files"`
		Published                 api.LocalProjectionSelector  `json:"published"`
		Previous                  *api.LocalProjectionSelector `json:"previous"`
		Persisted, Loaded, Failed bool
	} `json:"input"`
}

func vectors(t *testing.T) []vector {
	t.Helper()
	raw, e := os.ReadFile("../../../../contracts/network/fixtures/msgnet-local-projection-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var all []vector
	if e = json.Unmarshal(raw, &all); e != nil {
		t.Fatal(e)
	}
	return all
}
func TestAllSharedLocalProjectionVectors(t *testing.T) {
	all := vectors(t)
	if len(all) != 24 {
		t.Fatalf("unexpected vector count: %d", len(all))
	}
	for _, v := range all {
		t.Run(v.ID, func(t *testing.T) {
			i := v.Input
			files := map[string][]byte{}
			for k, b := range i.Files {
				files[k] = []byte(b)
			}
			got := Validate(i.Request, i.Manifest, files, i.Published, i.Previous, Facts{i.Persisted, i.Loaded, i.Failed})
			if got != v.Expected {
				t.Fatalf("got %s want %s", got, v.Expected)
			}
		})
	}
}

func TestGeneratedNestedUnknownFieldsAreRejected(t *testing.T) {
	v := vectors(t)[0]
	s := v.Input.Request.Selector
	raw, e := Bytes(s)
	if e != nil {
		t.Fatal(e)
	}
	raw = append([]byte(`{"exec":"not-permitted",`), raw[1:]...)
	raw, e = Canonical(raw)
	if e != nil {
		t.Fatal(e)
	}
	var decoded api.LocalProjectionSelector
	if e = strictModel(raw, &decoded); e == nil {
		t.Fatal("custom model decoder bypassed field guard")
	}
	if _, e = Canonical([]byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))); e == nil {
		t.Fatal("unbounded nesting")
	}
}

func fixture(t *testing.T) (*Reader, api.LocalProjectionRequest) {
	return fixtureComponent(t, "")
}

func fixtureComponent(t *testing.T, component string) (*Reader, api.LocalProjectionRequest) {
	t.Helper()
	v := vectors(t)[0]
	if component != "" {
		v.Input.Request.Selector.Component = component
		v.Input.Manifest.Component = component
	}
	root, control := t.TempDir(), t.TempDir()
	s := v.Input.Request.Selector
	manifest, e := Bytes(v.Input.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	s.ManifestDigest = Digest(manifest)
	v.Input.Request.Selector = s
	dir := filepath.Join(root, "generations", s.ManifestDigest[7:])
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	selector, e := Bytes(s)
	if e != nil {
		t.Fatal(e)
	}
	for name, raw := range map[string][]byte{filepath.Join(root, "selector.json"): selector, filepath.Join(dir, "manifest.json"): manifest} {
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for name, b := range v.Input.Files {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return &Reader{Root: root, Control: control, Scope: s, Verify: func(api.LocalProjectionSelector, map[string][]byte) error { return nil }, Activate: func(api.LocalProjectionSelector, map[string][]byte) error { return nil }}, v.Input.Request
}

func TestReaderExactRestartAndConfirm(t *testing.T) {
	r, request := fixture(t)
	if _, phase, code := r.Handle(request); phase != "loaded" || code != "none" {
		t.Fatalf("%s/%s", phase, code)
	}
	request.Operation = "confirm"
	if _, phase, _ := r.Handle(request); phase != "loaded" {
		t.Fatal(phase)
	}
	restarted := &Reader{Root: r.Root, Control: r.Control, Scope: r.Scope, Verify: r.Verify, Activate: r.Activate}
	if e := restarted.Restore(); e != nil {
		t.Fatal(e)
	}
	if _, _, code := restarted.Handle(request); code != "projection_pending" {
		t.Fatal("restart falsely confirmed active handles", code)
	}
	request.Operation = "reload"
	if _, phase, _ := restarted.Handle(request); phase != "loaded" {
		t.Fatal(phase)
	}
	request.Selector.MaterialRevision = "2"
	if _, _, code := restarted.Handle(request); code != "version_rollback" {
		t.Fatal(code)
	}
}

func TestReaderConfirmRechecksCurrentRootFenceWithoutReloading(t *testing.T) {
	r, request := fixture(t)
	if _, phase, _ := r.Handle(request); phase != "loaded" {
		t.Fatal(phase)
	}
	r.Verify = func(api.LocalProjectionSelector, map[string][]byte) error {
		return errors.New("authority revoked by latest control")
	}
	r.Activate = func(api.LocalProjectionSelector, map[string][]byte) error {
		t.Fatal("confirm triggered activation")
		return nil
	}
	request.Operation = "confirm"
	if _, phase, code := r.Handle(request); phase != "failed" || code != "invalid_chain" {
		t.Fatalf("revoked snapshot confirmed: %s/%s", phase, code)
	}
}

func TestReaderRejectsDiskTamperingAndMissingVerifier(t *testing.T) {
	for _, mode := range []string{"extra", "corrupt", "selector-newline", "no-verifier"} {
		t.Run(mode, func(t *testing.T) {
			r, request := fixture(t)
			dir := filepath.Join(r.Root, "generations", request.Selector.ManifestDigest[7:])
			switch mode {
			case "extra":
				_ = os.WriteFile(filepath.Join(dir, "extra"), nil, 0600)
			case "corrupt":
				_ = os.WriteFile(filepath.Join(dir, "bundle.json"), []byte("changed"), 0600)
			case "selector-newline":
				f, e := os.OpenFile(filepath.Join(r.Root, "selector.json"), os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, _ = f.WriteString("\n")
				_ = f.Close()
			case "no-verifier":
				r.Verify = nil
			}
			_, phase, _ := r.Handle(request)
			if phase != "failed" {
				t.Fatal("tamper accepted")
			}
			if _, e := os.Stat(filepath.Join(r.Control, "reader-state.json")); !os.IsNotExist(e) {
				t.Fatal("failed load changed durable state")
			}
		})
	}
}

func TestCanonicalRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{} {}`, "\xff", `{"n":1.2}`} {
		if _, e := Canonical([]byte(raw)); e == nil {
			t.Fatal("accepted", raw)
		}
	}
}
