//go:build unix

package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGuardFileThenEveryParentBarrierIncludingPrecreatedCLIPath(t *testing.T) {
	for _, precreated := range []bool{false, true} {
		for failAt := -1; ; failAt++ {
			base := t.TempDir()
			dir := filepath.Join(base, "new-a", "new-b", "agent")
			want := []string{"file"}
			for current := dir; ; current = filepath.Dir(current) {
				want = append(want, current)
				if filepath.Dir(current) == current {
					break
				}
			}
			if failAt >= len(want) {
				break
			}
			name := fmt.Sprintf("precreated-%t/fail-%d", precreated, failAt)
			t.Run(name, func(t *testing.T) {
				if precreated {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, ".layout"), []byte("auriga-agent\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				var calls []string
				injected := errors.New("injected sync failure")
				err := createTrustGuard(dir, guardTestValue(), func(f *os.File, actual string) error {
					record := func(label string) error {
						calls = append(calls, label)
						if len(calls)-1 == failAt {
							return injected
						}
						return nil
					}
					return syncGuardDirectories(func() error {
						if err := record("file"); err != nil {
							return err
						}
						return f.Sync()
					}, func(path string) error {
						if err := record(path); err != nil {
							return err
						}
						return syncGuardDirectory(path)
					}, actual)
				})
				if failAt < 0 {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if !errors.Is(err, injected) {
						t.Fatalf("sync failure ignored: %v", err)
					}
					want = want[:failAt+1]
				}
				if !reflect.DeepEqual(calls, want) {
					t.Fatalf("barrier order %q want %q", calls, want)
				}
				if _, err := ReadTrustGuard(dir); err != nil {
					t.Fatalf("guard evidence lost after barrier result: %v", err)
				}
				if err := CheckFreshEnrollment(dir); err == nil {
					t.Fatal("existing guard became fresh")
				}
				if _, err := os.Stat(filepath.Join(dir, "state.db")); !os.IsNotExist(err) {
					t.Fatal("barrier failure allowed database creation")
				}
			})
		}
	}
}

type fakeGuardDirectory struct {
	calls             *[]string
	syncErr, closeErr error
}

func (f *fakeGuardDirectory) Sync() error  { *f.calls = append(*f.calls, "sync"); return f.syncErr }
func (f *fakeGuardDirectory) Close() error { *f.calls = append(*f.calls, "close"); return f.closeErr }

func TestGuardDirectoryOpenSyncCloseErrors(t *testing.T) {
	for _, failure := range []string{"none", "open", "sync", "close", "sync and close"} {
		t.Run(failure, func(t *testing.T) {
			var calls []string
			injected := errors.New("directory IO failure")
			closeErr := errors.New("directory close failure")
			err := syncGuardDirectoryWithOpen("literal[1]", func(path string) (guardDirectoryHandle, error) {
				calls = append(calls, "open")
				if path != "literal[1]" {
					t.Fatal("directory reinterpreted")
				}
				if failure == "open" {
					return nil, injected
				}
				f := &fakeGuardDirectory{calls: &calls}
				if failure == "sync" || failure == "sync and close" {
					f.syncErr = injected
				}
				if failure == "close" || failure == "sync and close" {
					f.closeErr = closeErr
				}
				return f, nil
			})
			want := []string{"open", "sync", "close"}
			if failure == "open" {
				want = want[:1]
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("directory handle not closed correctly: %q", calls)
			}
			if (err == nil) != (failure == "none") {
				t.Fatalf("failure %s lost: %v", failure, err)
			}
			if (failure == "open" || failure == "sync" || failure == "sync and close") && !errors.Is(err, injected) {
				t.Fatal("original sync/open error lost")
			}
			if (failure == "close" || failure == "sync and close") && !errors.Is(err, closeErr) {
				t.Fatal("close error lost")
			}
		})
	}
}

func TestGuardBarrierResolvesLiteralSymlinkParent(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "actual", "inside")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias[1]")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	// A normal symlink alias keeps all file users on the same physical tree.
	dir := alias + "/new?/deep*/agent"
	var actualPath string
	if err := createTrustGuard(dir, guardTestValue(), func(f *os.File, actual string) error {
		actualPath = actual
		return syncTrustGuard(f, actual)
	}); err != nil {
		t.Fatal(err)
	}
	if actualPath != filepath.Join(base, "actual", "inside", "new?", "deep*", "agent") {
		t.Fatalf("wrong physical durability path: %q", actualPath)
	}
	if err := CheckFreshEnrollment(dir); err == nil {
		t.Fatal("literal symlink path lost historical guard")
	}
}

func TestUnreadableDirectoryIsNotFresh(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode-based readdir denial; injected read failure is covered on all platforms")
	}
	dir := filepath.Join(t.TempDir(), "denied")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o100); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if err := CheckFreshEnrollment(dir); !os.IsPermission(errors.Unwrap(err)) {
		t.Fatalf("directory read denial not propagated: %v", err)
	}
}

func TestDanglingInitializationLinksInLiteralDirectory(t *testing.T) {
	for _, leaf := range []string{KeyFileName, "." + TrustGuardFileName + ".tmp.link"} {
		t.Run(leaf, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "literal[1]*?", "agent")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("missing-target", filepath.Join(dir, leaf)); err != nil {
				t.Fatal(err)
			}
			if err := CheckFreshEnrollment(dir); err == nil {
				t.Fatal("dangling initialization link ignored")
			}
		})
	}
}
