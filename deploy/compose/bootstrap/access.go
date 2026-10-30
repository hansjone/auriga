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
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	"github.com/hansjone/auriga/deploy/bootstrap/relayui"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var identityMaterialFiles = []string{
	"pki/ca.crt", "pki/ca.key", "pki/workspace.crt", "pki/workspace.key",
	"pki/edge.crt", "pki/edge.key", "pki/iam.crt", "pki/iam.key", "pki/edge-iam.crt", "pki/edge-iam.key",
	"identity-signing-keys.json", "identity-keys.json", "ca-sha256.txt",
	"access.json", "iam-healthcheck.sh", "iam-client.json", "heartbeat-observer.json",
	"edge.config.json", "access.env", "agent-policy.yaml",
	"runtime-keys/root.pb",
	"pki/gateway.crt", "pki/gateway.key",
	"litellm-runtime.json", "litellm-public.env",
}

func init() {
	for _, name := range networkdeploy.Files {
		identityMaterialFiles = append(identityMaterialFiles, "network/"+name)
	}
	for _, name := range networkdeploy.CredentialFiles {
		identityMaterialFiles = append(identityMaterialFiles, "network-platform/"+name)
	}
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
	set := materials.MaterialSet{PublicFiles: append([]string{}, identityMaterialFiles...), PrivateFiles: []string{"edge.secret", "internal.properties", "compose.keys"}, ManagedDirectories: []string{"pki", "templates", "runtime-keys", "network", "network-platform", "browser-tls"}}
	access, err := os.ReadFile(filepath.Join(out, "access.json"))
	if err != nil {
		return set, fmt.Errorf("read managed access")
	}
	cfg, err := deploymentconfig.Load(access)
	if err != nil {
		return set, err
	}
	if cfg.PortalOrigin().Scheme == "https" {
		set.PublicFiles = append(set.PublicFiles, "browser-tls/browser.crt", "browser-tls/browser.key")
	}
	if _, err := os.Lstat(filepath.Join(out, "collab-installation")); err == nil {
		if !validateCollabSource(out) {
			return set, fmt.Errorf("invalid managed collab source metadata")
		}
		set.ManagedDirectories = append(set.ManagedDirectories, "collab-installation")
		for _, name := range collabLeaves {
			set.PublicFiles = append(set.PublicFiles, "collab-installation/"+name)
		}
		set.PublicFiles = append(set.PublicFiles, "network-platform/collab.secret", "pki/edge-collab.crt", "pki/edge-collab.key")
	} else if !os.IsNotExist(err) {
		return set, err
	}
	if err := networkdeploy.ValidateBrowserSource(out, cfg.PortalOrigin().Scheme == "https"); err != nil {
		return set, err
	}
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
	raw, err := readProofFile(filepath.Join(out, "templates", entries[0].Name()))
	if err != nil {
		return set, err
	}
	var spec nodev1.InstanceSpec
	if proto.Unmarshal(raw, &spec) != nil || !fixtureImagePattern.MatchString(spec.GetDsh().GetImage()) {
		return set, fmt.Errorf("invalid runtime proof locator")
	}
	leaf := strings.TrimPrefix(digestOf(spec.GetDsh().GetImage()), "sha256:")
	proofRoot := filepath.Join(out, "runtime-proofs")
	proofEntries, err := os.ReadDir(proofRoot)
	if err != nil || len(proofEntries) != 1 || proofEntries[0].Name() != leaf {
		return set, fmt.Errorf("invalid runtime proof root")
	}
	files, err := os.ReadDir(filepath.Join(proofRoot, leaf))
	if err != nil || len(files) != 2 || files[0].Name() != "config.json" || files[1].Name() != "manifest.json" {
		return set, fmt.Errorf("incomplete runtime proof files")
	}
	set.ManagedDirectories = append(set.ManagedDirectories, "runtime-proofs")
	set.PublicFileLimits = map[string]int64{}
	for _, name := range []string{"manifest.json", "config.json"} {
		path := "runtime-proofs/" + leaf + "/" + name
		set.PublicFiles = append(set.PublicFiles, path)
		set.PublicFileLimits[path] = maxProofBytes
	}
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
	if err := validateRuntimeTemplate(out); err != nil {
		return nil, "", false, err
	}
	if err := validateRuntimeRoot(out); err != nil {
		return nil, "", false, err
	}
	return readIdentityPublic(out)
}

var runtimeRootExpiry = time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC)

