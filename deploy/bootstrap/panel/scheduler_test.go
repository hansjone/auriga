package panel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenewalContinuesAfterFailureAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checks, failures := 0, 0
	err := Renew(ctx, time.Millisecond, func(time.Time) error {
		checks++
		if checks == 1 {
			return fmt.Errorf("signer unavailable")
		}
		cancel()
		return nil
	}, func(err error) {
		if err != nil {
			failures++
		}
	})
	if err != nil || checks != 2 || failures != 1 {
		t.Fatalf("checks=%d failures=%d err=%v", checks, failures, err)
	}
	if Renew(context.Background(), 0, func(time.Time) error { return nil }, func(error) {}) == nil {
		t.Fatal("nonpositive interval accepted")
	}
}

func TestListenerHealthIndependentOfRenewalAndArtifacts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, ".panel-renew-failed"), []byte("failed"), 0600); err != nil {
		t.Fatal(err)
	}
	// There is deliberately no heartbeat or manifest. Edge liveness only probes
	// its own listener; panel state remains separately observable.
	if err := CheckListener(listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if CheckListener(listener.Addr().String()) == nil {
		t.Fatal("closed edge listener reported live")
	}
	for _, address := range []string{"example.com:80", "192.0.2.10:8080", ":8080", "127.0.0.1:0"} {
		if CheckListener(address) == nil {
			t.Fatalf("unsafe probe accepted: %s", address)
		}
	}
}
