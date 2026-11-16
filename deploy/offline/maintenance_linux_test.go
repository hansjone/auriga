package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
