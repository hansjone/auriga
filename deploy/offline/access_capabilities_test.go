package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAccessFixture(t *testing.T, path, mode, scheme string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"mode":"` + mode + `","portalOrigin":{"scheme":"` + scheme + `","host":"example.internal","port":8080}}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAccessCapabilities_HTTPAndIPModeWarns(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "ip", "http")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.Mode != "ip" || r.PortalScheme != "http" {
		t.Fatalf("unexpected access: %+v", r)
	}
	if r.ManagedNetworkState != "unconfigured" || r.ManagedNetworkReason != "portal_http_no_managed_origin" {
		t.Fatalf("unexpected managed network state: %+v", r)
	}
	docLine := r.DoctorLine()
	for _, want := range []string{"WARN 访问形态与可选能力", "mode=ip", "portal=http", "managedNetworkState=unconfigured", "托管网络", "collab 消息网络", "共享模式核心能力不依赖托管网络/HTTPS"} {
		if !strings.Contains(docLine, want) {
			t.Fatalf("DoctorLine %q missing %q", docLine, want)
		}
	}
	statusLine := r.StatusLine()
	if !strings.Contains(statusLine, "managedNetworkState=unconfigured") || !strings.Contains(statusLine, "mode=ip portal=http") {
		t.Fatalf("unexpected StatusLine: %q", statusLine)
	}
}

func TestAccessCapabilities_NonHTTPSOriginRejectedReason(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "domain", "https")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=http://platform.internal:18446\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.ManagedNetworkState != "unconfigured" || r.ManagedNetworkReason != "origin_not_https" {
		t.Fatalf("expected origin_not_https, got %+v", r)
	}
	if !strings.Contains(r.DoctorLine(), "被门禁拒绝") {
		t.Fatalf("expected gate rejection hint in %q", r.DoctorLine())
	}
}

func TestAccessCapabilities_OriginSetButCollabAbsent(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "domain", "https")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=https://platform.internal:18446\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.ManagedNetworkState != "unconfigured" || r.ManagedNetworkReason != "collab_material_absent" {
		t.Fatalf("expected collab_material_absent, got %+v", r)
	}
	if !strings.Contains(r.StatusLine(), "managed-network.json") {
		t.Fatalf("expected managed-network.json hint in %q", r.StatusLine())
	}
}

func TestAccessCapabilities_ConfiguredDomainHTTPSPasses(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "domain", "https")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=\"https://platform.internal:18446\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "config/collab-source"), 0755); err != nil {
		t.Fatal(err)
	}
	mnPath := filepath.Join(data, "config/collab-runtime/platform/managed-network.json")
	if err := os.MkdirAll(filepath.Dir(mnPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mnPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.ManagedNetworkState != "configured" || r.ManagedNetworkReason != "" || !r.CollabConfigured {
		t.Fatalf("expected configured, got %+v", r)
	}
	if !strings.HasPrefix(r.DoctorLine(), "PASS 访问形态与可选能力") {
		t.Fatalf("expected PASS DoctorLine, got %q", r.DoctorLine())
	}
}

func TestAccessCapabilities_IPDirectHTTPSConfiguredPasses(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "ip", "https")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "config/collab-source"), 0755); err != nil {
		t.Fatal(err)
	}
	mnPath := filepath.Join(data, "config/collab-runtime/platform/managed-network.json")
	if err := os.MkdirAll(filepath.Dir(mnPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mnPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.ManagedNetworkState != "configured" || r.ManagedNetworkReason != "" || !r.CollabConfigured {
		t.Fatalf("expected configured for IP direct HTTPS, got %+v", r)
	}
	if !strings.HasPrefix(r.DoctorLine(), "PASS 访问形态与可选能力") {
		t.Fatalf("expected PASS DoctorLine for IP direct HTTPS, got %q", r.DoctorLine())
	}
}

func TestAccessCapabilities_IPHTTPWithInternalManagedIngressPasses(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "ip", "http")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=https://auriga-instance.internal:18446\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "config/collab-source"), 0755); err != nil {
		t.Fatal(err)
	}
	mnPath := filepath.Join(data, "config/collab-runtime/platform/managed-network.json")
	if err := os.MkdirAll(filepath.Dir(mnPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mnPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	r := accessCapabilities(state{Data: data, Access: accessPath})
	if r.ManagedNetworkState != "configured" || r.ManagedNetworkReason != "" || !r.CollabConfigured {
		t.Fatalf("expected configured for IP+HTTP+managed ingress, got %+v", r)
	}
	if !strings.HasPrefix(r.DoctorLine(), "PASS 访问形态与可选能力") {
		t.Fatalf("expected PASS DoctorLine for IP+HTTP+managed ingress, got %q", r.DoctorLine())
	}
}

func TestPrintDeploySummary_IncludesManagedNetworkState(t *testing.T) {
	data := t.TempDir()
	accessPath := filepath.Join(data, "config/materials/access.json")
	writeAccessFixture(t, accessPath, "ip", "http")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_MANAGED_PLATFORM_ORIGIN=\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	printDeploySummary(&buf, state{Data: data, Access: accessPath})
	out := buf.String()
	if !strings.Contains(out, "managedNetworkState=unconfigured") {
		t.Fatalf("printDeploySummary output missing managedNetworkState: %q", out)
	}
}
