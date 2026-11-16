package main

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopSourceNodeInterop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not installed; cross-language check runs on Windows toolchain")
	}
	root := t.TempDir()
	for name, body := range map[string]string{"main.go": "package example\n", "go.mod": "module example\n", "install.cmd": "@echo off\r\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	info := provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}
	if err := signedTestSourceManifest(t, root, info, strings.Repeat("b", 40), release{Version: "1.0", Build: &buildSpec{}}); err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs("../../scripts/client-release/source.mjs")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(module)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	script := `const {sourceIdentity}=await import(process.argv[1]);console.log(JSON.stringify(sourceIdentity(process.argv[2],{verifyKey:process.argv[3]})));`
	out, err := exec.Command(node, "--input-type=module", "-e", script, uri, root, filepath.Join(root, "release-public.pem")).CombinedOutput()
	if err != nil {
		t.Fatalf("Node failed: %v %s", err, out)
	}
	var actual struct {
		Commit  string `json:"sourceCommit"`
		Changes bool   `json:"siteChanges"`
		Kind    string `json:"sourceType"`
	}
	if err := json.Unmarshal(out, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Commit != info.Commit || actual.Changes || actual.Kind != "signed-archive" {
		t.Fatal(string(out))
	}
}
