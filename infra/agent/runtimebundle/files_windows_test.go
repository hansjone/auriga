package runtimebundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func shortWindowsPath(t *testing.T, path string) string {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n >= uint32(len(buf)) {
		t.Fatalf("GetShortPathName: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, path) {
		t.Skip("test volume does not provide an 8.3 alias")
	}
	return short
}

func TestWindowsRealShortPathIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public-runtime-root-with-long-name.pb")
	put(t, path, []byte("public path regression only"))
	short := shortWindowsPath(t, path)
	t.Logf("public long path=%q short alias=%q", path, short)
	opened, err := os.Open(short)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(opened.Fd()), &buf[0], uint32(len(buf)), 0)
	_ = opened.Close()
	if err != nil || n >= uint32(len(buf)) {
		t.Fatalf("public fixture handle name: %v", err)
	}
	t.Logf("public handle normalized path=%q", windows.UTF16ToString(buf[:n]))
	f, err := openOrdinary(short, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		t.Fatalf("public fixture: %v", err)
	}
}

func TestWindowsShortPathsCanSignAndVerify(t *testing.T) {
	f := fresh(t)
	f.o.RootPath = shortWindowsPath(t, f.o.RootPath)
	f.o.PrivateKeyPath = shortWindowsPath(t, f.o.PrivateKeyPath)
	f.o.ManifestPath = shortWindowsPath(t, f.o.ManifestPath)
	f.o.ConfigPath = shortWindowsPath(t, f.o.ConfigPath)
	f.o.OutputDir = filepath.Join(shortWindowsPath(t, filepath.Dir(f.o.OutputDir)), "bundle")
	signOK(t, f)
	if _, err := Verify(f.verify()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsShortAliasCannotSupplyBundleRoot(t *testing.T) {
	f := fresh(t)
	signOK(t, f)
	o := f.verify()
	o.TrustedRootPath = shortWindowsPath(t, filepath.Join(f.o.OutputDir, "runtime-keys", "root.pb"))
	_, err := Verify(o)
	invalid(t, err)
}
