package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

var identityMaterialFiles = []string{
	"pki/ca.crt", "pki/ca.key", "pki/workspace.crt", "pki/workspace.key",
	"pki/edge.crt", "pki/edge.key", "pki/iam.crt", "pki/iam.key", "pki/edge-iam.crt", "pki/edge-iam.key",
	"identity-signing-keys.json", "identity-keys.json", "ca-sha256.txt",
	"access.json", "iam-healthcheck.sh", "iam-client.json", "heartbeat-observer.json",
	"edge.config.json", "access.env", "agent-policy.yaml",
}

func writeComposeKeys(out string) error {
	p, err := materials.Open(out)
	if err != nil {
		return err
	}
	defer p.Close()
	raw, err := p.Read("compose.keys")
	if os.IsNotExist(err) {
		var b strings.Builder
		for _, key := range []string{"AURIGA_IAM_ENCRYPTION_KEY", "AURIGA_WORKSPACE_ENCRYPTION_KEY"} {
			value := os.Getenv(key)
			if value == "" {
				secret := make([]byte, 32)
				if _, err := rand.Read(secret); err != nil {
					return fmt.Errorf("generate compose key")
				}
				value = base64.StdEncoding.EncodeToString(secret)
				clear(secret)
			}
			fmt.Fprintf(&b, "%s=%s\n", key, value)
		}
		raw = []byte(b.String())
		if err := validateComposeKeys(raw); err != nil {
			return err
		}
		if err := p.Create("compose.keys", raw); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read compose keys")
	}
	defer clear(raw)
	if err := validateComposeKeys(raw); err != nil {
		return err
	}
	return p.Validate()
}

func validateComposeKeys(raw []byte) error {
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 3 || lines[2] != "" {
		return fmt.Errorf("invalid compose keys")
	}
	for i, key := range []string{"AURIGA_IAM_ENCRYPTION_KEY", "AURIGA_WORKSPACE_ENCRYPTION_KEY"} {
		value, ok := strings.CutPrefix(lines[i], key+"=")
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		clear(decoded)
		if !ok || err != nil || len(value) != 44 || len(decoded) != 32 {
			return fmt.Errorf("invalid compose keys")
		}
		if supplied := os.Getenv(key); supplied != "" && supplied != value {
			return fmt.Errorf("compose key input differs from existing installation")
		}
	}
	return nil
}

func composeMaterialSet(out string, requireTemplate bool) (materials.MaterialSet, error) {
	set := materials.MaterialSet{PublicFiles: append([]string{}, identityMaterialFiles...), PrivateFiles: []string{"edge.secret", "internal.properties", "compose.keys"}, ManagedDirectories: []string{"pki", "templates"}}
	entries, err := os.ReadDir(filepath.Join(out, "templates"))
	if err != nil {
		return set, err
	}
	if len(entries) == 0 && !requireTemplate {
		return set, nil
	}
	if len(entries) != 1 || (entries[0].Name() != "fake-dsh.pb" && entries[0].Name() != "real-dsh.pb") {
		return set, fmt.Errorf("incomplete fixture template")
	}
	set.PublicFiles = append(set.PublicFiles, "templates/"+entries[0].Name())
	return set, nil
}
func writeIdentityManifest(out, inputDigest string) error {
	set, err := composeMaterialSet(out, true)
	if err != nil {
		return err
	}
	return materials.SealReceipt(out, inputDigest, set)
}
func existingIdentityMaterials(out, inputDigest string) (ed25519.PublicKey, string, bool, error) {
	set, err := composeMaterialSet(out, false)
	if err != nil {
		return nil, "", false, err
	}
	reuse, err := materials.CheckReceipt(out, inputDigest, set)
	if err != nil || !reuse {
		return nil, "", false, err
	}
	return readIdentityPublic(out)
}

