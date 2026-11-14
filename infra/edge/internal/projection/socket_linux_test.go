//go:build linux

package projection

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestLinuxKernelProjectorPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sock")
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = listener.Close() }()
	result := make(chan error, 1)
	go func() {
		conn, e := listener.AcceptUnix()
		if e != nil {
			result <- e
			return
		}
		defer func() { _ = conn.Close() }()
		result <- projectorPeer(conn)
	}()
	conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	select {
	case e := <-result:
		if (e == nil) != (os.Geteuid() == 10001) {
			t.Fatalf("kernel uid %d, result %v", os.Geteuid(), e)
		}
	case <-time.After(time.Second):
		t.Fatal("peer check blocked")
	}
}

// Opt-in two-process check: the temporary container runs the reader as 65532
// and docker exec runs this same binary as projector UID 10001. No setuid or
// capabilities are granted to either process; the fixtures contain no secrets.
func TestLinuxCrossUIDWire(t *testing.T) {
	role := os.Getenv("AURIGA_MSGNET_I_WIRE_TEST")
	if role == "" {
		t.Skip("requires the isolated two-UID test container")
	}
	const base = "/tmp/msgnet-i-wire"
	if role == "reader" {
		if os.Geteuid() != 65532 || os.Getegid() != 10001 {
			t.Fatal("wrong reader test identity")
		}
		r, request := fixtureComponent(t, "edge")
		if e := os.Mkdir(base, 0755); e != nil {
			t.Fatal(e)
		}
		r.Control = filepath.Join(base, "control")
		if e := os.Mkdir(r.Control, 0710); e != nil {
			t.Fatal(e)
		}
		wire, e := Bytes(request)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(base, "request.json"), wire, 0644); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e = r.Listen(ctx); e != nil {
			t.Fatal(e)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.loaded || r.committed == nil || !Equal(*r.committed, request.Selector) {
			t.Fatal("cross-UID request never loaded")
		}
		return
	}
	if role != "projector" || os.Geteuid() != 10001 {
		t.Fatal("wrong projector test identity")
	}
	raw, e := os.ReadFile(filepath.Join(base, "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	var request api.LocalProjectionRequest
	if e = strictModel(raw, &request); e != nil {
		t.Fatal(e)
	}
	for _, operation := range []string{"reload", "confirm"} {
		request.Operation = operation
		wire, e := Bytes(request)
		if e != nil {
			t.Fatal(e)
		}
		conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(base, "control", "reload.sock"), Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if e = WriteFrame(conn, wire); e != nil {
			_ = conn.Close()
			t.Fatal(e)
		}
		if e = conn.CloseWrite(); e != nil {
			_ = conn.Close()
			t.Fatal(e)
		}
		response, e := ReadFrame(conn)
		_ = conn.Close()
		if e != nil {
			t.Fatal(e)
		}
		expected, e := Canonical(append(append([]byte(`{"failure_code":"none","phase":"loaded","selector":`), mustBytes(t, request.Selector)...), '}'))
		if e != nil || !bytes.Equal(response, expected) {
			t.Fatalf("unexpected %s response: %s", operation, response)
		}
	}
}

func mustBytes(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := Bytes(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestLinuxReaderListenerUIDAndDeniedPeer(t *testing.T) {
	r, _ := fixture(t)
	r.Scope.Component = "edge"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if os.Geteuid() != 65532 {
		if e := r.Listen(ctx); e == nil {
			t.Fatal("wrong reader UID accepted")
		}
		return
	}
	if e := os.Chmod(r.Control, 0710); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- r.Listen(ctx) }()
	path := filepath.Join(r.Control, "reload.sock")
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if _, e := os.Stat(path); e == nil {
			break
		}
		select {
		case e := <-done:
			t.Fatal(e)
		case <-deadline.C:
			t.Fatal("socket startup timeout")
		case <-ticker.C:
		}
	}
	conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, e = conn.Read(b[:]); e == nil {
		t.Fatal("edge UID was accepted as projector")
	}
	_ = conn.Close()
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
	if _, e := os.Stat(filepath.Join(r.Control, "reader-state.json")); !os.IsNotExist(e) {
		t.Fatal("denied peer modified reader state")
	}
}
