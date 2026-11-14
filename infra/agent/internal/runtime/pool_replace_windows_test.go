package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPoolStateReplaceWaitsForTransientDeleteSharing(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "new"), filepath.Join(root, "state")
	if e := os.WriteFile(source, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(target, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	name, e := windows.UTF16PtrFromString(target)
	if e != nil {
		t.Fatal(e)
	}
	handle, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	released := make(chan struct{})
	go func() { time.Sleep(50 * time.Millisecond); _ = windows.CloseHandle(handle); close(released) }()
	err := poolReplace(source, target)
	<-released
	if err != nil {
		t.Fatal(err)
	}
	raw, e := os.ReadFile(target)
	if e != nil || string(raw) != "new" {
		t.Fatal(string(raw), e)
	}
}

func TestPoolStateReplacePersistentConflictPreservesState(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "new"), filepath.Join(root, "state")
	if e := os.WriteFile(source, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(target, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	name, e := windows.UTF16PtrFromString(target)
	if e != nil {
		t.Fatal(e)
	}
	handle, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if e = poolReplace(source, target); e == nil {
		t.Fatal("persistent conflict accepted")
	}
	raw, e := os.ReadFile(target)
	if e != nil || string(raw) != "old" {
		t.Fatal(string(raw), e)
	}
}
