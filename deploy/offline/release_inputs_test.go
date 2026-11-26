package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeReleaseFixture(t *testing.T, file string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func fixtureDSHReleaseInput(t *testing.T, source string) string {
	t.Helper()
	const version = "0.2.1"
	commit := strings.Repeat("a", 40)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	archive := filepath.Join(t.TempDir(), "upstream.tar.gz")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	root := "deepseek-harness-" + commit
	for _, row := range []struct{ name, body string }{
		{"LICENSE", "MIT License\nfixture\n"},
		{"apps/cli/package.json", `{"name":"@deepseek-ai/dsh","version":"0.2.1"}`},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + row.name, Mode: 0644, Size: int64(len(row.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(row.body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, closeFile := range []func() error{tw.Close, gz.Close, out.Close} {
		if err := closeFile(); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := digest(archive)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFixture(t, filepath.Join(source, "clients/installer/upstream.json"), fmt.Appendf(nil, `{"dsh":%q,"dshIntegrity":%q}`, version, integrity))
	writeReleaseFixture(t, filepath.Join(source, "clients/installer/locks/dsh-"+version+".package-lock.json"),
		fmt.Appendf(nil, `{"packages":{"node_modules/@deepseek-ai/dsh":{"version":%q,"integrity":%q,"resolved":%q}}}`, version, integrity,
			"https://registry.npmjs.org/@deepseek-ai/dsh/-/dsh-"+version+".tgz"))
	if err := os.MkdirAll(filepath.Join(source, "deploy/offline"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := jsonFile(filepath.Join(source, dshSourcePinPath), dshArchivePin{version, commit, hash}, 0644); err != nil {
		t.Fatal(err)
	}
	return archive
}

func fixtureWindowsReleaseInput(t *testing.T, source, kitParent, key string) (string, string, string) {
	t.Helper()
	writeReleaseFixture(t, filepath.Join(source, "scripts/client-release/build-node.json"), []byte(`{"version":"22.19.0"}`))
	for _, name := range []string{"package.json", "pnpm-workspace.yaml", ".npmrc", "web/pnpm-lock.yaml",
		"clients/dsh-plugin/package.json", "clients/dsh-plugin/package-lock.json", "clients/installer/locks/python-win-x64.json"} {
		writeReleaseFixture(t, filepath.Join(source, name), []byte("{}\n"))
	}
	locks, err := windowsReleaseLocks(t.Context(), source, "0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	rawLocks, err := json.Marshal(locks)
	if err != nil {
		t.Fatal(err)
	}
	lockKey := fmt.Sprintf(`{"buildNode":"22.19.0","locks":%s,"platform":"win-x64","schema":1}`, rawLocks)
	lockSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(lockKey)))
	stem := windowsClientPrefix + lockSHA
	kit := filepath.Join(kitParent, stem)
	node := []byte("fixture Windows Node")
	nodeSHA := fmt.Sprintf("%x", sha256.Sum256(node))
	writeReleaseFixture(t, filepath.Join(kit, windowsBuildNodePath), node)
	manifest := fmt.Appendf(nil, `{"schema":1,"platform":"win-x64","buildNode":"22.19.0","lockSha256":%q,"locks":%s,"files":[{"path":%q,"bytes":%d,"sha256":%q}],"bytes":%d}`,
		lockSHA, rawLocks, windowsBuildNodePath, len(node), nodeSHA, len(node))
	writeReleaseFixture(t, filepath.Join(kit, "windows-client.json"), manifest)
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(manifest))
	metaFile := filepath.Join(kit, "client-inputs-release.json")
	if err := jsonFile(metaFile, windowsReleaseInput{1, "1.0", "win-x64", lockSHA, manifestSHA, nodeSHA}, 0644); err != nil {
		t.Fatal(err)
	}
	metaSHA, err := digest(metaFile)
	if err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(kit, "SHA256SUMS")
	writeReleaseFixture(t, sums, fmt.Appendf(nil, "%s  client-inputs-release.json\n%s  windows-client.json\n", metaSHA, manifestSHA))
	if err := signFile(sums, key); err != nil {
		t.Fatal(err)
	}
	if err := signingPublic(key, filepath.Join(kit, "release-public.pem")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), stem+".tar")
	if err := pack(kitParent, archive); err != nil {
		t.Fatal(err)
	}
	hash, err := digest(archive)
	if err != nil {
		t.Fatal(err)
	}
	return archive, hash, nodeSHA
}

func TestWindowsReleaseRejectsWrongIssuerAndChangedLock(t *testing.T) {
	source := t.TempDir()
	fixtureDSHReleaseInput(t, source)
	key, public := testSigningKeys(t)
	archive, hash, nodeSHA := fixtureWindowsReleaseInput(t, source, t.TempDir(), key)
	scratch := t.TempDir()
	if info, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, scratch); err != nil || info.BuildNodeSHA != nodeSHA {
		t.Fatal(info, err)
	}
	_, wrong := testSigningKeys(t)
	if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, wrong, scratch); err == nil || !strings.Contains(err.Error(), "签名不匹配") {
		t.Fatal("accepted another issuer", err)
	}
	if _, err := verifyWindowsRelease(t.Context(), archive, strings.Repeat("0", 64), source, public, scratch); err == nil || !strings.Contains(err.Error(), "转运") {
		t.Fatal("accepted changed transferred bytes", err)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	trailing := filepath.Join(t.TempDir(), filepath.Base(archive))
	modified := append(raw, []byte("unexpected tar data")...)
	writeReleaseFixture(t, trailing, modified)
	modifiedSHA := fmt.Sprintf("%x", sha256.Sum256(modified))
	if _, err := verifyWindowsRelease(t.Context(), trailing, modifiedSHA, source, public, scratch); err == nil || !strings.Contains(err.Error(), "结束标记") {
		t.Fatal("accepted bytes appended after Windows tar entries", err)
	}
	writeReleaseFixture(t, filepath.Join(source, "web/pnpm-lock.yaml"), []byte("changed\n"))
	if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, scratch); err == nil || !strings.Contains(err.Error(), "依赖锁") {
		t.Fatal("accepted a kit from a different lock", err)
	}
}

func TestWindowsReleaseLocksMatchNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for cross-language check")
	}
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := readDSHInstallerPin(source)
	if err != nil {
		t.Fatal(err)
	}
	locks, err := windowsReleaseLocks(t.Context(), source, pin.Version)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--input-type=module", "-e", `import {BUILD_NODE, lockIdentity} from './scripts/client-release/inputs.mjs'; process.stdout.write(JSON.stringify({...lockIdentity(), buildNode: BUILD_NODE}));`)
	command.Dir = source
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Node lock identity: %v %s", err, raw)
	}
	var m windowsClientManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Schema, m.Platform = 1, "win-x64"
	release := windowsReleaseInput{Platform: "win-x64", LockSHA: m.LockSHA}
	if len(m.Locks) != len(locks) {
		t.Fatal("Node and Go lock inventory length differs")
	}
	if err := verifyWindowsClientManifest(t.Context(), source, release, m, nil, 0); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsReleaseRejectsChangedBuildNode(t *testing.T) {
	source := t.TempDir()
	fixtureDSHReleaseInput(t, source)
	key, public := testSigningKeys(t)
	archive, hash, _ := fixtureWindowsReleaseInput(t, source, t.TempDir(), key)
	scratch := t.TempDir()
	if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, scratch); err != nil {
		t.Fatal(err)
	}
	writeReleaseFixture(t, filepath.Join(source, "scripts/client-release/build-node.json"), []byte(`{"version":"22.20.0"}`))
	if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, scratch); err == nil || !strings.Contains(err.Error(), "引导 Node 版本") {
		t.Fatal("accepted an old signed kit after changing only the build Node pin", err)
	}
}

