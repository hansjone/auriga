package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedServiceProjectionKeepsCredentialsInTheirRequiredRoles(t *testing.T) {
	source := sharedFixture(t)
	source["platform/uac-adapter.json"] = []byte(sharedApprovedAdapterFixture)
	source["public/profile.yml"] = []byte("synthetic-approved-profile")
	source["public/host.json"] = []byte(`{"publicOnly":true}`)
	edge := []byte(`{"issuer":"https://fixture.example.invalid","loginPreference":"team","iamRpc":{"address":"platform:9091","serverName":"platform","serviceTokenFile":"/run/auriga-internal/edge/service.jwt","timeoutMillis":2000},"iamHop":{"certFile":"/etc/auriga/pki/edge-iam.crt","keyFile":"/etc/auriga/pki/edge-iam.key","caFile":"/etc/auriga/pki/ca.crt","serverName":"platform"}}`)
	gateway := []byte(`{"origin":"http://bifrost:8080","memberPasswordFile":"/run/secrets/bifrost_member_password","iam":{"Target":"platform:9091"},"org":{"Target":"platform:9091"}}`)
	in := sharedProjectionInputs{Receipt: &sharedInstallationReceipt{Tenant: "11111111-2222-4333-8444-555555555555", Node: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}, Source: source, RuntimeJSON: []byte(`{"syntheticRuntime":true}`), InternalCA: []byte("synthetic-public-ca"), EdgeConfig: edge, BifrostConfig: gateway, NATSConfig: sharedRenderedNATSFixture(t)}
	projected, err := sharedServiceProjection(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 20 {
		t.Fatal("role leaf set changed without explicit review", len(projected))
	}
	for name, file := range projected {
		if strings.HasPrefix(name, "platform/") {
			if file.UID != 10001 || file.Mode != 0600 {
				t.Fatal("platform key/secret projection metadata changed")
			}
		} else {
			for _, private := range []string{"platform/agent-secret/installation.secret", "platform/ticket.pk8.pem", "platform/enterprise.pk8.pem", "platform/model.pk8.pem", "platform/message.pk8.pem", "platform/uac-adapter.json"} {
				if bytes.Contains(file.Body, source[private]) {
					t.Fatal("platform private material escaped role", name, private)
				}
			}
		}
		if !strings.HasPrefix(name, "edge/") && bytes.Contains(file.Body, source["edge/replay.seed"]) {
			t.Fatal("Edge private seed escaped role", name)
		}
		if strings.HasPrefix(name, "agent/") && bytes.Contains(file.Body, []byte("PRIVATE KEY")) {
			t.Fatal("Agent/Host public projection contains a signing key")
		}
	}
	if !bytes.Contains(projected["platform/platform.properties"].Body, []byte("allow-raw-agent-token=true")) || !bytes.Contains(projected["platform/platform.properties"].Body, []byte("agent-token-export.installation-owner=10001")) {
		t.Fatal("current-member raw credential/exporter policy was not wired")
	}
	var gotEdge map[string]json.RawMessage
	if json.Unmarshal(projected["edge/edge.config.json"].Body, &gotEdge) != nil || string(gotEdge["loginPreference"]) != `"team"` || !bytes.Contains(gotEdge["sharedDsh"], []byte("natsSeedFile")) || bytes.Contains(gotEdge["sharedDsh"], []byte("natsCredsFile")) {
		t.Fatal("Edge original deployment context or explicit NKey mode changed")
	}
	var gotGateway map[string]json.RawMessage
	if json.Unmarshal(projected["gateway/bifrost-runtime.json"].Body, &gotGateway) != nil || string(gotGateway["memberPasswordFile"]) != `"/run/secrets/bifrost_member_password"` || !bytes.Contains(gotGateway["sharedModel"], []byte(in.Receipt.Node)) {
		t.Fatal("existing model credentials or exact Node TLS allowlist changed")
	}
	services := projected["agent/services.json"].Body
	if !bytes.Contains(services, []byte("https://platform:19091")) || !bytes.Contains(services, []byte("https://ai-relay:18087")) || !bytes.Contains(services, []byte("/run/auriga-agent-token/agent/service.jwt")) {
		t.Fatal("authenticated Agent service endpoints absent")
	}
	if bytes.Contains(projected["agent/agent.json"].Body, []byte("dataRoot")) {
		t.Fatal("projection replaced existing Agent data root")
	}
	if _, ok := gotGateway["sharedMessages"]; ok {
		t.Fatal("uninstalled message authority was silently enabled")
	}
	saved := bytes.Clone(projected["edge/replay.seed"].Body)
	for _, raw := range source {
		clear(raw)
	}
	if !bytes.Equal(projected["edge/replay.seed"].Body, saved) {
		t.Fatal("projection aliases cleared source private memory")
	}
}
