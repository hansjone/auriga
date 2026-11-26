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

// TestBuildNodePinNodeInterop：Go 读到的 build-node.json 与 Node inputs.mjs 导出的
// BUILD_NODE / BUILD_NODE_ZIP_SHA256 一致（第 9/11 步 zip 摘要钉死后的互操作）。
func TestBuildNodePinNodeInterop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not installed; cross-language check runs on Windows toolchain")
	}
	root := repoRoot(t)
	var pin buildNodePin
	if err := readJSON(filepath.Join(root, "scripts", "client-release", "build-node.json"), &pin); err != nil {
		t.Fatal(err)
	}
	if pin.Version == "" || !hex64.MatchString(pin.WinX64ZipSha256) || len(pin.WinX64ZipSha256Sources) < 2 ||
		!hex64.MatchString(pin.LinuxX64TarSha256) || len(pin.LinuxX64TarSha256Sources) < 2 {
		t.Fatalf("build-node.json 钉死字段不完整：%+v", pin)
	}
	module, err := filepath.Abs(filepath.Join(root, "scripts", "client-release", "inputs.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(module)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	script := `const m=await import(process.argv[1]);const lp=m.readBuildNodePin(m.ROOT,'linux-x64');console.log(JSON.stringify({version:m.BUILD_NODE,sha:m.BUILD_NODE_ZIP_SHA256,linuxSha:lp.linuxX64TarSha256}));`
	out, err := exec.Command(node, "--input-type=module", "-e", script, uri).CombinedOutput()
	if err != nil {
		t.Fatalf("Node failed: %v %s", err, out)
	}
	var actual struct {
		Version  string `json:"version"`
		SHA      string `json:"sha"`
		LinuxSHA string `json:"linuxSha"`
	}
	if err := json.Unmarshal(out, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Version != pin.Version || actual.SHA != pin.WinX64ZipSha256 || actual.LinuxSHA != pin.LinuxX64TarSha256 {
		t.Fatalf("Go↔Node build-node 钉死不一致：go=%+v node=%+v", pin, actual)
	}
}

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
