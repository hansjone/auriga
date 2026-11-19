package runtimebundle

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWritePublisherRootRejectsLinksAndNonordinaryLinuxPaths(t *testing.T) {
	o, _, _ := publisherRootFixture(t)
	alias := filepath.Join(t.TempDir(), "public-link.pem")
	if err := os.Symlink(o.PublicKeyPath, alias); err != nil {
		t.Fatal(err)
	}
	bad := o
	bad.PublicKeyPath = alias
	_, err := WritePublisherRoot(bad)
	invalid(t, err)
	fifo := filepath.Join(t.TempDir(), "public-fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	bad.PublicKeyPath = fifo
	_, err = WritePublisherRoot(bad)
	invalid(t, err)
	parentAlias := filepath.Join(t.TempDir(), "parent-link")
	if err := os.Symlink(filepath.Dir(o.OutputPath), parentAlias); err != nil {
		t.Fatal(err)
	}
	bad = o
	bad.OutputPath = filepath.Join(parentAlias, "new-root.pb")
	_, err = WritePublisherRoot(bad)
	invalid(t, err)
	outputAlias := filepath.Join(t.TempDir(), "root-link.pb")
	if err := os.Symlink(o.PublicKeyPath, outputAlias); err != nil {
		t.Fatal(err)
	}
	bad.OutputPath = outputAlias
	_, err = WritePublisherRoot(bad)
	invalid(t, err)
}
