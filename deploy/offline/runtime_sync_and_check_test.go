package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 轻量 protobuf 编码辅助函数（仅测试构造 fixture 使用）。
func pbBytesField(fieldNum uint64, val []byte) []byte {
	var out []byte
	out = binary.AppendUvarint(out, (fieldNum<<3)|2)
	out = binary.AppendUvarint(out, uint64(len(val)))
	return append(out, val...)
}

func pbStringField(fieldNum uint64, val string) []byte {
	return pbBytesField(fieldNum, []byte(val))
}

func pbVarintField(fieldNum uint64, val uint64) []byte {
	var out []byte
	out = binary.AppendUvarint(out, (fieldNum<<3)|0)
	return binary.AppendUvarint(out, val)
}

func encodeTestDshRuntimeProto(image, dshVersion, imageDigest string, seq uint64) []byte {
	var env []byte
	if dshVersion != "" {
		env = append(env, pbStringField(5, dshVersion)...)
	}
	if imageDigest != "" {
		env = append(env, pbStringField(9, imageDigest)...)
	}
	if seq > 0 {
		env = append(env, pbVarintField(10, seq)...)
	}
	var dsh []byte
	if dshVersion != "" {
		dsh = append(dsh, pbStringField(2, dshVersion)...)
	}
	if image != "" {
		dsh = append(dsh, pbStringField(7, image)...)
	}
	if len(env) > 0 {
		dsh = append(dsh, pbBytesField(9, env)...)
	}
	return dsh
}

func encodeTestInstanceSpecProto(dshBytes []byte) []byte {
	var spec []byte
	spec = append(spec, pbStringField(1, "inst-template")...)
	spec = append(spec, pbBytesField(6, dshBytes)...)
	return spec
}

