package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const sharedApprovedAdapterFixture = `{"privateSession":{"cookie":"synthetic-private-cookie","token":"synthetic-private-token"},"sharedOutbound":{"empNoHeader":"X-Fixture-Emp","authValueHeader":"X-Fixture-Auth","allowedDestinations":[{"origin":"https://Z.example.invalid:8443","addresses":["192.0.2.8","2001:db8::8"]},{"origin":"https://a.example.invalid/","addresses":["192.0.2.9"]}]}}`

func TestSharedEnterpriseProjectionUsesOnlyApprovedPublicFields(t *testing.T) {
	raw, err := sharedEnterprisePolicy([]byte(sharedApprovedAdapterFixture))
	if err != nil {
		t.Fatal(err)
	}
	var got sharedPublicEnterprisePolicy
	if json.Unmarshal(raw, &got) != nil || !got.AllowRawAgentToken || strings.Join(got.OutboundAllowedHosts, ",") != "a.example.invalid,z.example.invalid" || got.EmpNoHeader != "X-Fixture-Emp" || got.AuthValueHeader != "X-Fixture-Auth" {
		t.Fatal("approved projection changed")
	}
	for _, secret := range []string{"privateSession", "cookie", "synthetic-private", "192.0.2.8", "addresses", "8443"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private adapter field escaped projection")
		}
	}
	for name, input := range map[string]string{
		"null-outbound":     `{"sharedOutbound":null}`,
		"empty":             `{"sharedOutbound":{}}`,
		"unknown":           strings.Replace(sharedApprovedAdapterFixture, `"empNoHeader":`, `"unknown":true,"empNoHeader":`, 1),
		"duplicate-public":  strings.Replace(sharedApprovedAdapterFixture, `"empNoHeader":`, `"empNoHeader":"X-Other","empNoHeader":`, 1),
		"duplicate-private": strings.Replace(sharedApprovedAdapterFixture, `"cookie":`, `"cookie":"other","cookie":`, 1),
		"trailing":          sharedApprovedAdapterFixture + ` {}`,
		"plaintext":         strings.Replace(sharedApprovedAdapterFixture, "https://Z", "http://Z", 1),
		"userinfo":          strings.Replace(sharedApprovedAdapterFixture, "https://Z", "https://user:secret@Z", 1),
		"path":              strings.Replace(sharedApprovedAdapterFixture, ":8443\"", ":8443/private\"", 1),
		"wildcard":          strings.Replace(sharedApprovedAdapterFixture, "Z.example", "*.example", 1),
		"query":             strings.Replace(sharedApprovedAdapterFixture, ":8443\"", ":8443?\"", 1),
		"port":              strings.Replace(sharedApprovedAdapterFixture, ":8443", ":65536", 1),
		"dns-pin":           strings.Replace(sharedApprovedAdapterFixture, "192.0.2.8", "pin.example.invalid", 1),
		"wrong-literal-pin": strings.Replace(sharedApprovedAdapterFixture, "Z.example.invalid", "192.0.2.99", 1),
		"transport-header":  strings.Replace(sharedApprovedAdapterFixture, "X-Fixture-Emp", "Host", 1),
		"same-header":       strings.Replace(sharedApprovedAdapterFixture, "X-Fixture-Emp", "x-fixture-auth", 1),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := sharedEnterprisePolicy([]byte(input))
			if err == nil || out != nil {
				t.Fatal("unapproved or ambiguous policy accepted")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-cookie") {
				t.Fatal("error exposed private values")
			}
		})
	}
}

func TestSharedRuntimeProjectionPreservesFullSpecAndRejectsSealedOrUnknown(t *testing.T) {
	// This is synthetic data for conversion only, never a verified runtime proof.
	spec := &nodev1.InstanceSpec{TenantId: "fixture-tenant", OwnerId: "fixture-owner", Generation: 37, Runtime: nodev1.RuntimeKind_RUNTIME_KIND_DOCKER, Dsh: &nodev1.DshRuntime{Version: "fixture", Image: "fixture.invalid/dsh@sha256:" + strings.Repeat("a", 64), ReleaseEnvelope: &nodev1.RuntimeReleaseEnvelope{ReleaseSequence: 29, ArchiveDigest: "fixture-digest"}}, Labels: map[string]string{"fixture": "preserved"}, ResourceSelectionRevision: 42}
	spec.Resources = &nodev1.Resources{CpuMillis: 250, MemoryMb: 384, DiskGb: 17, Pids: 91}
	encode := func(s *nodev1.InstanceSpec) []byte {
		raw, err := proto.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := encode(spec)
	output, err := sharedRuntimeSpecJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := new(nodev1.InstanceSpec)
	if protojson.Unmarshal(output, got) != nil || !proto.Equal(spec, got) || !bytes.Equal(raw, encode(spec)) {
		t.Fatal("complete spec or original input changed")
	}
	for name, secrets := range map[string]*nodev1.Secrets{
		"instance": {InstanceToken: &nodev1.SealedSecret{}},
		"gateway":  {AiGatewayToken: &nodev1.SealedSecret{}},
		"device":   {DeviceCredential: &nodev1.SealedSecret{}},
		"model":    {ModelCredential: &nodev1.SealedSecret{}},
	} {
		t.Run("private-"+name, func(t *testing.T) {
			altered := proto.Clone(spec).(*nodev1.InstanceSpec)
			altered.Secrets = secrets
			if output, err := sharedRuntimeSpecJSON(encode(altered)); err == nil || output != nil {
				t.Fatal("private sealed credential exported")
			}
		})
	}
	for name, altered := range map[string]*nodev1.InstanceSpec{"missing-envelope": proto.Clone(spec).(*nodev1.InstanceSpec), "sealed": proto.Clone(spec).(*nodev1.InstanceSpec), "nested-unknown": proto.Clone(spec).(*nodev1.InstanceSpec), "top-unknown": proto.Clone(spec).(*nodev1.InstanceSpec)} {
		switch name {
		case "missing-envelope":
			altered.Dsh.ReleaseEnvelope = nil
		case "sealed":
			altered.Secrets = &nodev1.Secrets{InstanceToken: &nodev1.SealedSecret{}}
		case "nested-unknown":
			altered.Dsh.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 50000, protowire.VarintType), 1))
		case "top-unknown":
			altered.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 50001, protowire.VarintType), 1))
		}
		t.Run(name, func(t *testing.T) {
			out, err := sharedRuntimeSpecJSON(encode(altered))
			if err == nil || out != nil {
				t.Fatal("non-public or lossy template accepted")
			}
		})
	}
}