func readIdentityPublic(out string) (ed25519.PublicKey, string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(out, "identity-keys.json"))
	var keys struct{ Keys []struct{ X string } }
	if err != nil || json.Unmarshal(raw, &keys) != nil || len(keys.Keys) != 1 {
		return nil, "", false, fmt.Errorf("invalid identity public key")
	}
	pub, err := base64.RawURLEncoding.Strict().DecodeString(keys.Keys[0].X)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, "", false, fmt.Errorf("invalid identity public key")
	}
	fp, err := os.ReadFile(filepath.Join(out, "ca-sha256.txt"))
	if err != nil {
		return nil, "", false, fmt.Errorf("invalid CA fingerprint")
	}
	return pub, strings.TrimSpace(string(fp)), true, nil
}

// loadComposeAccess validates the original bytes before decoding or writing any
// installation material. The production loader remains the only JSON parser.
func loadComposeAccess(path, edgeContainer string) ([]byte, deploymentconfig.Config, error) {
	if path == "" {
		raw, cfg, err := marshalAccess(composeAccess(edgeContainer))
		if err == nil {
			err = validateComposeAccess(cfg, edgeContainer)
		}
		return raw, cfg, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("open access file")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("read access file")
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("compose access: %w", err)
	}
	if err := validateComposeAccess(cfg, edgeContainer); err != nil {
		return nil, deploymentconfig.Config{}, err
	}
	return raw, cfg, nil
}

// Schema validity is necessary but does not claim unsupported Compose wiring.
func validateComposeAccess(cfg deploymentconfig.Config, edgeContainer string) error {
	if cfg.TenantID() != composeTenant {
		return fmt.Errorf("compose access: tenantId must match the combined dev seed")
	}
	if cfg.InstanceControl().EdgeContainer != edgeContainer {
		return fmt.Errorf("compose access: edgeContainer must match -edge-container and Compose project")
	}
	project, suffix := strings.CutSuffix(edgeContainer, "-edge")
	if !suffix || project == "" || strings.Trim(project, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" || strings.ContainsAny(project[:1], "_-") {
		return fmt.Errorf("compose access: edgeContainer must be a lowercase Compose project followed by -edge")
	}
	if cfg.PortalOrigin().Port == cfg.AdminOrigin().Port {
		return fmt.Errorf("compose access: this recipe requires distinct external portal and admin ports")
	}
	if cfg.IngressKind() != "direct" || cfg.PortalOrigin().Scheme != "http" || cfg.AdminOrigin().Scheme != "http" || cfg.InstanceControl().Origin.Scheme != "http" {
		return fmt.Errorf("compose access: only direct HTTP browser and instance-control ingress is supported")
	}
	ls := cfg.Listeners()
	for _, listener := range []deployment.Listener{ls.Portal, ls.Admin, ls.InstanceControl} {
		if listener.Address != "0.0.0.0" || listener.Tls {
			return fmt.Errorf("compose access: container listeners require 0.0.0.0 without TLS")
		}
	}
	for _, origin := range []deployment.Origin{cfg.PortalOrigin(), cfg.AdminOrigin()} {
		for _, reserved := range []int32{15432, 4222, 8222, 5000, iamPort, observerPort, 7443, iamRPCPort} {
			if origin.Port == reserved {
				return fmt.Errorf("compose access: browser port conflicts with a fixed combined host service")
			}
		}
	}
	return nil
}