func writeTestRuntimeSlot(t *testing.T, dataDir, slot, image, dshVersion, pluginLabel string, seq uint64, pending bool) {
	t.Helper()
	sourceDir := filepath.Join(dataDir, "config", "runtime-source")
	slotDir := filepath.Join(sourceDir, slot)
	_, digestPart, _ := strings.Cut(image, "@")
	hexDigest := strings.TrimPrefix(digestPart, "sha256:")
	for _, rel := range []string{
		"release",
		"templates",
		filepath.Join("release", "runtime-proofs", hexDigest),
	} {
		if err := os.MkdirAll(filepath.Join(slotDir, rel), 0755); err != nil {
			t.Fatal(err)
		}
	}
	dshRaw := encodeTestDshRuntimeProto(image, dshVersion, digestPart, seq)
	specRaw := encodeTestInstanceSpecProto(dshRaw)
	if err := os.WriteFile(filepath.Join(slotDir, "release", "runtime.pb"), dshRaw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotDir, "templates", "real-dsh.pb"), specRaw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotDir, "release", "runtime-proofs", hexDigest, "manifest.json"), []byte(`{"schemaVersion":2}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfgPayload := map[string]any{"config": map[string]any{"Labels": map[string]string{}}}
	if pluginLabel != "" {
		cfgPayload["config"] = map[string]any{"Labels": map[string]string{"io.auriga.dsh.plugin.version": pluginLabel}}
	}
	cfgRaw, _ := json.Marshal(cfgPayload)
	if err := os.WriteFile(filepath.Join(slotDir, "release", "runtime-proofs", hexDigest, "config.json"), cfgRaw, 0644); err != nil {
		t.Fatal(err)
	}
	activeRaw, _ := json.Marshal(runtimeActivation{Current: slot, Pending: pending})
	if err := os.WriteFile(filepath.Join(sourceDir, "active.json"), activeRaw, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeTestExpectedSource(t *testing.T, dir, dshVersion, pluginVersion string) {
	t.Helper()
	if dshVersion != "" {
		p := filepath.Join(dir, "deploy", "compose", "real-dsh")
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "versions.json"), []byte(`{"dshVersion":"`+dshVersion+`"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if pluginVersion != "" {
		p := filepath.Join(dir, "clients", "dsh-plugin")
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(`{"name":"auriga-dsh","version":"`+pluginVersion+`"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseRuntimeSyncPinFlags(t *testing.T) {
	img := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("d", 64)
	o, err := parseRuntimeSyncPin([]string{"--bundle", "/srv/rt/r2", "--root", "/srv/rt/root.pb", "--image", img, "--data", "/srv/site/auriga-data"})
	if err != nil {
		t.Fatalf("parseRuntimeSyncPin failed: %v", err)
	}
	if o.Runtime.Bundle != "/srv/rt/r2" || o.Runtime.Root != "/srv/rt/root.pb" || o.Runtime.Image != img || o.Data != "/srv/site/auriga-data" {
		t.Fatalf("unexpected options: %+v", o)
	}
	for _, bad := range [][]string{
		{},
		{"--bundle", "/srv/rt/r2"},
		{"--bundle", "/srv/rt/r2", "--root", "/srv/rt/root.pb"},
		{"--bundle", "rel/r2", "--root", "/srv/rt/root.pb", "--image", img},
		{"--bundle", "/srv/rt/r2", "--root", "/srv/rt/../root.pb", "--image", img},
		{"--bundle", "/srv/rt/r2", "--root", "/srv/rt/root.pb", "--image", "auriga/dsh:latest"},
		{"--bundle", "/srv/rt/r2", "--root", "/srv/rt/root.pb", "--image", img, "extra"},
	} {
		if _, err := parseRuntimeSyncPin(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

func TestReadActiveRuntimeInfo_SourcesAndErrors(t *testing.T) {
	// 1. Unconfigured site
	emptyData := t.TempDir()
	if info := readActiveRuntimeInfo(emptyData); info.Configured {
		t.Fatalf("expected unconfigured, got %+v", info)
	}

	// 2. Active runtime-source committed slot
	data := t.TempDir()
	img := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("a", 64)
	writeTestRuntimeSlot(t, data, "s-00000000000000000002", img, "0.2.1-rc.3", "0.1.17", 2, false)
	info := readActiveRuntimeInfo(data)
	if !info.Configured || info.ParseError != nil || info.SourceKind != "runtime-source" || info.Slot != "s-00000000000000000002" || info.Pending {
		t.Fatalf("unexpected active info: %+v", info)
	}
	if info.Image != img || info.DSHVersion != "0.2.1-rc.3" || info.PluginVersion != "0.1.17" || info.ReleaseSequence != 2 {
		t.Fatalf("parsed metadata mismatch: %+v", info)
	}

	// 3. Invalid slot traversal in active.json
	badData := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badData, "config", "runtime-source"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badData, "config", "runtime-source", "active.json"), []byte(`{"Current":"../escape","Pending":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if badInfo := readActiveRuntimeInfo(badData); !badInfo.Configured || badInfo.ParseError == nil {
		t.Fatalf("expected parse error for traversal slot, got %+v", badInfo)
	}

	// 4. Fallback to base-materials
	baseData := t.TempDir()
	tplDir := filepath.Join(baseData, "config", "materials", "templates")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}
	dshRaw := encodeTestDshRuntimeProto(img, "0.2.0", "sha256:"+strings.Repeat("a", 64), 1)
	if err := os.WriteFile(filepath.Join(tplDir, "real-dsh.pb"), encodeTestInstanceSpecProto(dshRaw), 0644); err != nil {
		t.Fatal(err)
	}
	baseInfo := readActiveRuntimeInfo(baseData)
	if !baseInfo.Configured || baseInfo.SourceKind != "base-materials" || baseInfo.Image != img || baseInfo.DSHVersion != "0.2.0" {
		t.Fatalf("unexpected base-materials info: %+v", baseInfo)
	}
}

func TestRuntimeConsistencyCheck_DoctorAndStatus(t *testing.T) {
	oldInspect := inspectRuntimeImageMetadataFunc
	oldDirs := runtimeExpectedSourceDirsFunc
	defer func() {
		inspectRuntimeImageMetadataFunc = oldInspect
		runtimeExpectedSourceDirsFunc = oldDirs
	}()

	imgV1 := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("1", 64)
	imgV2 := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("2", 64)

	t.Run("skip when no runtime configured", func(t *testing.T) {
		data := t.TempDir()
		runtimeExpectedSourceDirsFunc = func(s state) []string { return nil }
		inspectRuntimeImageMetadataFunc = func(ctx context.Context, image string) (runtimeImageMetadata, bool) {
			return runtimeImageMetadata{}, false
		}
		var docBuf, stBuf bytes.Buffer
		rep := printDoctorRuntimeCheck(&docBuf, state{Data: data})
		printStatusRuntimeSummary(&stBuf, state{Data: data})
		if rep.Status != "SKIP" || !strings.Contains(docBuf.String(), "SKIP 运行时一致性") {
			t.Fatalf("expected SKIP, got status=%s out=%q", rep.Status, docBuf.String())
		}
		if stBuf.Len() != 0 {
			t.Fatalf("status should stay silent when no runtime configured, got %q", stBuf.String())
		}
	})

	t.Run("pass when pinned matches active and versions match", func(t *testing.T) {
		data := t.TempDir()
		src := t.TempDir()
		writeTestRuntimeSlot(t, data, "s-00000000000000000001", imgV1, "0.2.1-rc.3", "", 1, false)
		writeTestExpectedSource(t, src, "0.2.1-rc.3", "0.1.17")
		runtimeExpectedSourceDirsFunc = func(s state) []string { return []string{src} }
		inspectRuntimeImageMetadataFunc = func(ctx context.Context, image string) (runtimeImageMetadata, bool) {
			return runtimeImageMetadata{DSHVersion: "0.2.1-rc.3", PluginVersion: "0.1.17"}, true
		}
		s := state{
			Data:    data,
			Runtime: &installRuntime{Source: installRuntimeOptions{Bundle: "/srv/r1", Root: "/srv/root.pb", Image: imgV1}},
		}
		var docBuf, stBuf bytes.Buffer
		rep := printDoctorRuntimeCheck(&docBuf, s)
		printStatusRuntimeSummary(&stBuf, s)
		if rep.Status != "PASS" || !strings.Contains(docBuf.String(), "PASS 运行时一致性") {
			t.Fatalf("expected PASS, got %+v out=%q", rep, docBuf.String())
		}
		for _, want := range []string{"安装钉定运行时（install pinned）", "当前生效运行时（active runtime）", "运行时一致性：一致"} {
			if !strings.Contains(stBuf.String(), want) {
				t.Fatalf("status output missing %q:\n%s", want, stBuf.String())
			}
		}
	})

	t.Run("warn on #23 dsh and plugin version mismatch", func(t *testing.T) {
		data := t.TempDir()
		src := t.TempDir()
		writeTestRuntimeSlot(t, data, "s-00000000000000000001", imgV1, "0.2.0", "", 1, false)
		writeTestExpectedSource(t, src, "0.2.1-rc.3", "0.1.17")
		runtimeExpectedSourceDirsFunc = func(s state) []string { return []string{src} }
		inspectRuntimeImageMetadataFunc = func(ctx context.Context, image string) (runtimeImageMetadata, bool) {
			return runtimeImageMetadata{DSHVersion: "0.2.0", PluginVersion: "0.1.15"}, true
		}
		s := state{
			Data:    data,
			Runtime: &installRuntime{Source: installRuntimeOptions{Bundle: "/srv/r1", Root: "/srv/root.pb", Image: imgV1}},
		}
		var docBuf bytes.Buffer
		rep := printDoctorRuntimeCheck(&docBuf, s)
		if rep.Status != "WARN" || len(rep.Warnings) != 2 {
			t.Fatalf("expected 2 version warnings, got %+v", rep)
		}
		out := docBuf.String()
		for _, want := range []string{"WARN 运行时一致性", "0.2.0", "0.2.1-rc.3", "0.1.15", "0.1.17", "runtime-upgrade.sh"} {
			if !strings.Contains(out, want) {
				t.Fatalf("doctor output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("warn on #25 pinned vs active mismatch and pending", func(t *testing.T) {
		data := t.TempDir()
		writeTestRuntimeSlot(t, data, "s-00000000000000000002", imgV2, "0.2.1-rc.3", "0.1.17", 2, true)
		runtimeExpectedSourceDirsFunc = func(s state) []string { return nil }
		inspectRuntimeImageMetadataFunc = func(ctx context.Context, image string) (runtimeImageMetadata, bool) {
			return runtimeImageMetadata{}, false
		}
		s := state{
			Data:    data,
			Runtime: &installRuntime{Source: installRuntimeOptions{Bundle: "/srv/r1", Root: "/srv/root.pb", Image: imgV1}},
		}
		var docBuf, stBuf bytes.Buffer
		rep := printDoctorRuntimeCheck(&docBuf, s)
		printStatusRuntimeSummary(&stBuf, s)
		if rep.Status != "WARN" || len(rep.Warnings) < 2 {
			t.Fatalf("expected pending + mismatch warnings, got %+v", rep)
		}
		for _, out := range []string{docBuf.String(), stBuf.String()} {
			for _, want := range []string{imgV1, imgV2, "config/runtime-source/active.json", "site.json", "runtime sync-pin", "pending"} {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
		}
	})

	t.Run("pinned falls back to stack.env when state.Runtime is nil", func(t *testing.T) {
		data := t.TempDir()
		if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_RUNTIME_IMAGE="+imgV1+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		writeTestRuntimeSlot(t, data, "s-00000000000000000002", imgV2, "0.2.1-rc.3", "", 2, false)
		runtimeExpectedSourceDirsFunc = func(s state) []string { return nil }
		inspectRuntimeImageMetadataFunc = func(ctx context.Context, image string) (runtimeImageMetadata, bool) {
			return runtimeImageMetadata{}, false
		}
		rep := evaluateRuntimeConsistency(state{Data: data}, false)
		if rep.PinnedSource != "stack.env" || rep.PinnedImage != imgV1 || rep.Status != "WARN" {
			t.Fatalf("expected stack.env fallback WARN, got %+v", rep)
		}
	})
}

func TestUpdateStackEnvRuntimePreservesLinesAndPermissions(t *testing.T) {
	data := t.TempDir()
	envPath := filepath.Join(data, "stack.env")
	initial := strings.Join([]string{
		"# Auriga stack environment",
		"AURIGA_DATA_ROOT=" + data,
		"AURIGA_RUNTIME_BUNDLE=/old/bundle",
		"AURIGA_RUNTIME_IMAGE=127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("1", 64),
		"AURIGA_RUNTIME_IMAGE=duplicate-should-be-removed",
		"AURIGA_PLATFORM_SEED=false",
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(initial), 0640); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AURIGA_RUNTIME_BUNDLE": "/new/snap/bundle",
		"AURIGA_RUNTIME_ROOT":   "/new/snap/root.pb",
		"AURIGA_RUNTIME_IMAGE":  "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("2", 64),
	}
	origRaw, origPerm, err := updateStackEnvRuntime(data, want)
	if err != nil {
		t.Fatalf("updateStackEnvRuntime failed: %v", err)
	}
	if string(origRaw) != initial {
		t.Fatalf("unexpected origRaw: %q", string(origRaw))
	}
	if runtime.GOOS != "windows" && origPerm != 0640 {
		t.Fatalf("expected origPerm 0640, got %o", origPerm)
	}
	afterRaw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	after := string(afterRaw)
	for _, line := range []string{
		"# Auriga stack environment",
		"AURIGA_DATA_ROOT=" + data,
		"AURIGA_PLATFORM_SEED=false",
		"AURIGA_RUNTIME_BUNDLE=/new/snap/bundle",
		"AURIGA_RUNTIME_ROOT=/new/snap/root.pb",
		"AURIGA_RUNTIME_IMAGE=127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("2", 64),
	} {
		if !strings.Contains(after, line+"\n") {
			t.Fatalf("updated stack.env missing %q:\n%s", line, after)
		}
	}
	if strings.Count(after, "AURIGA_RUNTIME_IMAGE=") != 1 {
		t.Fatalf("duplicate AURIGA_RUNTIME_IMAGE not cleaned:\n%s", after)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(envPath)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("stack.env mode not preserved: %v %v", info, err)
		}
	}
}

func makeCustomRuntimeBundleFixture(t *testing.T, tag, img string) installRuntimeOptions {
	t.Helper()
	rootDir := t.TempDir()
	bundle := filepath.Join(rootDir, "bundle")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"runtime.pb", "manifest.json", "config.json", "signature.json"} {
		if err := os.WriteFile(filepath.Join(bundle, f), []byte(tag+"-"+f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	rootFile := filepath.Join(rootDir, "root.pb")
	if err := os.WriteFile(rootFile, []byte(tag+"-root"), 0644); err != nil {
		t.Fatal(err)
	}
	return installRuntimeOptions{Bundle: bundle, Root: rootFile, Image: img}
}

func TestSyncPinnedInstalledRuntimeLifecycleAndGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installRuntimeOptions.validate requires POSIX absolute paths")
	}

	oldVerify := verifyInstallRuntimeFunc
	defer func() { verifyInstallRuntimeFunc = oldVerify }()
	verifyInstallRuntimeFunc = func(pkg string, r installRuntimeOptions) error { return nil }

	imgV1 := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("1", 64)
	imgV2 := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("2", 64)
	reqV1 := makeCustomRuntimeBundleFixture(t, "v1", imgV1)
	reqV2 := makeCustomRuntimeBundleFixture(t, "v2", imgV2)

	tmp := t.TempDir()
	data := filepath.Join(tmp, "auriga-data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}

	// 先建立首装钉定快照 v1、state.json 与 stack.env。
	prepV1, err := prepareRuntimeSnapshot(tmp, reqV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(prepV1.directory) }()
	s := state{Data: data, Package: tmp, Source: sourceLocalDeploy}
	if err := pinInstallRuntime(&s, prepV1); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	v1Env := installRuntimeEnv(s)
	stackContent := strings.Join([]string{
		"AURIGA_DATA_ROOT=" + data,
		"AURIGA_RUNTIME_BUNDLE=" + v1Env["AURIGA_RUNTIME_BUNDLE"],
		"AURIGA_RUNTIME_ROOT=" + v1Env["AURIGA_RUNTIME_ROOT"],
		"AURIGA_RUNTIME_IMAGE=" + v1Env["AURIGA_RUNTIME_IMAGE"],
		"AURIGA_KEEP_ME=yes",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte(stackContent), 0640); err != nil {
		t.Fatal(err)
	}

	// 1. Unsigned rejected.
	unsigned := s
	unsigned.AllowUnsigned = true
	if _, _, err := syncPinnedInstalledRuntime(&unsigned, tmp, reqV2); err == nil || !strings.Contains(err.Error(), "未签名") {
		t.Fatalf("expected unsigned rejection, got %v", err)
	}

	// 2. Unpinned site rejected (suggests runtime attach).
	unpinned := s
	unpinned.Runtime = nil
	if _, _, err := syncPinnedInstalledRuntime(&unpinned, tmp, reqV2); err == nil || !strings.Contains(err.Error(), "runtime attach") {
		t.Fatalf("expected unpinned rejection, got %v", err)
	}

	// 3. Missing active.json rejected.
	if _, _, err := syncPinnedInstalledRuntime(&s, tmp, reqV2); err == nil || !strings.Contains(err.Error(), "active.json") {
		t.Fatalf("expected missing active.json rejection, got %v", err)
	}

	// 4. Pending active.json rejected.
	writeTestRuntimeSlot(t, data, "s-00000000000000000002", imgV2, "0.2.1-rc.3", "0.1.17", 2, true)
	if _, _, err := syncPinnedInstalledRuntime(&s, tmp, reqV2); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("expected pending rejection, got %v", err)
	}

	// 5. Committed active.json with mismatched image rejected.
	writeTestRuntimeSlot(t, data, "s-00000000000000000002", imgV2, "0.2.1-rc.3", "0.1.17", 2, false)
	if _, _, err := syncPinnedInstalledRuntime(&s, tmp, reqV1); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("expected image mismatch rejection, got %v", err)
	}

	// 6. Verifier failure preserves original snapshot, state, and stack.env.
	verifyInstallRuntimeFunc = func(pkg string, r installRuntimeOptions) error {
		return errors.New("signature verification failed")
	}
	if _, _, err := syncPinnedInstalledRuntime(&s, tmp, reqV2); err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("expected verifier error, got %v", err)
	}
	if s.Runtime.Source.Image != imgV1 {
		t.Fatalf("state mutated on verify failure: %+v", s.Runtime)
	}

	// 7. Successful sync-pin updates snapshot, state.json, stack.env, and passes resumeInstallRuntime.
	verifyInstallRuntimeFunc = func(pkg string, r installRuntimeOptions) error { return nil }
	oldImg, slot, err := syncPinnedInstalledRuntime(&s, tmp, reqV2)
	if err != nil {
		t.Fatalf("syncPinnedInstalledRuntime failed: %v", err)
	}
	if oldImg != imgV1 || slot != "s-00000000000000000002" {
		t.Fatalf("unexpected sync result: oldImg=%s slot=%s", oldImg, slot)
	}
	if s.Runtime.Source != reqV2 {
		t.Fatalf("state.Runtime not updated: %+v", s.Runtime)
	}
	persisted, err := readState(data)
	if err != nil || persisted.Runtime == nil || *persisted.Runtime != *s.Runtime {
		t.Fatalf("persisted state mismatch: %+v %v", persisted.Runtime, err)
	}
	if _, err := os.Lstat(runtimeSnapshot(data) + ".sync-pin.bak"); !os.IsNotExist(err) {
		t.Fatalf("backup dir should be cleaned up")
	}
	envRaw, err := os.ReadFile(filepath.Join(data, "stack.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envRaw), "AURIGA_KEEP_ME=yes\n") || !strings.Contains(string(envRaw), "AURIGA_RUNTIME_IMAGE="+imgV2+"\n") {
		t.Fatalf("stack.env unexpected content:\n%s", string(envRaw))
	}
	if info, err := os.Stat(filepath.Join(data, "stack.env")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("stack.env permission changed: %v %v", info, err)
	}

	// 8. Subsequent deploy resumeInstallRuntime with new reqV2 succeeds!
	prepCheck, err := prepareRuntimeSnapshot(tmp, reqV2)
	if err != nil {
		t.Fatalf("prepareRuntimeSnapshot v2 failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(prepCheck.directory) }()
	if err := resumeInstallRuntime(persisted, reqV2, prepCheck); err != nil {
		t.Fatalf("resumeInstallRuntime after sync-pin failed: %v", err)
	}
	if rep := evaluateRuntimeConsistency(persisted, false); rep.Status != "PASS" {
		t.Fatalf("expected PASS after sync-pin, got %+v", rep)
	}
}