// An old installation never receives a new root directory as an implicit repair.
func prepareRuntimeRootDirectory(out string) error {
	path := filepath.Join(out, "runtime-keys")
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range append(append([]string{}, identityMaterialFiles...), "iam-installation/bootstrap.receipt", "iam-installation/edge.secret", "iam-installation/internal.properties", "iam-installation/compose.keys") {
		if _, err := os.Lstat(filepath.Join(out, filepath.FromSlash(name))); err == nil {
			return fmt.Errorf("existing installation lacks runtime root material; use a fresh installation directory")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.Mkdir(path, 0o755)
}

func generateRuntimeRootSigner(out string) (ed25519.PrivateKey, error) {
	if !time.Now().Before(runtimeRootExpiry) {
		return nil, fmt.Errorf("M2 runtime root fixture expiry reached; update fixture explicitly")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		clear(priv)
		return nil, err
	}
	k := &nodev1.ArtifactSigningKey{Kid: "e2e-runtime-root", PublicKey: pub, NotAfter: timestamppb.New(runtimeRootExpiry)}
	raw, err := proto.Marshal(k)
	if err != nil {
		clear(priv)
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(out, "runtime-keys", "root.pb"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		clear(priv)
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(raw); err != nil {
		clear(priv)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		clear(priv)
		return nil, err
	}
	return priv, nil
}

func validateRuntimeRoot(out string) error {
	entries, err := os.ReadDir(filepath.Join(out, "runtime-keys"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "root.pb" {
		return fmt.Errorf("invalid M2 runtime root directory")
	}
	raw, err := os.ReadFile(filepath.Join(out, "runtime-keys", "root.pb"))
	if err != nil {
		return err
	}
	var k nodev1.ArtifactSigningKey
	if proto.Unmarshal(raw, &k) != nil || len(k.ProtoReflect().GetUnknown()) != 0 || k.Kid != "e2e-runtime-root" || len(k.PublicKey) != ed25519.PublicKeySize || k.NotAfter == nil || len(k.NotAfter.ProtoReflect().GetUnknown()) != 0 || k.NotAfter.CheckValid() != nil || !k.NotAfter.AsTime().Equal(runtimeRootExpiry) || !time.Now().Before(k.NotAfter.AsTime()) || k.EndorsedByKid != "" || len(k.Endorsement) != 0 {
		return fmt.Errorf("invalid or expired M2 runtime root; use a fresh installation after explicit fixture update")
	}
	return nil
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
	if cfg.IngressKind() != "direct" || cfg.PortalOrigin().Scheme != cfg.AdminOrigin().Scheme || cfg.InstanceControl().Origin.Scheme != "http" || cfg.Listeners().InstanceControl.Tls {
		return fmt.Errorf("compose access: direct browser ingress and isolated HTTP instance-control required")
	}
	ls := cfg.Listeners()
	for _, listener := range []deployment.Listener{ls.Portal, ls.Admin, ls.InstanceControl} {
		if listener.Address != "0.0.0.0" {
			return fmt.Errorf("compose access: container listeners require 0.0.0.0")
		}
	}
	for _, origin := range []deployment.Origin{cfg.PortalOrigin(), cfg.AdminOrigin()} {
		for _, reserved := range []int32{15432, 4222, 8222, 5000, iamPort, observerPort, 7443, iamRPCPort, 8085, 9092} {
			if origin.Port == reserved {
				return fmt.Errorf("compose access: browser port conflicts with a fixed combined host service")
			}
		}
	}
	return nil
}

func composeAccessEnv(cfg deploymentconfig.Config, supplied bool, uiConfig ...relayui.Config) []byte {
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
	ui, err := relayui.Resolve(cfg, "")
	if len(uiConfig) > 0 {
		ui = uiConfig[0]
	} else if err != nil {
		panic(err) // All callers must supply an already validated installation.
	}
	return []byte(fmt.Sprintf("COMPOSE_PROJECT_NAME='%s'\nAURIGA_IAM_ISSUER='%s'\nAURIGA_TEAM_ORIGIN='%s'\nAURIGA_TEAM_RP_ID='%s'\nAURIGA_ACCESS_BIND='%s'\nAURIGA_PORTAL_PORT='%d'\nAURIGA_ADMIN_PORT='%d'\nAURIGA_PORTAL_TARGET_PORT='%d'\nAURIGA_ADMIN_TARGET_PORT='%d'\nAURIGA_LITELLM_UI_PORT='%d'\nAURIGA_LITELLM_UI_TARGET_PORT='%d'\n",
		strings.TrimSuffix(cfg.InstanceControl().EdgeContainer, "-edge"), cfg.Issuer(), cfg.PortalURL(), cfg.PortalOrigin().Host, bind,
		cfg.PortalOrigin().Port, cfg.AdminOrigin().Port, cfg.Listeners().Portal.Port, cfg.Listeners().Admin.Port, ui.Origin.Port, ui.Listener.Port))
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
