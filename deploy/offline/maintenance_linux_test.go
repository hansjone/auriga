package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewSiteSignedUpgradeAndHealthyArchiveWarning(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root numeric ownership backup")
	}
	base := t.TempDir()
	data := filepath.Join(base, "auriga-data")
	meta := data + ".oneclick"
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	s := state{Data: data, Package: filepath.Join(meta, "release"), Access: filepath.Join(meta, "access.json")}
	healthy := `[{"Service":"postgres","State":"running"},{"Service":"nats","State":"running"},{"Service":"registry","State":"running"},{"Service":"platform","State":"running"},{"Service":"edge","State":"running"},{"Service":"agent","State":"running"},{"Service":"bifrost","State":"running"},{"Service":"egress","State":"running"}]`
	makeRelease := func(root, version string, archiveFailure bool) {
		t.Helper()
		for _, dir := range []string{"images", "deploy-nas/scripts"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
				t.Fatal(err)
			}
		}
		compose := "if [ \"$1\" = config ]; then echo '{\"volumes\":{}}'; exit 0; fi\n"
		if archiveFailure {
			compose += "if [ \"$1\" = ps ]; then mv \"$NAS_ROOT/../../release.previous\" \"$NAS_ROOT/../../release.retained\"; fi\n"
		}
		compose += "printf '%s\\n' '" + healthy + "'\n"
		id := "sha256:" + strings.Repeat("a", 64)
		load := ""
		for _, name := range []string{"POSTGRES", "NATS", "REGISTRY", "PLATFORM", "EDGE"} {
			load += "echo AURIGA_IMAGE_" + name + "=" + id + "\n"
		}
		for name, body := range map[string]string{"stop.sh": "exit 0", "start.sh": "exit 0", "render.sh": "exit 0", "preflight.sh": "exit 0", "oneclick-compose.sh": compose, "load-images.sh": load} {
			if err := os.WriteFile(filepath.Join(root, "deploy-nas/scripts", name), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		for name, body := range map[string]string{"images/fixture": "layer", "NAS-SHA256SUMS": "fixture", "versions.txt": version} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := jsonFile(filepath.Join(root, "offline-release.json"), release{Schema: 1, Version: version}, 0644); err != nil {
			t.Fatal(err)
		}
	}
	makeRelease(s.Package, "1.0", false)
	issuer, public := testSigningKeys(t)
	signTestRoot(t, s.Package, issuer)
	if err := pinInitialTrust(&s, public); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_PLATFORM_SEED=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	keydir := filepath.Join(base, "developer")
	if err := generateSiteKey(keydir); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(t.TempDir(), "next")
	makeRelease(next, "1.0-site.1", true)
	signTestRoot(t, next, filepath.Join(keydir, "site-private.pem"))
	archive := filepath.Join(t.TempDir(), "site.tar")
	if err := pack(next, archive); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(data, archive); err == nil {
		t.Fatal("upgrade accepted unregistered site key")
	}
	fp, err := publicFingerprint(filepath.Join(keydir, "site-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCommand([]string{"add-site-key", filepath.Join(keydir, "site-public.pem"), "--confirm-fingerprint", fp, "--data", data}); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(data, archive); err != nil {
		t.Fatal("site-signed upgrade failed or archive warning caused rollback", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Package, "offline-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r release
	if err := json.Unmarshal(raw, &r); err != nil || r.Version != "1.0-site.1" {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(s.Package + ".retained"); err != nil {
		t.Fatal("old release not retained after simulated archive failure", err)
	}
	backups, err := filepath.Glob(filepath.Join(base, "auriga-backups/backup-*.tar"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	if _, err := os.Stat(data + ".upgrade.json"); !os.IsNotExist(err) {
		t.Fatal("completed upgrade left rollback journal")
	}
	// An exception for the installed old release must also remain visible after
	// a correctly signed replacement is activated.
	if err := os.Remove(filepath.Join(s.Package, "SHA256SUMS.sig")); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second")
	makeRelease(second, "1.0-site.2", false)
	signTestRoot(t, second, filepath.Join(keydir, "site-private.pem"))
	secondArchive := filepath.Join(t.TempDir(), "second.tar")
	if err := pack(second, secondArchive); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(data, secondArchive); err == nil {
		t.Fatal("accepted unsigned installed release without explicit exception")
	}
	if err := upgradeAllowed(data, secondArchive, true); err != nil {
		t.Fatal(err)
	}
	saved, err := readState(data)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.AllowUnsigned || trustHealth(saved) == nil {
		t.Fatal("old-release exception lost its persistent doctor alarm")
	}
}

func TestReviewResourceProvisionRetryAndPartialFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("service UID ownership")
	}
	data := t.TempDir()
	s := state{Data: data}
	if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_PLATFORM_SEED=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := provisionResource(s); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(data, "resource-keys/platform/release.pk8")
	before, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := provisionResource(s); err != nil {
		t.Fatal("complete material retry should recover interrupted state save", err)
	}
	after, err := os.ReadFile(key)
	if err != nil || string(after) != string(before) {
		t.Fatal("retry replaced key", err)
	}
	raw, err := os.ReadFile(filepath.Join(data, "stack.env"))
	if err != nil || strings.Count(string(raw), "AURIGA_RESOURCE_RELEASE_KID=") != 1 {
		t.Fatal(string(raw), err)
	}
	if err := os.Remove(filepath.Join(data, "resource-keys/offline/downgrade.pk8")); err != nil {
		t.Fatal(err)
	}
	if err := provisionResource(s); err == nil || !strings.Contains(err.Error(), "残缺") {
		t.Fatal("partial materials not explicitly refused", err)
	}
	after, err = os.ReadFile(key)
	if err != nil || string(after) != string(before) {
		t.Fatal("partial retry replaced original key", err)
	}
}

func TestReviewSiteKeyRotationPreservesIssuerAndActiveSigner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("server trust management is root-only")
	}
	data := filepath.Join(t.TempDir(), "auriga-data")
	if err := os.Mkdir(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	issuer, public := testSigningKeys(t)
	s := state{Data: data, Package: filepath.Join(data+".oneclick", "release")}
	if err := os.Mkdir(s.Package, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Package, "payload"), []byte("site"), 0644); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, s.Package, issuer)
	if err := pinInitialTrust(&s, public); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	old, oldpub := testSigningKeys(t)
	next, nextpub := testSigningKeys(t)
	oldFP, err := publicFingerprint(oldpub)
	if err != nil {
		t.Fatal(err)
	}
	nextFP, err := publicFingerprint(nextpub)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range [][2]string{{oldpub, oldFP}, {nextpub, nextFP}} {
		if err := trustCommand([]string{"add-site-key", entry[0], "--confirm-fingerprint", entry[1], "--data", data}); err != nil {
			t.Fatal(err)
		}
	}
	signTestRoot(t, s.Package, old)
	if err := trustCommand([]string{"remove-site-key", oldFP, "--data", data}); err == nil {
		t.Fatal("removed active site signer")
	}
	signTestRoot(t, s.Package, next)
	if err := trustCommand([]string{"remove-site-key", oldFP, "--data", data}); err != nil {
		t.Fatal("old site signer could not retire", err)
	}
	if err := trustCommand([]string{"add-site-key", oldpub, "--confirm-fingerprint", oldFP, "--data", data}); err != nil {
		t.Fatal("orphaned public file prevented explicit re-registration", err)
	}
	issuerFP, err := publicFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCommand([]string{"remove-site-key", issuerFP, "--data", data}); err == nil {
		t.Fatal("removed pinned issuer")
	}
}

func TestColdBackupAndRollbackRestoreDatabaseAndKeys(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("numeric owner restore requires root")
	}
	base := t.TempDir()
	data := filepath.Join(base, "auriga-data")
	meta := data + ".oneclick"
	s := state{Data: data, Package: filepath.Join(meta, "release"), Access: filepath.Join(meta, "access.json")}
	scripts := filepath.Join(s.Package, "deploy-nas/scripts")
	if err := os.MkdirAll(scripts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database", "secret"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"stop.sh": "exit 0", "start.sh": "exit 0", "oneclick-compose.sh": `if [ "$1" = config ]; then echo '{"volumes":{}}'; exit 0; fi
printf '%s\n' '[{"Service":"postgres","State":"running"},{"Service":"nats","State":"running"},{"Service":"registry","State":"running"},{"Service":"platform","State":"running"},{"Service":"edge","State":"running"},{"Service":"agent","State":"running"},{"Service":"bifrost","State":"running"},{"Service":"egress","State":"running"}]'`} {
		if err := os.WriteFile(filepath.Join(scripts, name), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	archive, err := coldBackup(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "database"), []byte("migrated"), 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the old data directory was quarantined and
	// before either restored root was promoted. The persisted plan must resume.
	plan, err := prepareRestore(pendingUpgrade{Backup: archive, State: s})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(data, data+plan.Suffix); err != nil {
		t.Fatal(err)
	}
	if err := rollback(pendingUpgrade{Backup: archive, State: s}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database", "secret"} {
		raw, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || string(raw) != "before" {
			t.Fatalf("%s %q %v", name, raw, err)
		}
	}
	if err := os.WriteFile(archive+".sha256", []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rollback(pendingUpgrade{Backup: archive, State: s}); err == nil {
		t.Fatal("accepted corrupt backup")
	}
}

func TestResourceKeyPermissionsAndPurposes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "keys")
	if err := resourceKeys(root); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"platform": 0700, "offline": 0700, "platform/release.pk8": 0600, "platform/revocation.pk8": 0600, "offline/downgrade.pk8": 0600} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s %v %v", name, info, err)
		}
	}
}

func TestDoctorDockerPrerequisites(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	if err := dockerReady(); err == nil {
		t.Fatal("accepted missing Docker")
	}
	for _, test := range []struct {
		version string
		want    bool
	}{{"2.28.9", false}, {"2.29.0", true}, {"v2.40.0", true}, {"3.0.0", true}, {"garbage", false}} {
		body := "#!/bin/sh\nif [ \"$1\" = context ]; then echo unix:///var/run/docker.sock; elif [ \"$1\" = info ]; then echo 29.1.3; else echo '" + test.version + "'; fi\n"
		if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
		if err := dockerReady(); (err == nil) != test.want {
			t.Fatalf("version=%s err=%v", test.version, err)
		}
	}
	t.Setenv("DOCKER_HOST", "tcp://192.0.2.1:2376")
	if err := dockerReady(); err == nil {
		t.Fatal("accepted remote Docker endpoint")
	}
}
