package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func sharedRenderedNATSFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../nats/nats.conf.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`__NATS_[A-Z_]+__`).ReplaceAll(raw, []byte("synthetic-password-not-a-real-credential"))
}

func TestSharedNATSProjectionPreservesPasswordUsersAndAddsOnlyTheDedicatedAuthority(t *testing.T) {
	files := sharedFixture(t)
	original := sharedRenderedNATSFixture(t)
	public := string(files["public/replay.nkey"])
	projected, err := sharedNATSProjection(original, public)
	if err != nil {
		t.Fatal(err)
	}
	user, err := sharedReplayNATSUser(public)
	if err != nil {
		t.Fatal(err)
	}
	withoutUser := bytes.Replace(projected, []byte(user), []byte(sharedNATSUserMarker), 1)
	var permissions strings.Builder
	for _, subject := range sharedReplayPlatformPermissions() {
		permissions.WriteString("\"" + subject + "\",\n            ")
	}
	restored := bytes.Replace(withoutUser, []byte(permissions.String()), []byte(sharedNATSPlatformMarker), 1)
	if !bytes.Equal(restored, original) {
		t.Fatal("ordinary NATS users or permissions changed")
	}
	if bytes.Contains(projected, files["edge/replay.seed"]) || bytes.Count(projected, []byte(public)) != 1 {
		t.Fatal("private seed exposed or public user duplicated")
	}
	for _, input := range [][]byte{nil, bytes.Replace(original, []byte(sharedNATSUserMarker), nil, 1), append(append([]byte{}, original...), []byte(sharedNATSPlatformMarker)...), projected} {
		if _, err := sharedNATSProjection(input, public); err == nil {
			t.Fatal("missing/duplicate/already projected authority accepted")
		}
	}
	if output := os.Getenv("AURIGA_SHARED_NATS_FIXTURE"); output != "" {
		installer, err := nkeys.CreateUser()
		if err != nil {
			t.Fatal(err)
		}
		defer installer.Wipe()
		installerPublic, err := installer.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := sharedReplayPrepareNATS(original, public, installerPublic)
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(output) {
			t.Fatal("new absolute fixture path required")
		}
		if err := os.Mkdir(output, 0700); err != nil {
			t.Fatal(err)
		}
		for name, raw := range map[string][]byte{"nats.conf": projected, "prepare.nats.conf": prepared, "installer.nkey": []byte(installerPublic), "replay.seed": files["edge/replay.seed"], "replay.nkey": files["public/replay.nkey"]} {
			f, err := os.OpenFile(filepath.Join(output, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := f.Write(raw)
			closeErr := f.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatal("synthetic fixture write failed")
			}
		}
	}
}
