//go:build linux

package sharednode

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPoolSocketLongNASDataRootKeepsRealSocketInode(t *testing.T) {
	root := filepath.Join(t.TempDir(), strings.Repeat("long-nas-root-", 12), "control", "host-relay")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "shared-node.sock")
	listener, directory, e := listenPoolSocket(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = directory.Close() }()
	defer func() { _ = listener.Close() }()
	info, e := os.Lstat(path)
	if e != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("real socket missing", e)
	}
	connection, e := net.Dial("unix", listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = connection.Close() }()
	accepted, e := listener.Accept()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = accepted.Close() }()
	if peerUID(accepted) != uint32(os.Geteuid()) {
		t.Fatal("SO_PEERCRED did not identify the actual socket peer")
	}
}
