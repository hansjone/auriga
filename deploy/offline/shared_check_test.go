package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSharedRuntime_AbsentSkips(t *testing.T) {
	data := t.TempDir()
	r := checkSharedRuntime(state{Data: data})
	if r.Present {
		t.Fatalf("expected absent, got %+v", r)
	}
	if got := r.DoctorLine(); got != "SKIP 共享模式运行态 — 未启用" {
		t.Fatalf("unexpected DoctorLine: %q", got)
	}
}

func TestCheckSharedRuntime_XFSWithAvailableBinaryPasses(t *testing.T) {
	origProbe := sharedXFSQuotaProbe
	t.Cleanup(func() { sharedXFSQuotaProbe = origProbe })
	var probedImg, probedBin string
	sharedXFSQuotaProbe = func(img, bin string) error {
		probedImg, probedBin = img, bin
		return nil
	}

	data := t.TempDir()
	gen := filepath.Join(data, "config/shared-source/v-00000000000000000001")
	if err := os.MkdirAll(gen, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gen, "installation.receipt"), []byte(`{"version":1,"storageBackend":"xfs","quotaMount":"/srv/auriga/data/agent/pools","quotaBinary":"/usr/sbin/xfs_quota"}`), 0600); err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(data, "config/shared-replay-source")
	if err := os.MkdirAll(replay, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replay, "bucket.receipt"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replay, "activation.receipt"), []byte(`{"open":true}`), 0600); err != nil {
		t.Fatal(err)
	}

	r := checkSharedRuntime(state{Data: data, Images: map[string]string{"AGENT": "auriga/agent:test"}})
	if !r.Present || r.StorageBackend != "xfs" || !r.ReplayReady || !r.XFSBinaryAvailable {
		t.Fatalf("unexpected report: %+v", r)
	}
	if probedImg != "auriga/agent:test" || probedBin != "/usr/sbin/xfs_quota" {
		t.Fatalf("unexpected probe args: img=%q bin=%q", probedImg, probedBin)
	}
	line := r.DoctorLine()
	if !strings.HasPrefix(line, "PASS 共享模式运行态") || !strings.Contains(line, "backend=xfs") || !strings.Contains(line, "quotaBinary=/usr/sbin/xfs_quota(ok)") {
		t.Fatalf("unexpected DoctorLine: %q", line)
	}
}

func TestCheckSharedRuntime_XFSMissingBinaryWarns(t *testing.T) {
	origProbe := sharedXFSQuotaProbe
	t.Cleanup(func() { sharedXFSQuotaProbe = origProbe })
	sharedXFSQuotaProbe = func(img, bin string) error {
		return errors.New("exit status 1")
	}

	data := t.TempDir()
	gen := filepath.Join(data, "config/shared-source/v-00000000000000000001")
	if err := os.MkdirAll(gen, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gen, "installation.receipt"), []byte(`{"version":1,"storageBackend":"xfs","quotaMount":"/srv/auriga/data/agent/pools"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_IMAGE_AGENT=auriga/agent:r2\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := checkSharedRuntime(state{Data: data})
	if !r.Present || !r.XFSBinaryChecked || r.XFSBinaryAvailable {
		t.Fatalf("expected missing xfs binary warning, got %+v", r)
	}
	line := r.DoctorLine()
	if !strings.HasPrefix(line, "WARN 共享模式运行态") || !strings.Contains(line, "quotaBinary=/usr/sbin/xfs_quota(missing)") {
		t.Fatalf("unexpected DoctorLine: %q", line)
	}
}
