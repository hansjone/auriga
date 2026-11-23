package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSiteConfigRuntimeUACAndPostDeployHook(t *testing.T) {
	oldGOOS := siteGOOS
	siteGOOS = "linux"
	defer func() { siteGOOS = oldGOOS }()

	img := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("a", 64)
	validJSON := fmt.Sprintf(`{
  "schemaVersion": 1,
  "deps": { "cache": "/srv/auriga/cache" },
  "server": {
    "data": "/srv/auriga/auriga-data",
    "postDeployHook": "/etc/auriga/hooks/post-deploy.sh",
    "install": {
      "domain": "site.example",
      "uacMaterialsDir": "/etc/auriga/uac-materials",
      "runtime": {
        "bundle": "/srv/auriga/runtime/release-1",
        "root": "/srv/auriga/runtime/root.pb",
        "image": %q
      }
    }
  }
}`, img)
	cfg, err := parseSiteConfig([]byte(validJSON))
	if err != nil {
		t.Fatalf("valid site.json rejected: %v", err)
	}
	if cfg.Server.PostDeployHook == nil || *cfg.Server.PostDeployHook != "/etc/auriga/hooks/post-deploy.sh" {
		t.Fatalf("postDeployHook not parsed: %+v", cfg.Server.PostDeployHook)
	}
	if cfg.Server.Install.UACMaterialsDir == nil || *cfg.Server.Install.UACMaterialsDir != "/etc/auriga/uac-materials" {
		t.Fatalf("uacMaterialsDir not parsed: %+v", cfg.Server.Install.UACMaterialsDir)
	}
	if cfg.Server.Install.Runtime == nil || cfg.Server.Install.Runtime.Image != img {
		t.Fatalf("runtime not parsed: %+v", cfg.Server.Install.Runtime)
	}
	o, err := siteInstallOptions(cfg, "/srv/auriga/auriga-data")
	if err != nil {
		t.Fatalf("siteInstallOptions failed: %v", err)
	}
	if o.Runtime != *cfg.Server.Install.Runtime || o.UACMaterialsDir != "/etc/auriga/uac-materials" {
		t.Fatalf("siteInstallOptions lost fields: %+v", o)
	}

	badCases := map[string]string{
		"empty runtime object": `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","install":{"domain":"site.example","runtime":{}}}}`,
		"partial runtime":      `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","install":{"domain":"site.example","runtime":{"bundle":"/srv/r","root":"/srv/r.pb"}}}}`,
		"relative runtime":     fmt.Sprintf(`{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","install":{"domain":"site.example","runtime":{"bundle":"rel/r","root":"/srv/r.pb","image":%q}}}}`, img),
		"tag runtime image":    `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","install":{"domain":"site.example","runtime":{"bundle":"/srv/r","root":"/srv/r.pb","image":"auriga/dsh:latest"}}}}`,
		"relative uac dir":     `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","install":{"domain":"site.example","uacMaterialsDir":"etc/uac"}}}`,
		"relative hook":        `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","postDeployHook":"hooks/post.sh"}}`,
		"traversal hook":       `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga/auriga-data","postDeployHook":"/etc/auriga/../post.sh"}}`,
	}
	for name, raw := range badCases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSiteConfig([]byte(raw)); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func makeRuntimeBundleFixture(t *testing.T) (installRuntimeOptions, string) {
	t.Helper()
	rootDir := t.TempDir()
	bundle := filepath.Join(rootDir, "bundle")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"runtime.pb", "manifest.json", "config.json", "signature.json"} {
		if err := os.WriteFile(filepath.Join(bundle, f), []byte("fixture-"+f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	rootFile := filepath.Join(rootDir, "root.pb")
	if err := os.WriteFile(rootFile, []byte("fixture-root"), 0644); err != nil {
		t.Fatal(err)
	}
	img := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("b", 64)
	// For cross-platform tests, normalize to slash only when validating Linux paths;
	// on Windows installRuntimeOptions.validate() checks path.IsAbs (POSIX '/').
	return installRuntimeOptions{Bundle: bundle, Root: rootFile, Image: img}, rootDir
}

func TestRuntimeAttachFlagsAndHelp(t *testing.T) {
	img := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("c", 64)
	o, err := parseRuntimeAttach([]string{"--bundle", "/srv/rt/b", "--root", "/srv/rt/root.pb", "--image", img, "--data", "/srv/site/auriga-data", "--no-restart"})
	if err != nil {
		t.Fatalf("parseRuntimeAttach failed: %v", err)
	}
	if !o.NoRestart || o.Runtime.Image != img || o.Data != "/srv/site/auriga-data" {
		t.Fatalf("unexpected parsed options: %+v", o)
	}
	for _, bad := range [][]string{
		{},
		{"--bundle", "/srv/rt/b"},
		{"--bundle", "/srv/rt/b", "--root", "/srv/rt/root.pb", "--image", "auriga/dsh:latest"},
		{"--bundle", "/srv/rt/b", "--root", "/srv/rt/root.pb", "--image", img, "extra"},
	} {
		if _, err := parseRuntimeAttach(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
	for _, args := range [][]string{{"runtime", "--help"}, {"runtime", "attach", "--help"}} {
		out, done, err := captureHelp(t, args)
		if !done || err != nil {
			t.Fatalf("help %v failed: done=%v err=%v", args, done, err)
		}
		if !strings.HasPrefix(out, "用法：") || strings.Contains(out, "（无说明）") {
			t.Fatalf("bad help output for %v:\n%s", args, out)
		}
	}
}

func TestAttachInstalledRuntimeLifecycleAndGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installRuntimeOptions.validate requires POSIX absolute paths")
	}
	req, _ := makeRuntimeBundleFixture(t)
	tmp := t.TempDir()
	data := filepath.Join(tmp, "auriga-data")
	meta := data + ".oneclick"
	if err := os.MkdirAll(meta, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}

	oldVerify := verifyInstallRuntimeFunc
	oldAttach := attachBootstrapRuntimeFunc
	defer func() {
		verifyInstallRuntimeFunc = oldVerify
		attachBootstrapRuntimeFunc = oldAttach
	}()

	verifyCalls := 0
	verifyInstallRuntimeFunc = func(pkg string, r installRuntimeOptions) error {
		verifyCalls++
		return nil
	}

	// 1. Unsigned installation is refused.
	unsignedState := state{Data: data, Package: tmp, AllowUnsigned: true}
	if err := attachInstalledRuntime(&unsignedState, tmp, req); err == nil || !strings.Contains(err.Error(), "未签名") {
		t.Fatalf("expected unsigned rejection, got %v", err)
	}

	// 2. Existing runtime is refused (no root replacement).
	withRuntime := state{Data: data, Package: tmp, Runtime: &installRuntime{Source: req, SHA256: strings.Repeat("1", 64)}}
	if err := attachInstalledRuntime(&withRuntime, tmp, req); err == nil || !strings.Contains(err.Error(), "已登记") {
		t.Fatalf("expected already-attached rejection, got %v", err)
	}

	// 3. Bootstrap failure cleans up runtime-inputs and leaves s.Runtime nil.
	attachBootstrapRuntimeFunc = func(s state, pkg string, snap installRuntimeOptions) error {
		return errors.New("bootstrap attach failed")
	}
	s := state{Data: data, Package: tmp, Source: sourceLocalDeploy}
	if err := attachInstalledRuntime(&s, tmp, req); err == nil || !strings.Contains(err.Error(), "bootstrap attach failed") {
		t.Fatalf("expected bootstrap error, got %v", err)
	}
	if s.Runtime != nil {
		t.Fatalf("s.Runtime should remain nil on failure")
	}
	if _, err := os.Lstat(runtimeSnapshot(data)); !os.IsNotExist(err) {
		t.Fatalf("runtime-inputs should be cleaned up on failure")
	}

	// 4. Success persists runtime-inputs and state.json without touching config/materials.
	var gotSnap installRuntimeOptions
	attachBootstrapRuntimeFunc = func(s state, pkg string, snap installRuntimeOptions) error {
		gotSnap = snap
		return nil
	}
	if err := attachInstalledRuntime(&s, tmp, req); err != nil {
		t.Fatalf("attachInstalledRuntime failed: %v", err)
	}
	if s.Runtime == nil || s.Runtime.Source != req || !hex64.MatchString(s.Runtime.SHA256) {
		t.Fatalf("state runtime not populated: %+v", s.Runtime)
	}
	if gotSnap.Bundle != filepath.Join(runtimeSnapshot(data), "bundle") || gotSnap.Root != filepath.Join(runtimeSnapshot(data), "root.pb") {
		t.Fatalf("bootstrap did not receive pinned snapshot: %+v", gotSnap)
	}
	persisted, err := readState(data)
	if err != nil || persisted.Runtime == nil || *persisted.Runtime != *s.Runtime {
		t.Fatalf("state.json not persisted: %+v %v", persisted.Runtime, err)
	}
	if verifyCalls < 3 {
		t.Fatalf("expected verifier to run on original, snapshot, and pinned state, got %d calls", verifyCalls)
	}
}

func TestSyncUACMaterials(t *testing.T) {
	oldCheck := uacMaterialsDirCheck
	oldChown := uacChownFunc
	uacMaterialsDirCheck = func(dir string) error { return nil }
	uacChownFunc = func(path string) error { return nil }
	defer func() {
		uacMaterialsDirCheck = oldCheck
		uacChownFunc = oldChown
	}()

	src := t.TempDir()
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "adapter.json"), []byte(`{"issuer":"https://uac.corp.example"}`), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "uac.properties"), []byte("uac.enabled=true\n"), 0640); err != nil {
		t.Fatal(err)
	}
	// Pre-seed uac.properties in destination to verify existing file is preserved.
	dstDir := filepath.Join(data, "config", "materials", "uac")
	if err := os.MkdirAll(dstDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "uac.properties"), []byte("existing=true\n"), 0640); err != nil {
		t.Fatal(err)
	}

	if err := syncUACMaterials(data, src); err != nil {
		t.Fatalf("syncUACMaterials failed: %v", err)
	}
	gotAdapter, err := os.ReadFile(filepath.Join(dstDir, "adapter.json"))
	if err != nil || !strings.Contains(string(gotAdapter), "uac.corp.example") {
		t.Fatalf("adapter.json not synced: %q %v", gotAdapter, err)
	}
	gotProps, err := os.ReadFile(filepath.Join(dstDir, "uac.properties"))
	if err != nil || string(gotProps) != "existing=true\n" {
		t.Fatalf("existing uac.properties should not be overwritten, got %q %v", gotProps, err)
	}

	// Unknown leaf in source dir must be rejected (prevents unknown NAS source leaf).
	if err := os.WriteFile(filepath.Join(src, "extra.txt"), []byte("nope"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := syncUACMaterials(data, src); err == nil || !strings.Contains(err.Error(), "不允许") {
		t.Fatalf("expected unknown UAC leaf rejection, got %v", err)
	}
}

func TestCheckPostDeployHookValidation(t *testing.T) {
	dir := t.TempDir()
	// Directory is rejected.
	if err := checkPostDeployHook(dir); err == nil {
		t.Fatal("directory hook should be rejected")
	}
	// Missing file is rejected.
	if err := checkPostDeployHook(filepath.Join(dir, "missing.sh")); err == nil {
		t.Fatal("missing hook should be rejected")
	}
	if runtime.GOOS == "windows" {
		// Relative path is rejected.
		if err := checkPostDeployHook("relative\\hook.ps1"); err == nil {
			t.Fatal("relative hook should be rejected")
		}
		return
	}
	// Unix permission / owner checks using injectable hooks.
	hook := filepath.Join(dir, "post-deploy.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	oldOwner, oldParent := siteOwnerOf, siteParentCheck
	siteOwnerOf = func(os.FileInfo) (uint32, error) { return 0, nil }
	siteParentCheck = func(string) error { return nil }
	defer func() {
		siteOwnerOf = oldOwner
		siteParentCheck = oldParent
	}()
	if err := checkPostDeployHook(hook); err != nil {
		t.Fatalf("valid 0700 root hook rejected: %v", err)
	}
	// Group-writable hook rejected.
	if err := os.Chmod(hook, 0770); err != nil {
		t.Fatal(err)
	}
	if err := checkPostDeployHook(hook); err == nil || !strings.Contains(err.Error(), "不可写") && !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("group-writable hook should be rejected, got %v", err)
	}
	// Non-executable hook rejected.
	if err := os.Chmod(hook, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPostDeployHook(hook); err == nil || !strings.Contains(err.Error(), "可执行") {
		t.Fatalf("non-executable hook should be rejected, got %v", err)
	}
	// Non-root owner rejected.
	if err := os.Chmod(hook, 0700); err != nil {
		t.Fatal(err)
	}
	siteOwnerOf = func(os.FileInfo) (uint32, error) { return 1000, nil }
	if err := checkPostDeployHook(hook); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("non-root hook should be rejected, got %v", err)
	}
}

func TestExecutePostDeployHookPhasesAndFailure(t *testing.T) {
	oldRunner := postDeployHookRunner
	defer func() { postDeployHookRunner = oldRunner }()

	hookPath := "/etc/auriga/hooks/post-deploy.sh"
	cfg := &siteConfig{Server: &siteServer{Data: "/srv/auriga/auriga-data", PostDeployHook: &hookPath}}
	s := state{Data: "/srv/auriga/auriga-data", Package: "/srv/auriga/auriga-releases/1", Access: "/srv/auriga/auriga-data.oneclick/access.json"}

	var gotPath, gotPhase string
	var gotState state
	postDeployHookRunner = func(ctx context.Context, path, phase string, st state, w io.Writer) error {
		gotPath, gotPhase, gotState = path, phase, st
		return nil
	}
	var buf bytes.Buffer
	rc := &siteReceipt{}
	if err := executePostDeployHook(context.Background(), cfg, "install", s, rc, &buf); err != nil {
		t.Fatalf("executePostDeployHook install failed: %v", err)
	}
	if gotPath != hookPath || gotPhase != "install" || gotState.Data != s.Data || gotState.Package != s.Package {
		t.Fatalf("unexpected hook invocation: path=%q phase=%q state=%+v", gotPath, gotPhase, gotState)
	}
	if len(rc.Notes) != 1 || !strings.Contains(rc.Notes[0], "install") {
		t.Fatalf("receipt note missing: %+v", rc.Notes)
	}

	postDeployHookRunner = func(ctx context.Context, path, phase string, st state, w io.Writer) error {
		return errors.New("exit status 2")
	}
	if err := executePostDeployHook(context.Background(), cfg, "upgrade", s, rc, &buf); err == nil || !strings.Contains(err.Error(), "部署后置钩子执行失败") {
		t.Fatalf("expected hook failure error, got %v", err)
	}
}

func TestDeployFirstInstallRunsPostDeployHook(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "auriga-data")
	rel := filepath.Join(tmp, "auriga-releases", "1")
	if err := os.MkdirAll(filepath.Join(rel, "deploy-nas"), 0755); err != nil {
		t.Fatal(err)
	}
	domain := "site.example"
	hookPath := "/etc/auriga/hooks/post-deploy.sh"
	uacDir := filepath.Join(tmp, "uac-src")
	cfg := &siteConfig{
		SchemaVersion: 1,
		Deps:          &siteDeps{Cache: filepath.Join(tmp, "cache")},
		Server: &siteServer{
			Data:           data,
			PostDeployHook: &hookPath,
			Install: &siteInstall{
				Domain:          &domain,
				UACMaterialsDir: &uacDir,
			},
		},
	}

	oldSteps := deployInstallSteps
	oldHook := postDeployHookRunner
	oldRun := deployRun
	defer func() {
		deployInstallSteps = oldSteps
		postDeployHookRunner = oldHook
		deployRun = oldRun
	}()
	deployRun = func(ctx context.Context, spec runSpec) error { return nil }
	var capturedOpts installOptions
	deployInstallSteps = func(s *state, o installOptions, register bool, prepared *preparedInstallRuntime, images func(state) (map[string]string, error), timeout time.Duration) error {
		capturedOpts = o
		return nil
	}
	hookCalled := false
	postDeployHookRunner = func(ctx context.Context, path, phase string, s state, w io.Writer) error {
		hookCalled = true
		if path != hookPath || phase != "install" || s.Data != data || s.Package != rel {
			return fmt.Errorf("unexpected hook args: %s %s %+v", path, phase, s)
		}
		return nil
	}
	var out bytes.Buffer
	run := &siteRun{receipt: &siteReceipt{}, out: &out}
	rec := deployRecord{Seq: 1, Release: rel, Images: map[string]string{"PLATFORM": "sha256:" + strings.Repeat("1", 64)}}
	if err := deployFirstInstall(context.Background(), cfg, data, rec, run); err != nil {
		t.Fatalf("deployFirstInstall failed: %v", err)
	}
	if !hookCalled {
		t.Fatal("postDeployHook was not called after first install")
	}
	if capturedOpts.UACMaterialsDir != uacDir {
		t.Fatalf("UACMaterialsDir not passed to installSteps: %q", capturedOpts.UACMaterialsDir)
	}
}
