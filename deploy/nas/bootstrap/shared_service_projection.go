package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Each role receives only its own immutable projection. The source, Agent
// installation secret and service-token output directory are never Host mounts.
type sharedProjectionInputs struct {
	Receipt       *sharedInstallationReceipt
	Source        map[string][]byte
	RuntimeJSON   []byte // resolved by the existing runtime/proof verifier
	InternalCA    []byte // fixed public sealed CA
	EdgeConfig    []byte // original verified base/collab projection
	BifrostConfig []byte // original verified Bifrost material source
	NATSConfig    []byte // freshly rendered standard password-user configuration
}

func sharedServiceProjection(in sharedProjectionInputs) (map[string]sharedProjectedFile, error) {
	if in.Receipt == nil || in.Receipt.Tenant == "" || in.Receipt.Node == "" || len(in.RuntimeJSON) == 0 || len(in.InternalCA) == 0 {
		return nil, fmt.Errorf("complete verified shared projection inputs required")
	}
	dedicated, err := sharedDedicatedProfile(in.Source, in.Receipt.DedicatedProfile)
	if err != nil {
		return nil, err
	}
	files := map[string]sharedProjectedFile{}
	put := func(name string, raw []byte, uid uint32, mode os.FileMode) {
		files[name] = sharedProjectedFile{Body: bytes.Clone(raw), UID: uid, Mode: mode}
	}
	jsonFile := func(name string, value any, uid uint32) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		put(name, append(raw, '\n'), uid, 0600)
		return nil
	}
	var properties strings.Builder
	properties.WriteString("auriga.workspace.shared-dsh.enabled=true\nauriga.iam.shared-tasks.enabled=true\nauriga.iam.shared-tasks.allow-raw-agent-token=true\n")
	ticket, err := sharedMaterialPublicKey(in.Source, "ticket")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&properties, "auriga.workspace.shared-dsh.ticket-key-id=%s\nauriga.workspace.shared-dsh.ticket-private-key-file=/etc/auriga/shared/ticket.pk8.pem\n", ticket.Kid)
	for _, purpose := range []string{"enterprise", "model", "message"} {
		key, err := sharedMaterialPublicKey(in.Source, purpose)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&properties, "auriga.iam.shared-tasks.signing-keys.%s.kid=%s\nauriga.iam.shared-tasks.signing-keys.%s.private-key-file=/etc/auriga/shared/%s.pk8.pem\n", purpose, key.Kid, purpose, purpose)
		put("platform/"+purpose+".pk8.pem", in.Source["platform/"+purpose+".pk8.pem"], 10001, 0600)
	}
	put("platform/ticket.pk8.pem", in.Source["platform/ticket.pk8.pem"], 10001, 0600)
	secretHash := string(in.Source["platform/agent-secret.sha256"])
	if len(secretHash) != 64 {
		return nil, fmt.Errorf("verified Agent installation secret hash required")
	}
	fmt.Fprintf(&properties, "auriga.iam.internal-secret-hashes.agent=%s\nauriga.iam.agent-token-export.enabled=true\nauriga.iam.agent-token-export.installation-owner=10001\nauriga.iam.agent-token-export.installation-secret-file=/etc/auriga/shared/agent-secret/installation.secret\nauriga.iam.agent-token-export.token-file=/run/auriga-agent-token/agent/service.jwt\nauriga.iam.uac.adapter-config-file=/etc/auriga/shared/uac-adapter.json\n", secretHash)
	put("platform/platform.properties", []byte(properties.String()), 10001, 0600)
	put("platform/agent-secret/installation.secret", in.Source["platform/agent-secret/installation.secret"], 10001, 0600)
	put("platform/uac-adapter.json", in.Source["platform/uac-adapter.json"], 10001, 0600)
	if sharedUniqueJSON(in.EdgeConfig) != nil {
		return nil, fmt.Errorf("original Edge projection JSON refused")
	}
	var edge map[string]json.RawMessage
	if json.Unmarshal(in.EdgeConfig, &edge) != nil || len(edge["iamRpc"]) == 0 || len(edge["iamHop"]) == 0 || len(edge["sharedDsh"]) != 0 {
		return nil, fmt.Errorf("original authenticated Edge RPC projection required")
	}
	shared := map[string]any{"rpc": edge["iamRpc"], "hop": edge["iamHop"], "natsSeedFile": "/etc/auriga/shared/replay.seed", "ticketPublicKeysFile": "/etc/auriga/shared/ticket.jwks.json", "identityKeysFile": "/etc/auriga/shared/identity-signing-keys.json"}
	sharedRaw, err := json.Marshal(shared)
	if err != nil {
		return nil, err
	}
	edge["sharedDsh"] = sharedRaw
	if err := jsonFile("edge/edge.config.json", edge, 65532); err != nil {
		return nil, err
	}
	put("edge/replay.seed", in.Source["edge/replay.seed"], 65532, 0600)
	put("edge/identity-signing-keys.json", in.Source["edge/identity-signing-keys.json"], 65532, 0600)
	put("edge/ticket.jwks.json", in.Source["public/ticket.jwks.json"], 65532, 0644)
	if sharedUniqueJSON(in.BifrostConfig) != nil {
		return nil, fmt.Errorf("original Bifrost projection JSON refused")
	}
	var gateway map[string]json.RawMessage
	if json.Unmarshal(in.BifrostConfig, &gateway) != nil || len(gateway["iam"]) == 0 || len(gateway["org"]) == 0 || len(gateway["memberPasswordFile"]) == 0 || len(gateway["sharedModel"]) != 0 || len(gateway["sharedMessages"]) != 0 {
		return nil, fmt.Errorf("original authenticated Bifrost material projection required")
	}
	model, err := sharedMaterialPublicKey(in.Source, "model")
	if err != nil {
		return nil, err
	}
	listener := map[string]any{"listen": "0.0.0.0:18887", "tenantId": in.Receipt.Tenant, "allowedNodeIds": []string{in.Receipt.Node}, "modelPublicKeys": map[string]string{model.Kid: "/etc/auriga/shared/model.pem"}, "tlsCertFile": "/etc/auriga/pki/gateway.crt", "tlsKeyFile": "/etc/auriga/pki/gateway.key", "clientCAFile": "/etc/auriga/pki/ca.crt"}
	listenerRaw, err := json.Marshal(listener)
	if err != nil {
		return nil, err
	}
	gateway["sharedModel"] = listenerRaw
	if err := jsonFile("gateway/bifrost-runtime.json", gateway, 10001); err != nil {
		return nil, err
	}
	put("gateway/model.pem", in.Source["public/model.pem"], 10001, 0644)
	endpoint := func(address string) map[string]string {
		return map[string]string{"address": address, "caFile": "/etc/auriga/shared-runtime/internal-ca.crt"}
	}
	services := map[string]any{"tenantId": in.Receipt.Tenant, "workspace": endpoint("https://platform:19091"), "iam": endpoint("https://platform:19091"), "serviceTokenFile": "/run/auriga-agent-token/agent/service.jwt", "sharedModel": endpoint("https://ai-relay:18087")}
	if err := jsonFile("agent/services.json", services, 0); err != nil {
		return nil, err
	}
	storageSpec, err := sharedStorageSpecFromReceipt(in.Receipt)
	if err != nil {
		return nil, err
	}
	projectFirst, projectLast := storageSpec.effectiveProjectRange()
	var preparation map[string]any
	if storageSpec.Backend == "xfs" {
		preparation = map[string]any{"backend": "xfs", "quotaMount": storageSpec.QuotaMount, "quotaBinary": storageSpec.QuotaBinary, "projectFirst": projectFirst, "projectLast": projectLast}
	} else {
		preparation = map[string]any{"backend": "ext4-image", "imageBytes": int64(137438953472), "mkfsBinary": "/usr/sbin/mkfs.ext4", "losetupBinary": "/usr/sbin/losetup", "projectFirst": projectFirst, "projectLast": projectLast}
	}
	agent := map[string]any{"sharedDshPreparation": preparation, "sharedDshRuntime": map[string]string{"templateFile": "/etc/auriga/shared-runtime/runtime.json", "profileFile": "/etc/auriga/shared-runtime/profile.yml", "servicesFile": "/etc/auriga/shared-runtime/services.json", "hostConfigFile": "/etc/auriga/shared-runtime/host.json"}}
	if dedicated != nil {
		agent["sharedDshRuntime"].(map[string]string)["dedicatedProfileFile"] = "/etc/auriga/shared-runtime/dedicated-profile.yml"
		put("agent/dedicated-profile.yml", dedicated, 0, 0444)
	}
	if err := jsonFile("agent/agent.json", agent, 0); err != nil {
		return nil, err
	}
	put("agent/runtime.json", in.RuntimeJSON, 0, 0444)
	put("agent/profile.yml", in.Source["public/profile.yml"], 0, 0444)
	put("agent/host.json", in.Source["public/host.json"], 0, 0444)
	put("agent/internal-ca.crt", in.InternalCA, 0, 0444)
	nats, err := sharedNATSProjection(in.NATSConfig, string(in.Source["public/replay.nkey"]))
	if err != nil {
		return nil, err
	}
	put("nats/nats.conf", nats, 65532, 0600)
	for _, file := range files {
		if len(file.Body) == 0 {
			return nil, fmt.Errorf("shared role projection leaf absent")
		}
	}
	return files, nil
}
