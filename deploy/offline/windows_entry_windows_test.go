package main

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPowerShell51ArchiveChangesAndClientDispatch(t *testing.T) {
	exe := os.Getenv("AURIGA_WINDOWS_EXE")
	if exe == "" {
		exe = filepath.Join(t.TempDir(), "auriga-cli.exe")
		if raw, err := exec.Command("go", "build", "-buildvcs=false", "-o", exe, ".").CombinedOutput(); err != nil {
			t.Fatal("compile native CLI for PowerShell 5.1", string(raw), err)
		}
	}
	root := filepath.Join(t.TempDir(), "source with spaces")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile("../../auriga.ps1", filepath.Join(root, "auriga.ps1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(exe, filepath.Join(root, "bin/auriga-cli.exe"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := signedTestSourceManifest(t, root, provenance{Commit: strings.Repeat("a", 40), UpstreamVersion: "1.0", Version: "1.0"}, strings.Repeat("b", 40), release{Version: "1.0", Build: &buildSpec{}}); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	if err := signingPublic(key, filepath.Join(root, "release-public.pem")); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	clean := filepath.Join(t.TempDir(), "clean copy")
	if err := copyTree(root, clean); err != nil {
		t.Fatal(err)
	}
	run := func(source string, args ...string) ([]byte, error) {
		cmd := exec.Command("powershell.exe", append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(source, "auriga.ps1")}, args...)...)
		return cmd.CombinedOutput()
	}
	if raw, err := run(root, "verify", ".", "--verify-key", filepath.Join(root, "release-public.pem")); err != nil || !strings.Contains(string(raw), "PASS") {
		t.Fatal(string(raw), err)
	}
	if raw, err := run(root, "verify", "."); err == nil || !strings.Contains(string(raw), "仅完整性，未核验发行身份") {
		t.Fatal("PowerShell did not propagate identity rejection", string(raw), err)
	}
	keydir := filepath.Join(t.TempDir(), "private site key")
	if raw, err := run(root, "keygen", "site", "--out", keydir); err != nil || !strings.Contains(string(raw), "SHA256(SPKI)") {
		t.Fatal("PowerShell site keygen failed", string(raw), err)
	}
	if err := os.WriteFile(filepath.Join(root, "new file.txt"), []byte("line1\r\nline2\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "patch with spaces.tar")
	if raw, err := run(root, "changes", "export", "--out", patch); err != nil {
		t.Fatal(string(raw), err)
	}
	if raw, err := run(clean, "changes", "apply", patch); err != nil {
		t.Fatal(string(raw), err)
	}
	if raw, err := os.ReadFile(filepath.Join(clean, "new file.txt")); err != nil || string(raw) != "line1\nline2\n" {
		t.Fatal(string(raw), err)
	}
	cli := filepath.Join(root, "scripts/client-release/cli.mjs")
	if err := os.MkdirAll(filepath.Dir(cli), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("if (JSON.stringify(process.argv.slice(2)) === JSON.stringify(['client','sign','path with spaces'])) process.exit(7); process.exit(9);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Fatal("client dispatch test requires a local approved Node", err)
	}
	t.Setenv("AURIGA_CLIENT_DEVKIT", "")
	t.Setenv("AURIGA_CLIENT_NODE", node)
	_, err = run(root, "client", "sign", "path with spaces")
	var exitCode int
	if e, ok := err.(*exec.ExitError); ok {
		exitCode = e.ExitCode()
	}
	if exitCode != 7 {
		t.Fatal("client dispatch lost argv or exit status", err)
	}
}

func TestWindowsBusyFileRenameRetries(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "new")
	target := filepath.Join(root, "old")
	for _, path := range []string{source, target} {
		if err := os.WriteFile(path, []byte(path), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ptr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(ptr, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { time.Sleep(150 * time.Millisecond); done <- syscall.CloseHandle(handle) }()
	if err := renamePortable(source, target); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTransferDirectoryOnlyCurrentUser(t *testing.T) {
	root := t.TempDir()
	if err := protectTransferRoot(root); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "acl.ps1")
	if err := os.WriteFile(script, []byte("param([string]$Target)\r\n(Get-Acl -LiteralPath $Target).GetAccessRules($true,$true,[System.Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value }\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, root).CombinedOutput()
	if err != nil {
		t.Fatal(string(raw), err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	entries := strings.Fields(string(raw))
	if len(entries) == 0 {
		t.Fatal("missing explicit owner access")
	}
	for _, sid := range entries {
		if sid != current.Uid {
			t.Fatal("transfer directory exposed to another identity", sid)
		}
	}
}
