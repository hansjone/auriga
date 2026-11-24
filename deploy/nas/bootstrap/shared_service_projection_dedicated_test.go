package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// This regression uses only pre-existing projection APIs, so it also compiles
// against the original shared-only producer and fails on its missing DED leaf.
func TestSharedDedicatedProfileIsApprovedAndProjectedToAgentOnly(t *testing.T) {
	in := sharedDedicatedProjectionFixture(t)
	projected, err := sharedServiceProjection(in)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Runtime struct {
			Profile   string `json:"profileFile"`
			Dedicated string `json:"dedicatedProfileFile"`
		} `json:"sharedDshRuntime"`
	}
	if json.Unmarshal(projected["agent/agent.json"].Body, &config) != nil || config.Runtime.Profile != "/etc/auriga/shared-runtime/profile.yml" || config.Runtime.Dedicated != "/etc/auriga/shared-runtime/dedicated-profile.yml" {
		t.Fatal("approved dedicated profile never reached the real Agent config field")
	}
	file, ok := projected["agent/dedicated-profile.yml"]
	if !ok || !bytes.Equal(file.Body, in.Source["public/dedicated-profile.yml"]) || file.UID != 0 || file.Mode != 0444 {
		t.Fatal("approved dedicated profile bytes or readonly Agent metadata lost")
	}
	for name, value := range projected {
		if name != "agent/dedicated-profile.yml" && bytes.Equal(value.Body, file.Body) {
			t.Fatal("dedicated profile escaped its fixed Agent role", name)
		}
		if strings.HasPrefix(name, "agent/") && bytes.Contains(value.Body, []byte("PRIVATE KEY")) {
			t.Fatal("private signing material escaped into Agent/Host inputs")
		}
	}
}

func TestSharedDedicatedProjectionRejectsUnapprovedOrChangedProfile(t *testing.T) {
	for _, kind := range []string{"unapproved", "missing", "wrong-digest", "incomplete", "shared-profile"} {
		t.Run(kind, func(t *testing.T) {
			in := sharedDedicatedProjectionFixture(t)
			switch kind {
			case "unapproved":
				*in.Receipt = sharedInstallationReceipt{}
				if err := json.Unmarshal([]byte(`{"tenant":"11111111-2222-4333-8444-555555555555","node":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`), in.Receipt); err != nil {
					t.Fatal(err)
				}
			case "missing":
				delete(in.Source, "public/dedicated-profile.yml")
			case "wrong-digest":
				in.Source["public/dedicated-profile.yml"] = append(in.Source["public/dedicated-profile.yml"], []byte("\n# changed after approval\n")...)
			case "incomplete", "shared-profile":
				raw := []byte("[]")
				if kind == "shared-profile" {
					var err error
					raw, err = os.ReadFile("../shared-runtime-profile.example.yml")
					if err != nil {
						t.Fatal(err)
					}
				}
				in.Source["public/dedicated-profile.yml"] = raw
				setSharedDedicatedFixtureReceipt(t, in.Receipt, sharedSHA(raw))
			}
			if _, err := sharedServiceProjection(in); err == nil {
				t.Fatal("unapproved or non-dedicated profile reached Agent projection")
			}
		})
	}
}

func setSharedDedicatedFixtureReceipt(t *testing.T, receipt *sharedInstallationReceipt, digest string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"tenant": "11111111-2222-4333-8444-555555555555", "node": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "dedicatedProfile": digest})
	if err != nil || json.Unmarshal(raw, receipt) != nil {
		t.Fatal("synthetic receipt encoding failed", err)
	}
}

func sharedDedicatedProjectionFixture(t *testing.T) sharedProjectionInputs {
	t.Helper()
	source := sharedFixture(t)
	source["platform/uac-adapter.json"] = []byte(sharedApprovedAdapterFixture)
	for name, path := range map[string]string{"public/profile.yml": "../shared-runtime-profile.example.yml", "public/dedicated-profile.yml": "../dedicated-runtime-profile.example.yml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source[name] = raw
	}
	source["public/host.json"] = []byte(`{"publicOnly":true}`)
	receipt := &sharedInstallationReceipt{}
	setSharedDedicatedFixtureReceipt(t, receipt, sharedSHA(source["public/dedicated-profile.yml"]))
	return sharedProjectionInputs{Receipt: receipt, Source: source, RuntimeJSON: []byte(`{"syntheticRuntime":true}`), InternalCA: []byte("synthetic-public-ca"), EdgeConfig: []byte(`{"iamRpc":{"address":"platform:9091"},"iamHop":{"serverName":"platform"}}`), BifrostConfig: []byte(`{"iam":{"Target":"platform:9091"},"org":{"Target":"platform:9091"},"memberPasswordFile":"/run/secrets/bifrost_member_password"}`), BifrostPGPassword: []byte(strings.Repeat("a", 64)), BifrostPGMigrationPassword: []byte(strings.Repeat("b", 64)), NATSConfig: sharedRenderedNATSFixture(t)}
}
