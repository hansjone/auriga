package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func captureHelp(t *testing.T, args []string) (string, bool, error) {
	t.Helper()
	var b bytes.Buffer
	old := helpOut
	helpOut = &b
	defer func() { helpOut = old }()
	done, err := tryHelp(args)
	return b.String(), done, err
}

func TestTopHelpListsEveryDispatchedCommand(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`args\[0\] == "([a-z-]+)"|case "([a-z]+)":`).FindAllStringSubmatch(string(src), -1) {
		name := m[1] + m[2]
		if name != "help" && !strings.HasPrefix(name, "internal-") {
			names[name] = true
		}
	}
	if len(names) < 15 {
		t.Fatalf("did not find dispatched commands: %v", names)
	}
	for _, args := range [][]string{nil, {"--help"}, {"help"}, {"-h"}} {
		out, done, err := captureHelp(t, args)
		if !done || err != nil {
			t.Fatalf("%v: %v %v", args, done, err)
		}
		for name := range names {
			if !regexp.MustCompile(`(?m)^\s+` + name + `\b`).MatchString(out) {
				t.Errorf("top help lacks command %s", name)
			}
		}
		for _, want := range []string{"devkit verify", "devkit merge", "changes export", "keygen site", "trust add-site-key", "release upload", "<命令> --help"} {
			if !strings.Contains(out, want) {
				t.Errorf("top help lacks %q", want)
			}
		}
	}
}

func TestSubcommandHelpExitsCleanlyWithFlagsAndDefaults(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"install", "--help"}, []string{"--address", "--domain", "--proxy", "--data", "/srv/auriga", "--verify-key", "--trust-fingerprint", "--allow-unsigned", "--package", "--desktop-public-key", "必填"}},
		{[]string{"upgrade", "-h"}, []string{"--data", "/srv/auriga", "--allow-unsigned"}},
		{[]string{"doctor", "--help"}, []string{"--data", "/srv/auriga"}},
		{[]string{"doctor", "--build", "--help"}, []string{"--memory-mib", "4096", "--cpus"}},
		{[]string{"backup", "--help"}, []string{"--data"}},
		{[]string{"status", "--help"}, []string{"--data"}},
		{[]string{"logs", "--help"}, []string{"--data"}},
		{[]string{"stop", "--help"}, []string{"--data"}},
		{[]string{"bundle", "--help"}, []string{"--build", "--offline", "--sign-key", "--memory-mib", "4096", "--release", "auriga-release.json", "必填"}},
		{[]string{"devkit", "--help"}, []string{"--out", "--sign-key", "--new-baseline", "--max-devkit-bytes", "68719476736"}},
		{[]string{"devkit", "verify", "--help"}, []string{"--version", "--source", "--verify-key"}},
		{[]string{"devkit", "merge", "--help"}, []string{"--kit", "--verify-key"}},
		{[]string{"changes", "export", "--help"}, []string{"--out"}},
		{[]string{"changes", "--help"}, []string{"export", "apply"}},
		{[]string{"keygen", "--help"}, []string{"--out", "site-build-key"}},
		{[]string{"trust", "--help"}, []string{"--data", "--confirm-fingerprint", "必填"}},
		{[]string{"verify", "--help"}, []string{"--verify-key", "--trust-fingerprint", "--allow-unsigned"}},
		{[]string{"unpack", "--help"}, []string{"--out", "必填", "--verify-key"}},
		{[]string{"release", "--help"}, []string{"--tag", "--sign-key", "--cache-dir", ".offline-devkit-cache"}},
		{[]string{"release", "upload", "--help"}, []string{"--server", "--execute", "--publish"}},
		{[]string{"help", "install"}, []string{"--address"}},
	}
	for _, c := range cases {
		out, done, err := captureHelp(t, c.args)
		if !done || err != nil {
			t.Fatalf("%v: done=%v err=%v", c.args, done, err)
		}
		if !strings.HasPrefix(out, "用法：") {
			t.Errorf("%v: no usage header: %q", c.args, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v lacks %q:\n%s", c.args, w, out)
			}
		}
		if strings.Contains(out, "（无说明）") {
			t.Errorf("%v has undocumented flag:\n%s", c.args, out)
		}
	}
}

func TestHelpFlagReturnsErrHelpAndBadFlagStaysError(t *testing.T) {
	var b bytes.Buffer
	old := helpOut
	helpOut = &b
	defer func() { helpOut = old }()
	if _, err := parseInstall([]string{"--help"}); err != flag.ErrHelp {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(b.String(), "--address") {
		t.Fatal("help not printed")
	}
	b.Reset()
	flagFailure = ""
	_, err := parseInstall([]string{"--wat"})
	if err == nil || err == flag.ErrHelp || b.Len() != 0 || flagFailure != "install" {
		t.Fatalf("err=%v out=%q failure=%q", err, b.String(), flagFailure)
	}
	flagFailure = ""
}

func testState(t *testing.T) state {
	t.Helper()
	base := t.TempDir()
	s := state{Data: filepath.Join(base, "srv", "auriga-data")}
	s.Package = filepath.Join(base, "srv", "auriga-data.oneclick", "release")
	s.Access = filepath.Join(base, "srv", "auriga-data.oneclick", "access.json")
	for _, d := range []string{s.Data, s.Package} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Package, "offline-release.json"), []byte(`{"version":"0.2.0-test.1"}`), 0644); err != nil {
		t.Fatal(err)
	}
	access := `{"mode":"ip","portalOrigin":{"scheme":"http","host":"192.0.2.10","port":8080},"adminOrigin":{"scheme":"http","host":"192.0.2.10","port":8084}}`
	if err := os.WriteFile(s.Access, []byte(access), 0644); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInstallSummaryAndCustodyList(t *testing.T) {
	s := testState(t)
	secret := "SECRET-TOTP-123456"
	if err := os.MkdirAll(filepath.Join(s.Data, "resource-keys/offline"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Data, "resource-keys/offline/downgrade.pk8"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	var access accessSummary
	if err := readJSONLoose(s.Access, &access); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := writeInstallSummary(s, access, now); err != nil {
		t.Fatal(err)
	}
	if err := writeInstallSummary(s, access, now.Add(time.Hour)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	summary := filepath.Join(s.Data, installSummaryName)
	raw, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"0.2.0-test.1", "2026-01-02T04:04:05Z", "ip", "http://192.0.2.10:8084/", "http://192.0.2.10:8080/", s.Data,
		"未签名安装", filepath.Join(s.Data, "resource-keys/resource-roots.json"), filepath.Join(s.Data, "resource-keys/offline/downgrade.pk8"),
		filepath.Join(s.Data+".oneclick", "site-build-key"), filepath.Join(filepath.Dir(s.Data), "auriga-backups"), "应急账号口令与 TOTP",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, secret) || strings.Contains(text, "BEGIN") {
		t.Fatal("summary leaks key material")
	}
	if _, err := os.Stat(summary + ".new"); err == nil {
		t.Fatal("temp file left behind")
	}
	if info, err := os.Stat(summary); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("mode %v %v", info, err)
	}
	var out bytes.Buffer
	printCustody(&out, s)
	for _, want := range []string{"⚠ 请立即保管以下材料", "resource-roots.json", "downgrade.pk8", "site-build-key", "auriga-backups", "TOTP", installSummaryName} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("custody list lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("custody list leaks key material")
	}
}
