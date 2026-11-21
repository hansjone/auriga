package installation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedHostInstallationUsesOriginalRendererAndExclusiveOutput(t *testing.T) {
	dir := t.TempDir()
	inputs := SharedHostInputs{Model: "--output=literal-model-value", OutputFile: filepath.Join(dir, "host.json"), EnterprisePolicyFile: filepath.Join(dir, "policy.json")}
	for name, target := range map[string]*string{"identity": &inputs.IdentityKeysFile, "enterprise": &inputs.EnterpriseKeysFile, "model": &inputs.ModelKeysFile, "message": &inputs.MessageKeysFile} {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": name, "x": base64.RawURLEncoding.EncodeToString(public)}}})
		*target = filepath.Join(dir, name+".json")
		if err := os.WriteFile(*target, raw, 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(inputs.EnterprisePolicyFile, []byte(`{"allowRawAgentToken":true,"outboundAllowedHosts":["fixture.example.invalid"],"empNoHeader":"X-Fixture-Emp","authValueHeader":"X-Fixture-Auth"}`), 0444); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := json.Marshal(map[string]any{"realm": "7", "roots": []any{map[string]string{"kid": "fixture-root", "public_key_spki": base64.RawURLEncoding.EncodeToString(der)}}})
	inputs.MessageTrustFile = filepath.Join(dir, "trust.json")
	if err := os.WriteFile(inputs.MessageTrustFile, trust, 0444); err != nil {
		t.Fatal(err)
	}
	if err := RenderSharedHostConfig(inputs); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(inputs.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model          string
		IdentityKeys   map[string]string
		CapabilityKeys map[string]map[string]string
		MessageTrust   struct{ Realm string }
	}
	if json.Unmarshal(raw, &got) != nil || got.Model != inputs.Model || len(got.IdentityKeys) != 1 || len(got.CapabilityKeys) != 3 || got.MessageTrust.Realm != "7" || bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("fixed renderer public projection changed")
	}
	if err := RenderSharedHostConfig(inputs); !errors.Is(err, os.ErrExist) {
		t.Fatal("original exclusive-create error lost", err)
	}
	again, err := os.ReadFile(inputs.OutputFile)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("existing output changed")
	}
	inputs.OutputFile = filepath.Join(dir, "rejected.json")
	inputs.ModelKeysFile = inputs.EnterpriseKeysFile
	if err := RenderSharedHostConfig(inputs); err == nil {
		t.Fatal("original cross-purpose key rejection lost")
	}
	if _, err := os.Stat(inputs.OutputFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected renderer created output")
	}
}

func TestSharedInstallationProfileUsesTheProductionPoolValidator(t *testing.T) {
	profile, err := os.ReadFile("../../../deploy/nas/shared-runtime-profile.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSharedProfile(profile); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{nil, []byte("[]"), append(append([]byte{}, profile...), []byte("\n- id: arbitrary-shell\n  name: '@deepseek-ai/dsh-tool-shell'\n")...)} {
		if err := ValidateSharedProfile(invalid); err == nil {
			t.Fatal("non-production profile accepted by installation facade")
		}
	}
}

func TestDedicatedInstallationProfileUsesItsSeparateProductionValidator(t *testing.T) {
	profile, err := os.ReadFile("../../../deploy/nas/dedicated-runtime-profile.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDedicatedProfile(profile); err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile("../../../deploy/nas/shared-runtime-profile.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{nil, []byte("[]"), shared, append(bytes.Clone(profile), []byte("\n- id: unapproved\n  name: 'auriga-dsh'\n")...), bytes.Repeat([]byte(" "), (1<<20)+1)} {
		if err := ValidateDedicatedProfile(invalid); err == nil {
			t.Fatal("incomplete, cross-mode, unapproved or oversized dedicated profile accepted")
		}
	}
}

func TestSharedHostExplicitEmptyOutboundPolicyDoesNotInventCompanyDestinations(t *testing.T) {
	dir := t.TempDir()
	in := SharedHostInputs{Model: "synthetic-model", EnterprisePolicyFile: filepath.Join(dir, "policy.json")}
	for name, target := range map[string]*string{"identity": &in.IdentityKeysFile, "enterprise": &in.EnterpriseKeysFile, "model": &in.ModelKeysFile, "message": &in.MessageKeysFile} {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": name, "x": base64.RawURLEncoding.EncodeToString(public)}}})
		*target = filepath.Join(dir, name+".json")
		if err := os.WriteFile(*target, raw, 0444); err != nil {
			t.Fatal(err)
		}
	}
	for name, policy := range map[string]string{
		"deny-all":                       `{"allowRawAgentToken":true,"outboundAllowedHosts":[]}`,
		"missing-host-list":              `{"allowRawAgentToken":true}`,
		"null-host-list":                 `{"allowRawAgentToken":true,"outboundAllowedHosts":null}`,
		"half-headers":                   `{"allowRawAgentToken":true,"outboundAllowedHosts":["fixture.example.invalid"],"empNoHeader":"X-Fixture-Emp"}`,
		"invented-headers-without-hosts": `{"allowRawAgentToken":true,"outboundAllowedHosts":[],"empNoHeader":"X-Fixture-Emp","authValueHeader":"X-Fixture-Token"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(in.EnterprisePolicyFile, []byte(policy), 0600); err != nil {
				t.Fatal(err)
			}
			in.OutputFile = filepath.Join(dir, name+"-host.json")
			err := RenderSharedHostConfig(in)
			if name != "deny-all" {
				if err == nil {
					t.Fatal("ambiguous or incomplete enterprise policy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(in.OutputFile)
			if err != nil {
				t.Fatal(err)
			}
			var host map[string]json.RawMessage
			if json.Unmarshal(raw, &host) != nil || len(host["model"]) == 0 || len(host["capabilityKeys"]) == 0 {
				t.Fatal("ordinary/model public material disappeared")
			}
			var settings map[string]json.RawMessage
			if json.Unmarshal(host["enterprise"], &settings) != nil || len(settings) != 2 || string(settings["allowRawAgentToken"]) != "true" || string(settings["outboundAllowedHosts"]) != "[]" {
				t.Fatal("renderer invented enterprise access or disabled member credentials")
			}
		})
	}
}