func TestWindowsReleaseBuildNodePinStrictPolicy(t *testing.T) {
	source := t.TempDir()
	fixtureDSHReleaseInput(t, source)
	key, public := testSigningKeys(t)
	archive, hash, _ := fixtureWindowsReleaseInput(t, source, t.TempDir(), key)
	pinPath := filepath.Join(source, "scripts/client-release/build-node.json")

	winSHA := strings.Repeat("a", 64)
	linuxSHA := strings.Repeat("b", 64)
	linuxSHAB := strings.Repeat("c", 64)

	t.Run("windows-only legacy pin accepted", func(t *testing.T) {
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","winX64ZipSha256":"`+winSHA+`","winX64ZipSha256Sources":["https://nodejs.org/dist/v22.19.0/SHASUMS256.txt"],"winX64ZipSha256Note":"win"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err != nil {
			t.Fatalf("rejected legacy Windows-only build-node pin: %v", err)
		}
	})

	t.Run("multi-platform pin accepted and linux sha isolated from windows verification", func(t *testing.T) {
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","winX64ZipSha256":"`+winSHA+`","winX64ZipSha256Sources":["https://nodejs.org/dist/v22.19.0/SHASUMS256.txt"],"winX64ZipSha256Note":"win","linuxX64TarSha256":"`+linuxSHA+`","linuxX64TarSha256Sources":["https://nodejs.org/dist/v22.19.0/SHASUMS256.txt"],"linuxX64TarSha256Note":"linux"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err != nil {
			t.Fatalf("rejected multi-platform build-node pin: %v", err)
		}
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","winX64ZipSha256":"`+winSHA+`","linuxX64TarSha256":"`+linuxSHAB+`"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err != nil {
			t.Fatalf("changing valid linuxX64TarSha256 unexpectedly broke Windows verification: %v", err)
		}
	})

	t.Run("unknown field still rejected by strict reader", func(t *testing.T) {
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","linuxX64TarSha256Typo":"`+linuxSHA+`"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("accepted unknown field in build-node.json: %v", err)
		}
	})

	t.Run("trailing JSON still rejected", func(t *testing.T) {
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0"}{"extra":true}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err == nil || !strings.Contains(err.Error(), "多余数据") {
			t.Fatalf("accepted trailing JSON in build-node.json: %v", err)
		}
	})

	t.Run("invalid windows or linux sha256 rejected", func(t *testing.T) {
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","winX64ZipSha256":"not-a-sha"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err == nil || !strings.Contains(err.Error(), "zip 摘要无效") {
			t.Fatalf("accepted invalid winX64ZipSha256: %v", err)
		}
		writeReleaseFixture(t, pinPath, []byte(`{"version":"22.19.0","winX64ZipSha256":"`+winSHA+`","linuxX64TarSha256":"NOTHEX"}`))
		if _, err := verifyWindowsRelease(t.Context(), archive, hash, source, public, t.TempDir()); err == nil || !strings.Contains(err.Error(), "Linux 构建 Node tar 摘要无效") {
			t.Fatalf("accepted invalid linuxX64TarSha256: %v", err)
		}
	})
}

func TestDSHReleaseSourcePinnedArchive(t *testing.T) {
	source, input := t.TempDir(), os.Getenv("AURIGA_TEST_DSH_SOURCE")
	if input == "" {
		input = fixtureDSHReleaseInput(t, source)
	} else {
		source = "../.."
	}
	archive, files, err := stageDSHSource(t.Context(), source, input, t.TempDir())
	if err != nil || len(files) != 2 {
		t.Fatal(archive, files, err)
	}
	original, err := digest(input)
	if err != nil {
		t.Fatal(err)
	}
	if copySHA, err := digest(archive); err != nil || copySHA != original {
		t.Fatal("changed upstream bytes", copySHA, err)
	}
	wrong := filepath.Join(t.TempDir(), "wrong.tar.gz")
	writeReleaseFixture(t, wrong, []byte("changed upstream bytes"))
	if _, _, err := stageDSHSource(t.Context(), source, wrong, t.TempDir()); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatal("accepted pinned archive with different bytes", err)
	}
}
