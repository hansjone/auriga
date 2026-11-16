package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewIndependentReleaseIdentityAndReplacement(t *testing.T) {
	root := t.TempDir()
	key, public := testSigningKeys(t)
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("approved"), 0644); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, root, key)
	fp, err := publicFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCommand([]string{root}); err == nil || !strings.Contains(err.Error(), "仅完整性，未核验发行身份") {
		t.Fatal(err)
	}
	if err := verifyCommand([]string{root, "--trust-fingerprint", fp}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyCommand([]string{root}); err == nil || strings.Contains(err.Error(), "仅完整性") {
		t.Fatal("claimed integrity before checking payload", err)
	}
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("approved"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verifyRootTrust(root, trustOptions{Fingerprint: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("accepted wrong fingerprint")
	}
	if _, _, err := verifyRootTrust(root, trustOptions{Fingerprint: strings.Repeat("0", 64), AllowUnsigned: true}); err == nil {
		t.Fatal("unsigned flag bypassed wrong fingerprint")
	}
	archive := filepath.Join(t.TempDir(), "release.tar")
	if err := pack(root, archive); err != nil {
		t.Fatal(err)
	}
	if err := unpackCommand([]string{archive, "--out", filepath.Join(t.TempDir(), "refused")}); err == nil {
		t.Fatal("unpack trusted embedded signer without identity")
	}
	if err := unpackCommand([]string{archive, "--out", filepath.Join(t.TempDir(), "approved"), "--trust-fingerprint", fp}); err != nil {
		t.Fatal(err)
	}
	meta := t.TempDir()
	s := state{Data: filepath.Join(meta, "auriga-data"), Package: root}
	if err := os.Mkdir(s.Data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	if err := pinInitialTrust(&s, public); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(s.Data+".oneclick", "release")
	if err := copyTree(root, installed); err != nil {
		t.Fatal(err)
	}
	s.Package = installed
	attacker, _ := testSigningKeys(t)
	signTestRoot(t, root, attacker)
	if _, _, err := verifyRootTrust(root, trustOptions{Fingerprint: fp}); err == nil {
		t.Fatal("self-signed replacement accepted")
	}
	if _, err := verifyStatePackage(s, root, "", true); err == nil {
		t.Fatal("allow-unsigned bypassed unknown signer")
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(t.TempDir(), "replacement.tar")
	if err := pack(root, replacement); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(s.Data, replacement); err == nil || !strings.Contains(err.Error(), "state固定") {
		t.Fatal("upgrade accepted embedded replacement", err)
	}
	if err := install(installOptions{Data: s.Data, Package: replacement, PublicKey: filepath.Join(root, "release-public.pem")}); err == nil || !strings.Contains(err.Error(), "state固定") {
		t.Fatal("install retry replaced pinned issuer", err)
	}
}

func TestReviewUnsignedExceptionWarnsAndRemainsInState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("unsigned"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "unsigned.tar")
	if err := pack(root, archive); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "warnings")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = stderr
	err = unpackCommand([]string{archive, "--out", filepath.Join(t.TempDir(), "out"), "--allow-unsigned"})
	os.Stderr = old
	if closeErr := stderr.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stderr.Name())
	if err != nil || !bytes.Contains(raw, []byte("警告：--allow-unsigned")) {
		t.Fatal(string(raw), err)
	}
	s := state{Data: filepath.Join(t.TempDir(), "auriga-data"), AllowUnsigned: true}
	if err := os.Mkdir(s.Data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	saved, err := readState(s.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHealth(saved); err == nil || !strings.Contains(err.Error(), "allow-unsigned") {
		t.Fatal("doctor lost unsigned alarm", err)
	}
}

func TestReviewSiteKeyRegistrationAndSigning(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "auriga-data")
	if err := os.Mkdir(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	_, issuer := testSigningKeys(t)
	s := state{Data: data}
	if err := pinInitialTrust(&s, issuer); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "developer-key")
	if err := keygenCommand([]string{"site", "--out", dir}); err != nil {
		t.Fatal(err)
	}
	if err := generateSiteKey(dir); err == nil {
		t.Fatal("overwrote site private key")
	}
	public := filepath.Join(dir, "site-public.pem")
	fp, err := publicFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("site build"), 0644); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, root, filepath.Join(dir, "site-private.pem"))
	if _, err := verifyStatePackage(s, root, "", false); err == nil {
		t.Fatal("unregistered site signer accepted")
	}
	if err := registerSiteKey(&s, public, strings.Repeat("0", 64)); err == nil {
		t.Fatal("registered wrong fingerprint")
	}
	if err := registerSiteKey(&s, public, fp); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	s, err = readState(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyStatePackage(s, root, "", false); err != nil {
		t.Fatal("registered site signer refused", err)
	}
	if err := bundle([]string{"--build", "--offline"}); err == nil || !strings.Contains(err.Error(), "--sign-key") {
		t.Fatal(err)
	}
	if _, err := parseSiteBuild([]string{"--build", "--offline"}); err == nil {
		t.Fatal("Windows accepted missing signer")
	}
}

func reviewArchiveKit(t *testing.T) (string, string, string) {
	t.Helper()
	source := t.TempDir()
	single := fixtureSingleKit(t, "1.0", "a")
	kit, err := readDevkit(single)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	if err := writeSourceManifest(source, provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}, strings.Repeat("b", 40), kit.Release); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, source, key)
	locks, err := dependencyLocks(source, kit.Release)
	if err != nil {
		t.Fatal(err)
	}
	kit.Locks = &locks
	if err := jsonFile(filepath.Join(single, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, single, key)
	full := filepath.Join(t.TempDir(), "full")
	if _, err := composeDevkit("", single, "1.0", strings.Repeat("a", 40), full, key, 1<<30, true); err != nil {
		t.Fatal(err)
	}
	return source, full, key
}

