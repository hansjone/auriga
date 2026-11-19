package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

func TestSharedNATSAuthenticationIsExplicitAndMutuallyExclusive(t *testing.T) {
	for _, config := range []*SharedDshConfig{nil, {}, {NATSCredsFile: "creds", NATSSeedFile: "seed"}, {NATSCredsFile: "relative"}, {NATSSeedFile: "relative"}} {
		if _, err := sharedNATSAuthentication(config); err == nil {
			t.Fatal("ambiguous or non-installation authentication accepted")
		}
	}
	for _, raw := range []string{
		`{"natsAuthMode":"unknown"}`, `{"natsSeedFile":"a","natsSeedFile":"b"}`,
		`{"NATSSeedFile":"a"}`, `{"rpc":{"unknown":"field"}}`,
		`{"natsSeedFile":"a"} {}`, `null`,
	} {
		var config SharedDshConfig
		if json.Unmarshal([]byte(raw), &config) == nil {
			t.Fatal("unknown or duplicate shared installation configuration accepted")
		}
	}
	var config SharedDshConfig
	encoded, _ := json.Marshal(map[string]string{"natsCredsFile": filepath.Join(t.TempDir(), "legacy.creds")})
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	option, err := sharedNATSAuthentication(&config)
	if err != nil {
		t.Fatal(err)
	}
	options := nats.GetDefaultOptions()
	// The retained library adapter actually probes its installation file.
	// No account JWT is forged merely to exercise this compatibility branch.
	if err := option(&options); !errors.Is(err, os.ErrNotExist) || options.Nkey != "" {
		t.Fatal("legacy UserCredentials adapter did not read its fixed file")
	}
	if origin, err := sharedNATSOrigin("nats://old:synthetic@nats.example.invalid:4222"); err != nil || origin != "nats://nats.example.invalid:4222" {
		t.Fatal("dedicated NKey connection inherited another service password")
	}
	for _, raw := range []string{"", "http://nats.example.invalid", "nats://nats.example.invalid/path", "nats://nats.example.invalid?token=synthetic", "nats://nats.example.invalid/#x"} {
		if _, err := sharedNATSOrigin(raw); err == nil {
			t.Fatal("nonfixed replay origin accepted")
		}
	}
}

