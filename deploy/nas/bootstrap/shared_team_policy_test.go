package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSharedMissingOutboundPolicyDeniesAllDestinationsAndPreservesApprovedAdapter(t *testing.T) {
	for name, raw := range map[string][]byte{
		"explicit-no-adapter":               []byte(sharedTeamOnlyAdapter),
		"existing-company-without-outbound": []byte(`{"clientIP":"192.0.2.8","allowedDestinations":[{"origin":"https://fixture.example.invalid","addresses":["192.0.2.9"]}],"qr":{"path":"/fixture"},"constants":{"syntheticPrivate":"preserve"},"tokenVerifyUrl":"https://fixture.example.invalid/verify"}`),
	} {
		t.Run(name, func(t *testing.T) {
			original := bytes.Clone(raw)
			public, err := sharedEnterprisePolicy(raw)
			if err != nil {
				t.Fatal(err)
			}
			var policy map[string]json.RawMessage
			if json.Unmarshal(public, &policy) != nil || string(policy["allowRawAgentToken"]) != "true" || string(policy["outboundAllowedHosts"]) != "[]" || len(policy) != 2 {
				t.Fatal("no-adapter policy did not explicitly deny all without disabling current-member raw credentials")
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("approved company adapter mappings changed")
			}
			for _, private := range []string{"qr", "constants", "tokenVerifyUrl", "192.0.2.8"} {
				if bytes.Contains(public, []byte(private)) {
					t.Fatal("private company mappings escaped public deny policy")
				}
			}
		})
	}
}
