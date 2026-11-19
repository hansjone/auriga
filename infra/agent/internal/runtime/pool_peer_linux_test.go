//go:build linux

package runtime

import (
	"os"
	"testing"
)

func TestPoolPeerKernelIdentityUsesStartTimeAndParent(t *testing.T) {
	identity, err := readPoolProc(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if identity.parent != os.Getppid() || identity.start == "" {
		t.Fatal("not the real kernel process identity", identity)
	}
	again, err := readPoolProc(os.Getpid())
	if err != nil || again != identity {
		t.Fatal("process lifetime changed unexpectedly", err)
	}
	if _, err = readPoolProc(-1); err == nil {
		t.Fatal("invalid process accepted")
	}
}