func TestSharedNATSSeedSignsTheResolvedUserAndRejectsChangedPrivateInput(t *testing.T) {
	key, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer key.Wipe()
	seed, err := key.Seed()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(seed)
	public, err := key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replay.seed")
	if fixture := os.Getenv("AURIGA_SHARED_MATERIAL_FIXTURE"); fixture != "" {
		path = filepath.Join(fixture, "edge", "replay.seed")
		bytes, err := os.ReadFile(filepath.Join(fixture, "public", "replay.nkey"))
		if err != nil {
			t.Fatal(err)
		}
		public = string(bytes)
	} else if err := os.WriteFile(path, seed, 0600); err != nil {
		t.Fatal(err)
	}
	option, err := sharedNATSAuthentication(&SharedDshConfig{NATSSeedFile: path})
	if err != nil {
		t.Fatal(err)
	}
	options := nats.GetDefaultOptions()
	if err := option(&options); err != nil || options.Nkey != public || options.UserJWT != nil || options.SignatureCB == nil {
		t.Fatal("fixed NkeyOptionFromSeed adapter failed")
	}
	nonce := []byte("synthetic-server-nonce")
	signature, err := options.SignatureCB(nonce)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := nkeys.FromPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Wipe()
	if verify.Verify(nonce, signature) != nil {
		t.Fatal("production callback signature does not match generated user")
	}
	mixed := nats.GetDefaultOptions()
	mixed.User = "other-service"
	if option(&mixed) == nil {
		t.Fatal("NKey adapter combined another service identity")
	}
	// Mutations use a new isolated copy, never change an exported producer fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	copyPath := filepath.Join(t.TempDir(), "seed")
	if err := os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	copyOption, err := sharedNATSAuthentication(&SharedDshConfig{NATSSeedFile: copyPath})
	if err != nil {
		t.Fatal(err)
	}
	current := nats.GetDefaultOptions()
	if err := copyOption(&current); err != nil {
		t.Fatal(err)
	}
	other, _ := nkeys.CreateUser()
	defer other.Wipe()
	otherSeed, _ := other.Seed()
	defer clear(otherSeed)
	if err := os.WriteFile(copyPath, otherSeed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SignatureCB(nonce); err == nil {
		t.Fatal("changed resolved NKey accepted on reconnect")
	}
	if err := os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(copyPath, 0640); err != nil {
			t.Fatal(err)
		}
		if _, err := current.SignatureCB(nonce); err == nil {
			t.Fatal("group-readable seed accepted")
		}
		if err := os.Chmod(copyPath, 0600); err != nil {
			t.Fatal(err)
		}
		hard := copyPath + "-hard"
		if err := os.Link(copyPath, hard); err != nil {
			t.Fatal(err)
		}
		if _, err := current.SignatureCB(nonce); err == nil {
			t.Fatal("hard-linked seed accepted")
		}
		if err := os.Remove(hard); err != nil {
			t.Fatal(err)
		}
	}
	account, _ := nkeys.CreateAccount()
	defer account.Wipe()
	accountSeed, _ := account.Seed()
	defer clear(accountSeed)
	if err := os.WriteFile(copyPath, accountSeed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedNATSAuthentication(&SharedDshConfig{NATSSeedFile: copyPath}); err == nil {
		t.Fatal("account seed accepted as user")
	}
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SignatureCB(nonce); err == nil {
		t.Fatal("missing seed accepted")
	}
}

func TestSharedReplayNATSServerConfigUsesOnlyDedicatedUserAndBucket(t *testing.T) {
	var user []byte
	if fixture := os.Getenv("AURIGA_SHARED_MATERIAL_FIXTURE"); fixture != "" {
		var err error
		user, err = os.ReadFile(filepath.Join(fixture, "replay-user.conf"))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		// This is the exact go:embed source used by the installation producer.
		raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "nas", "bootstrap", "shared_replay_user.conf.tmpl"))
		if err != nil {
			t.Fatal(err)
		}
		key, _ := nkeys.CreateUser()
		defer key.Wipe()
		public, _ := key.PublicKey()
		quoted, _ := json.Marshal(public)
		user = []byte(fmt.Sprintf(string(raw), quoted))
	}
	path := filepath.Join(t.TempDir(), "nats.conf")
	config := []byte("port: -1\njetstream: {store_dir: '/unused-fixture-store'}\naccounts {\nAURIGA: {\njetstream: enabled\nusers: [\n" + string(user) + "\n]\n}\n}\n")
	fullFixture := os.Getenv("AURIGA_SHARED_NATS_FIXTURE")
	if fullFixture != "" {
		var err error
		config, err = os.ReadFile(filepath.Join(fullFixture, "nats.conf"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	options, err := server.ProcessConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacyUsers := 0
	if fullFixture != "" {
		legacyUsers = 5
	} // SYS sys plus four original AURIGA password users.
	if len(options.Nkeys) != 1 || len(options.Users) != legacyUsers {
		t.Fatal("replay config did not define one dedicated NKey user")
	}
	if fullFixture != "" {
		platformFound := false
		for _, legacy := range options.Users {
			if legacy.Password != "synthetic-password-not-a-real-credential" {
				t.Fatal("legacy password changed by shared projection")
			}
			if legacy.Username != "platform" {
				continue
			}
			platformFound = true
			if legacy.Permissions == nil || legacy.Permissions.Publish == nil {
				t.Fatal("platform read permissions missing")
			}
			readSubjects := map[string]bool{}
			for _, subject := range legacy.Permissions.Publish.Allow {
				if !strings.Contains(subject, "auriga-shared-entry") {
					continue
				}
				if subject != "$JS.API.STREAM.INFO.KV_auriga-shared-entry" && subject != "$JS.API.STREAM.MSG.GET.KV_auriga-shared-entry" && subject != "$JS.API.DIRECT.GET.KV_auriga-shared-entry" && subject != "$JS.API.DIRECT.GET.KV_auriga-shared-entry.>" {
					t.Fatal("platform received shared write or management authority")
				}
				readSubjects[subject] = true
			}
			if len(readSubjects) != 4 {
				t.Fatal("platform dedicated read permission set changed")
			}
		}
		if !platformFound {
			t.Fatal("legacy platform user absent")
		}
		prepareOptions, err := server.ProcessConfigFile(filepath.Join(fullFixture, "prepare.nats.conf"))
		if err != nil {
			t.Fatal(err)
		}
		installerPublic, err := os.ReadFile(filepath.Join(fullFixture, "installer.nkey"))
		if err != nil {
			t.Fatal(err)
		}
		if len(prepareOptions.Nkeys) != 2 || len(prepareOptions.Users) != len(options.Users) {
			t.Fatal("prepare configuration changed resident accounts")
		}
		installerFound := false
		for _, key := range prepareOptions.Nkeys {
			if key.Nkey != string(installerPublic) {
				continue
			}
			installerFound = true
			p := key.Permissions
			if p == nil || p.Publish == nil || p.Subscribe == nil || len(p.Subscribe.Allow) != 1 || p.Subscribe.Allow[0] != "_INBOX_auriga_shared_install.>" || len(p.Publish.Allow) != 3 {
				t.Fatal("installer permissions changed")
			}
			allow := map[string]bool{"$JS.API.INFO": true, "$JS.API.STREAM.INFO.KV_auriga-shared-entry": true, "$JS.API.STREAM.CREATE.KV_auriga-shared-entry": true}
			for _, subject := range p.Publish.Allow {
				if !allow[subject] {
					t.Fatal("installer has foreign/read/update authority")
				}
				delete(allow, subject)
			}
			if len(allow) != 0 {
				t.Fatal("installer exact permission set differs")
			}
		}
		if !installerFound {
			t.Fatal("prepare configuration lacks independent installer")
		}
		for _, key := range options.Nkeys {
			if key.Nkey == string(installerPublic) {
				t.Fatal("installer remains in final server configuration")
			}
		}
		public, err := os.ReadFile(filepath.Join(fullFixture, "replay.nkey"))
		if err != nil {
			t.Fatal(err)
		}
		option, err := sharedNATSAuthentication(&SharedDshConfig{NATSSeedFile: filepath.Join(fullFixture, "replay.seed")})
		if err != nil {
			t.Fatal(err)
		}
		client := nats.GetDefaultOptions()
		if err := option(&client); err != nil || client.Nkey != string(public) {
			t.Fatal("consumer seed does not identify projected server user", err)
		}
		verifier, err := nkeys.FromPublicKey(string(public))
		if err != nil {
			t.Fatal(err)
		}
		defer verifier.Wipe()
		nonce := []byte("synthetic-full-NAS-nonce")
		signature, err := client.SignatureCB(nonce)
		if err != nil || verifier.Verify(nonce, signature) != nil {
			t.Fatal("actual fresh seed file did not sign projected user nonce")
		}
	}
	permissions := options.Nkeys[0].Permissions
	if permissions == nil || permissions.Publish == nil || permissions.Subscribe == nil {
		t.Fatal("replay permissions absent")
	}
	if len(permissions.Subscribe.Allow) != 1 || permissions.Subscribe.Allow[0] != "_INBOX_auriga_shared_entry.>" {
		t.Fatal("foreign inbox subscription granted")
	}
	if len(permissions.Publish.Allow) != 5 {
		t.Fatal("replay publish allowlist changed without contract review")
	}
	for _, subject := range permissions.Publish.Allow {
		if subject == "$KV.auriga-shared-entry.ticket.*" {
			continue
		}
		if !strings.HasPrefix(subject, "$JS.API.") || !strings.Contains(subject, ".KV_auriga-shared-entry") || strings.Contains(subject, "CREATE") || strings.Contains(subject, "UPDATE") || strings.Contains(subject, "DELETE") || strings.Contains(subject, "CONSUMER") {
			t.Fatal("replay identity has foreign/global authority")
		}
	}
	// Parse only: no NewServer, Start, connection, JetStream mutation or network.
}
