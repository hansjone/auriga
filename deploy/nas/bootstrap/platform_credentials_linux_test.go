package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
)

func TestNASPlatformCredentialDirectoryReceipt(t *testing.T) {
	if os.Geteuid() != 10001 || os.Getegid() != 10001 {
		t.Skip("requires real Linux installer uid/gid10001")
	}
	out := t.TempDir()
	if err := os.Chmod(out, 0755); err != nil {
		t.Fatal(err)
	}
	set := materials.MaterialSet{ManagedDirectories: []string{"network", "network-platform"}, PrivateFiles: []string{"edge.secret", "internal.properties"}}
	for _, name := range networkdeploy.Files {
		set.PublicFiles = append(set.PublicFiles, "network/"+name)
	}
	for _, name := range networkdeploy.CredentialFiles {
		set.PublicFiles = append(set.PublicFiles, "network-platform/"+name)
	}
	guard, err := openNASMaterialGuard(out, set)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	rpc := map[string]string{"ServiceTokenFile": "/private/service.jwt"}
	if err := networkdeploy.Generate(filepath.Join(out, "network"), networkdeploy.Options{Root: "/etc/auriga/network", PlatformDirectory: filepath.Join(out, "network-platform"), SubmitListen: "127.0.0.1:18443", BrokerHost: "127.0.0.1", BrokerURL: "tls://127.0.0.1:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}); err != nil {
		t.Fatal(err)
	}
	if err := materials.EnsureServiceCredentials(out); err != nil {
		t.Fatal(err)
	}
	if err := guard.Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := materials.SealReceipt(out, "platform-directory-fixture", set); err != nil {
		t.Fatal(err)
	}
	if reused, err := materials.CheckReceipt(out, "platform-directory-fixture", set); err != nil || !reused {
		t.Fatalf("valid receipt: %v", err)
	}
	properties, err := os.ReadFile(filepath.Join(out, "network-platform", "platform.properties"))
	if err != nil || !strings.Contains(string(properties), fmt.Sprintf("installation-owner=%d\n", os.Geteuid())) {
		t.Fatal("wrong installation owner", err)
	}
	secret := filepath.Join(out, "network-platform", "gateway.secret")
	if err := os.Chmod(secret, 0640); err != nil {
		t.Fatal(err)
	}
	if guard.Validate(false) == nil {
		t.Fatal("group-readable platform credential accepted")
	}
	if err := os.Chmod(secret, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 1
	if err := os.WriteFile(secret, raw, 0600); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if _, err := materials.CheckReceipt(out, "platform-directory-fixture", set); err == nil {
		t.Fatal("changed platform credential accepted by receipt")
	}
}
