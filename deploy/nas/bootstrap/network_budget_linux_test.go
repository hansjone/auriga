//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNetworkBudgetCommandKeepsSourcesAndReaderOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("projection requires installer chown capability")
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "source.json")
	broker := filepath.Join(dir, "broker.source")
	out := filepath.Join(dir, "out.json")
	bout := filepath.Join(dir, "out.conf")
	raw := []byte(`{"redeemTimeoutMillis":1000,"requestTimeoutMillis":250,"activeIntervalMillis":250,"protocolSigning":{"privateKeyFile":"unchanged"}}`)
	b := []byte("tls {timeout: 2}\nauthorization {timeout: 2, auth_callout {issuer: SAME_ROOT}}\n")
	for p, v := range map[string][]byte{runtime: raw, broker: b} {
		if e := os.WriteFile(p, v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	args := []string{"-runtime", runtime, "-broker", broker, "-out-runtime", out, "-out-broker", bout}
	for range 2 {
		if e := networkBudgetCommand(args); e != nil {
			t.Fatal(e)
		}
	}
	for p, v := range map[string][]byte{runtime: raw, broker: b} {
		got, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(got, v) {
			t.Fatal("source altered")
		}
	}
	for _, p := range []string{out, bout} {
		f, e := os.Stat(p)
		if e != nil || f.Mode().Perm() != 0600 {
			t.Fatal("projection mode")
		}
		st := f.Sys().(*syscall.Stat_t)
		if st.Uid != 10001 || st.Gid != 10001 {
			t.Fatal("projection owner")
		}
	}
	if e := networkBudgetCommand([]string{"-runtime", runtime, "-broker", broker, "-out-runtime", filepath.Join(dir, ".", "source.json"), "-out-broker", bout}); e == nil {
		t.Fatal("source alias accepted")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(out, link); e != nil {
		t.Fatal(e)
	}
	if e := networkBudgetCommand([]string{"-runtime", runtime, "-broker", broker, "-out-runtime", link, "-out-broker", bout}); e == nil {
		t.Fatal("symlink output accepted")
	}
}