func composeAccessEnv(cfg deploymentconfig.Config, supplied bool) []byte {
	// No shell syntax can enter these values: all hosts/ports were validated by
	// deploymentconfig.Load. Custom domain installs publish on all interfaces;
	// custom IP installs publish only the declared IPv4. Default M2 stays local.
	bind := "127.0.0.1"
	if supplied {
		bind = "0.0.0.0"
		if cfg.Mode() == "ip" {
			bind = cfg.PortalOrigin().Host
		}
	}
	return []byte(fmt.Sprintf("COMPOSE_PROJECT_NAME='%s'\nAURIGA_IAM_ISSUER='%s'\nAURIGA_TEAM_ORIGIN='%s'\nAURIGA_TEAM_RP_ID='%s'\nAURIGA_ACCESS_BIND='%s'\nAURIGA_PORTAL_PORT='%d'\nAURIGA_ADMIN_PORT='%d'\nAURIGA_PORTAL_TARGET_PORT='%d'\nAURIGA_ADMIN_TARGET_PORT='%d'\n",
		strings.TrimSuffix(cfg.InstanceControl().EdgeContainer, "-edge"), cfg.Issuer(), cfg.PortalURL(), cfg.PortalOrigin().Host, bind,
		cfg.PortalOrigin().Port, cfg.AdminOrigin().Port, cfg.Listeners().Portal.Port, cfg.Listeners().Admin.Port))
}

// These ports belong to the disposable public M2 stack, not product defaults.
const (
	composeTenant  = "00000000-0000-0000-0000-000000000001"
	portalHost     = "portal.example.com"
	adminHost      = "admin.example.com"
	instanceParent = "i.example.com"
	controlAlias   = "auriga-instance.internal"
	portalPort     = 8080
	adminPort      = 8084
	observerPort   = 8082
	controlPort    = 8083
	iamPort        = 8081
	iamServerName  = "platform"
	edgeHopDNSName = "edge.auriga.internal"
)

func composeAccess(edgeContainer string) deployment.Access {
	portal := deployment.Origin{Scheme: "http", Host: portalHost, Port: portalPort}
	return deployment.Access{
		SchemaVersion: 1, Mode: "domain", TenantId: composeTenant,
		PortalOrigin: portal,
		AdminOrigin:  deployment.Origin{Scheme: "http", Host: adminHost, Port: adminPort},
		IssuerOrigin: portal, InsecureHttpAcknowledged: true,
		Listeners: deployment.AccessListeners{
			Portal:          deployment.Listener{Address: "0.0.0.0", Port: portalPort, Tls: false},
			Admin:           deployment.Listener{Address: "0.0.0.0", Port: adminPort, Tls: false},
			InstanceControl: deployment.Listener{Address: "0.0.0.0", Port: controlPort, Tls: false},
		},
		Ingress: deployment.AccessIngressDirectAsAccessIngress(&deployment.AccessIngressDirect{Kind: "direct"}),
		Instances: deployment.AccessInstancesSubdomainAsAccessInstances(&deployment.AccessInstancesSubdomain{
			Mode: "subdomain", ParentDomain: instanceParent, Scheme: "http", Port: portalPort,
		}),
		InstanceControl: deployment.AccessInstanceControl{
			DnsAlias:      controlAlias,
			Origin:        deployment.Origin{Scheme: "http", Host: controlAlias, Port: controlPort},
			EdgeContainer: edgeContainer,
		},
	}
}

// Validate the exact serialized generated model through the production loader.
func marshalAccess(access deployment.Access) ([]byte, deploymentconfig.Config, error) {
	raw, err := json.MarshalIndent(access, "", "  ")
	if err != nil {
		return nil, deploymentconfig.Config{}, err
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("compose access: %w", err)
	}
	return append(raw, '\n'), cfg, nil
}

func iamHealthcheck() string {
	// The same generated script runs in the platform container and the CI checkout.
	return fmt.Sprintf(`#!/bin/sh
set -eu
material_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "${AURIGA_IAM_EDGE_TOKEN_EXPORT_ENABLED:-false}" = true ]; then
  test -s "$AURIGA_IAM_EDGE_TOKEN_FILE"
fi
exec curl --fail --silent --show-error --noproxy '*' \
  --cacert "$material_dir/pki/ca.crt" \
  --cert "$material_dir/pki/edge-iam.crt" \
  --key "$material_dir/pki/edge-iam.key" \
  --resolve '%s:%d:127.0.0.1' 'https://%s:%d/actuator/health'
`, iamServerName, iamPort, iamServerName, iamPort)
}
