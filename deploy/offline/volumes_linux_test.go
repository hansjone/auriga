package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamedVolumeColdBackupAndRollback(t *testing.T) {
	image := os.Getenv("AURIGA_OFFLINE_VOLUME_TEST_IMAGE")
	if image == "" {
		t.Skip("set the approved local fixture image to run real Docker volume recovery")
	}
	name := "auriga-offline-volume-test-" + time.Now().UTC().Format("20060102150405.000000000")
	if _, err := capture("docker", "volume", "create", "--label", "auriga.offline.fixture=true", name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = capture("docker", "volume", "rm", name) }()
	base := t.TempDir()
	data := filepath.Join(base, "auriga-data")
	s := state{Data: data, Package: filepath.Join(data+".oneclick", "release"), Images: map[string]string{"POSTGRES": image}}
	scripts := filepath.Join(s.Package, "deploy-nas/scripts")
	if err := os.MkdirAll(scripts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data, 0755); err != nil {
		t.Fatal(err)
	}
	config := `{"volumes":{"state":{"name":"` + name + `"},"alias":{"name":"` + name + `"}}}`
	health := `[{"Service":"postgres","State":"running"},{"Service":"nats","State":"running"},{"Service":"registry","State":"running"},{"Service":"platform","State":"running"},{"Service":"edge","State":"running"},{"Service":"agent","State":"running"},{"Service":"bifrost","State":"running"},{"Service":"egress","State":"running"}]`
	for script, body := range map[string]string{"stop": "exit 0", "start": "exit 0", "oneclick-compose": `if [ "$1" = config ]; then printf '%s\n' '` + config + `'; else printf '%s\n' '` + health + `'; fi`} {
		if err := os.WriteFile(filepath.Join(scripts, script+".sh"), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	if err := command(nil, os.Stdout, nil, "docker", append(volumeHelper(s, name, false), "printf before >/volume/state; chown 10001:10001 /volume/state; chmod 0600 /volume/state")...); err != nil {
		t.Fatal(err)
	}
	archive, err := coldBackup(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := command(nil, os.Stdout, nil, "docker", append(volumeHelper(s, name, false), "printf after >/volume/state; touch /volume/new")...); err != nil {
		t.Fatal(err)
	}
	if err := rollback(pendingUpgrade{Backup: archive, State: s}); err != nil {
		t.Fatal(err)
	}
	raw, err := capture("docker", append(volumeHelper(s, name, true), "cat /volume/state; stat -c '%u:%g:%a' /volume/state; test ! -e /volume/new")...)
	if err != nil || !strings.Contains(raw, "before10001:10001:600") {
		t.Fatalf("%q %v", raw, err)
	}
	stage := t.TempDir()
	if err := snapshotVolumes(s, []volumeRecord{{Name: name, Driver: "local", Labels: map[string]string{"auriga.offline.fixture": "true"}}}, stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "volumes/0.tar"), []byte("tamper"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreVolumes(s, stage); err == nil {
		t.Fatal("accepted corrupt volume archive")
	}
	var records []volumeRecord
	if err := readJSON(filepath.Join(stage, "volumes/manifest.json"), &records); err != nil {
		t.Fatal(err)
	}
	records[0].Name = "../unsafe"
	rawJSON, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "volumes/manifest.json"), rawJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreVolumes(s, stage); err == nil {
		t.Fatal("accepted unsafe volume name")
	}
}
