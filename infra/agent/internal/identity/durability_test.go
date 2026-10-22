package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardTestValue() TrustGuard {
	return TrustGuard{Version: 1, Binding: strings.Repeat("a", 64), Certificate: strings.Repeat("b", 64)}
}

func TestLiteralDirectoryReadErrorsAndMissing(t *testing.T) {
	dir := t.TempDir()
	readErr := &os.PathError{Op: "readdir", Path: dir, Err: os.ErrPermission}
	err := checkFreshEnrollment(dir, func(string) ([]os.DirEntry, error) { return nil, readErr })
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("directory read error ignored: %v", err)
	}
	if err := CheckFreshEnrollment(filepath.Join(dir, "not-created", "agent")); err != nil {
		t.Fatalf("missing directory rejected: %v", err)
	}
	if err := CheckFreshEnrollment(dir); err != nil {
		t.Fatalf("empty directory rejected: %v", err)
	}
}

func TestGuardWrittenBeforePersistenceAndFailureRetainsEvidence(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "new ancestors"}[nested], func(t *testing.T) {
			base := t.TempDir()
			dir := base
			if nested {
				dir = filepath.Join(base, "new[1]", "new[2]", "agent")
			}
			injected := errors.New("persistence barrier failure")
			called := false
			g := guardTestValue()
			err := createTrustGuard(dir, g, func(f *os.File, actual string) error {
				called = true
				resolved, e := filepath.EvalSymlinks(dir)
				if e != nil {
					t.Fatal(e)
				}
				resolved, e = filepath.Abs(resolved)
				if e != nil {
					t.Fatal(e)
				}
				if actual != resolved {
					t.Fatalf("wrong actual initialization path %q vs %q", actual, resolved)
				}
				raw, e := os.ReadFile(f.Name())
				if e != nil {
					t.Fatal(e)
				}
				want, _ := json.Marshal(g)
				if !bytes.Equal(raw, want) {
					t.Fatal("barrier ran before complete guard write")
				}
				return injected
			})
			if !called || !errors.Is(err, injected) {
				t.Fatalf("barrier failure lost: %v", err)
			}
			if err := CheckFreshEnrollment(dir); err == nil {
				t.Fatal("failed barrier erased initialization evidence")
			}
			if _, err := os.Stat(filepath.Join(dir, "state.db")); !os.IsNotExist(err) {
				t.Fatal("database created before successful barrier")
			}
			if err := createTrustGuard(dir, g, func(*os.File, string) error {
				t.Fatal("failed initialization was silently retried")
				return nil
			}); err == nil {
				t.Fatal("guard replaced after persistence failure")
			}
		})
	}
}

func TestGuardCloseFailureIsNotSuccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	err := createTrustGuard(dir, guardTestValue(), func(f *os.File, _ string) error { return f.Close() })
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("guard close error ignored: %v", err)
	}
	if err := CheckFreshEnrollment(dir); err == nil {
		t.Fatal("close failure removed guard evidence")
	}
}

func TestMissingParentTraversalCannotHideExistingWriterTarget(t *testing.T) {
	base := t.TempDir()
	actual := filepath.Join(base, "agent")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(actual, ".node.key.tmp.crashed")
	want := []byte("historical writer target")
	if err := os.WriteFile(evidence, want, 0o600); err != nil {
		t.Fatal(err)
	}
	literal := filepath.Join(base, "not-created") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "agent"
	if err := CheckFreshEnrollment(literal); err == nil {
		t.Fatal("unreachable raw parent hid history in the target used by filepath.Join writers")
	}
	got, err := os.ReadFile(evidence)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("probe changed evidence")
	}
	if _, err := os.Stat(filepath.Join(base, "not-created")); !os.IsNotExist(err) {
		t.Fatal("read-only probe created missing parent")
	}
}
