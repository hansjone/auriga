package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIRejectsUnsupportedHosts(t *testing.T) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		t.Skip("unsupported-host boundary runs in the non-Linux CI job")
	}
	for _, args := range [][]string{{"--help"}, {"keygen", "site"}, {"verify", "."}, {"bundle", "--build", "--offline"}} {
		if err := dispatch(args); err == nil || !strings.Contains(err.Error(), "Linux amd64") {
			t.Fatalf("unsupported host accepted %v: %v", args, err)
		}
	}
}

func TestInstallArguments(t *testing.T) {
	for _, args := range [][]string{{"--wat"}, {"--address", "$(id)"}, {"--address", "192.0.2.10", "--domain", "example.com"}, {"--domain", "../evil"}} {
		if _, err := parseInstall(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parseInstall([]string{"--address", "192.0.2.10", "--data", "/srv/auriga"})
	if err != nil || o.Data != "/srv/auriga/auriga-data" {
		t.Fatalf("%+v %v", o, err)
	}
	o, err = parseInstall([]string{"--address", "192.0.2.10", "--data", "/srv/auriga-rehearsal"})
	if err != nil || o.Data != "/srv/auriga-rehearsal/auriga-data" {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestChecksumsRejectTamperingAndExtraFiles(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "payload"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(d); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "payload"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err == nil {
		t.Fatal("accepted tampering")
	}
	if err := writeSums(d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "extra"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err == nil {
		t.Fatal("accepted unlisted file")
	}
}

func TestPrivateResourceKeys(t *testing.T) {
	d := filepath.Join(t.TempDir(), "keys")
	if err := resourceKeys(d); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(d, "platform/release.pk8"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resourceKeys(d); err == nil {
		t.Fatal("silently replaced keys")
	}
	after, _ := os.ReadFile(filepath.Join(d, "platform/release.pk8"))
	if string(before) != string(after) {
		t.Fatal("key changed")
	}
	roots, err := os.ReadFile(filepath.Join(d, "resource-roots.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"release", "revocation", "downgrade"} {
		if !strings.Contains(string(roots), role) {
			t.Fatal(role)
		}
	}
}

func TestWindowsClientCommandsAndHelp(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"client", "build"}, true},
		{[]string{"deps", "baseline", "--client"}, true},
		{[]string{"deps", "fetch", "--client"}, true},
		{[]string{"deps", "import", `D:\kit`, "--client"}, true},
		{[]string{"deps", "fetch"}, false},
		{[]string{"deploy"}, false},
		{[]string{"--help"}, false},
	} {
		if got := windowsClientCommand(c.args); got != c.want {
			t.Errorf("windowsClientCommand(%v)=%v，期望 %v", c.args, got, c.want)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	for _, args := range [][]string{{"deps", "--help"}, {"client", "--help"}, {"client", "build", "--help"}, {"deps", "baseline", "--help"}} {
		if err := dispatch(args); err != nil {
			t.Errorf("Windows 上应能查看 %v 帮助：%v", args, err)
		}
	}
	if err := dispatch([]string{"deploy"}); err == nil || !strings.Contains(err.Error(), "Linux amd64") {
		t.Errorf("Windows 上非客户端命令应拒绝：%v", err)
	}
}