func TestReviewBindingCannotRedirectToForgedDevkit(t *testing.T) {
	source, full, _ := reviewArchiveKit(t)
	if err := verifyCumulative(full, source, "", ""); err != nil {
		t.Fatal(err)
	}
	attacker, _ := testSigningKeys(t)
	signTestRoot(t, full, attacker)
	if _, _, err := locateDevkit(source); err == nil {
		t.Fatal("binding redirected to self-signed replacement")
	}
	if err := devkitVerify([]string{full, "--source", source}); err == nil {
		t.Fatal("forged catalog verified")
	}
}

func TestReviewDevkitVerifyDoesNotNeedBuildResources(t *testing.T) {
	source, full, _ := reviewArchiveKit(t)
	t.Chdir(source)
	t.Setenv("PATH", t.TempDir())
	if err := dispatch([]string{"devkit", "verify", full}); err != nil {
		t.Fatal("dependency verification required Docker/resources", err)
	}
}

func TestReviewResourcePartialDirectoryIsExplicitAndPreserved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "partial")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateResourceMaterial(root); err == nil || !strings.Contains(err.Error(), "残缺") {
		t.Fatal(err)
	}
	if err := resourceKeys(root); err == nil {
		t.Fatal("overwrote incomplete materials")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "keep" {
		t.Fatal(err)
	}
}

func signTestRoot(t *testing.T, root, key string) {
	t.Helper()
	if err := signingPublic(key, filepath.Join(root, "release-public.pem")); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
}

func signedTestSourceManifest(t *testing.T, root string, info provenance, tree string, r release) error {
	if err := writeSourceManifest(root, info, tree, r); err != nil {
		return err
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, root, key)
	return nil
}

func TestReviewSignatureStripping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(root, ""); err == nil {
		t.Error("accepted unsigned package")
	}
	archive := filepath.Join(t.TempDir(), "unsigned.tar")
	if err := pack(root, archive); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(archive, filepath.Join(t.TempDir(), "out"), ""); err == nil {
		t.Error("unpack accepted stripped signature and key")
	}
}

func TestReviewSourceAndKitStripping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSourceManifest(root, provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}, strings.Repeat("b", 40), release{Version: "1.0", Build: &buildSpec{}}); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceManifest(root); err == nil {
		t.Error("accepted unsigned source baseline")
	}
	single := fixtureSingleKit(t, "1.0", "a")
	if err := os.Remove(filepath.Join(single, "SHA256SUMS.sig")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := locateDevkit(filepath.Join(single, "source")); err == nil {
		t.Error("accepted unsigned single devkit")
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, single, key)
	full := filepath.Join(t.TempDir(), "full")
	if _, err := composeDevkit("", single, "1.0", strings.Repeat("a", 40), full, key, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SHA256SUMS.sig", "release-public.pem"} {
		if err := os.Remove(filepath.Join(full, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readCatalog(full, ""); err == nil {
		t.Error("accepted stripped catalog")
	}
	source := t.TempDir()
	if err := jsonFile(filepath.Join(source, ".auriga-devkit.json"), devkitBinding{Root: full}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := locateDevkit(source); err == nil {
		t.Error("binding redirected to unsigned devkit")
	}
}

func TestReviewWindowsScriptCopiesCRLF(t *testing.T) {
	for _, name := range []string{"entry.ps1", "entry.cmd", "entry.bat"} {
		root := t.TempDir()
		src := filepath.Join(root, "in")
		out := filepath.Join(root, "out")
		if err := os.WriteFile(src, []byte("line1\r\nline2\r\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := copySourceFile(src, out, name, 0644); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "line1\r\nline2\r\n" {
			t.Errorf("%s lost CRLF: %q", name, raw)
		}
	}
}

func TestReviewChangesMaterializeWindowsScriptsCRLF(t *testing.T) {
	root := t.TempDir()
	r := release{Version: "1.0", Build: &buildSpec{}}
	for _, name := range []string{"a.cmd", "b.txt", "c.ps1", "d.bat"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("before\r\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := signedTestSourceManifest(t, root, provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}, strings.Repeat("b", 40), r); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "clean")
	if err := copyTree(root, target); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.cmd", "b.txt", "c.ps1", "d.bat"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("after\r\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	patch := filepath.Join(t.TempDir(), "patch.tar")
	if err := exportChanges(root, patch); err != nil {
		t.Fatal(err)
	}
	if err := applyChanges(target, patch); err != nil {
		t.Fatal(err)
	}
	if err := applyChanges(target, patch); err != nil {
		t.Fatal("retry lost canonical identity", err)
	}
	for _, name := range []string{"a.cmd", "b.txt", "c.ps1", "d.bat"} {
		raw, err := os.ReadFile(filepath.Join(target, name))
		if err != nil {
			t.Fatal(err)
		}
		want := "after\r\n"
		if name == "b.txt" {
			want = "after\n"
		}
		if string(raw) != want {
			t.Fatalf("%s %q != %q", name, raw, want)
		}
	}
}
