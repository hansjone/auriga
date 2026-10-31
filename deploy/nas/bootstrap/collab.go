package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// Only the NAS projection differs: the offline approval and Java publication
// authority are the same as compose. No online root is generated here.
func writeNASCollab(out, base string, c *collab.Input, cfg deploymentconfig.Config, publicOrigin string) error {
	if err := c.CheckState(); err != nil {
		return err
	}
	for _, name := range []string{"collab-installation", "pki", "network", "network-platform"} {
		if err := os.Mkdir(filepath.Join(out, name), 0700); err != nil {
			return err
		}
	}
	caPEM, err := os.ReadFile(filepath.Join(base, "pki", "ca.crt"))
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(base, "pki", "ca.key"))
	if err != nil {
		return err
	}
	defer clear(keyPEM)
	certBlock, _ := pem.Decode(caPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return fmt.Errorf("NAS collab CA refused")
	}
	ca, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return err
	}
	private, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return err
	}
	key, ok := private.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("NAS collab CA key refused")
	}
	c.Files["ca.crt"] = caPEM
	c.Files["server.crt"], c.Files["server.key"] = mustIssue(key, ca, "collab", "auriga://collab/nas", []string{"collab.network.internal", "collab"}, []net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	c.Files["client.crt"], c.Files["client.key"] = mustIssue(key, ca, "collab-iam", "auriga://collab/nas", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	edgeCert, edgeKey := mustIssue(key, ca, "edge-collab", "auriga://edge/collab-nas", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	for name, raw := range map[string][]byte{"ca.crt": caPEM, "edge-collab.crt": edgeCert, "edge-collab.key": edgeKey} {
		writeFile(filepath.Join(out, "pki", name), raw, 0600)
	}
	// Kept in the fixed shared leaf set; this is JWKS TLS trust, never a network root.
	c.Files["browser.crt"] = caPEM
	secret := func() (string, error) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		defer clear(b)
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	ownerPass, err := secret()
	if err != nil {
		return err
	}
	appPass, err := secret()
	if err != nil {
		return err
	}
	var p strings.Builder
	p.WriteString("auriga.platform.combined=false\nauriga.service.name=collab\nauriga.service.schema=collab\n")
	for _, service := range []string{"iam", "org", "workspace", "resource", "knowledge", "audit", "ops"} {
		fmt.Fprintf(&p, "auriga.services.%s.enabled=false\n", service)
	}
	fmt.Fprintf(&p, "auriga.services.collab.enabled=true\nspring.datasource.url=jdbc:postgresql://postgres:5432/auriga\nspring.datasource.username=auriga_collab_app\nspring.datasource.password=%s\nspring.flyway.user=auriga_collab_owner\nspring.flyway.password=%s\nspring.flyway.schemas=collab\nspring.flyway.locations=classpath:db/collab\n", appPass, ownerPass)
	fmt.Fprintf(&p, "auriga.security.jwt.issuer=%s\nauriga.security.jwt.jwk-set-uri=https://platform:8081/oauth2/jwks\n", cfg.Issuer())
	p.WriteString("server.address=0.0.0.0\nserver.port=8085\nserver.ssl.enabled=true\nserver.ssl.certificate=/etc/auriga/collab/server.crt\nserver.ssl.certificate-private-key=/etc/auriga/collab/server.key\nserver.ssl.trust-certificate=/etc/auriga/collab/ca.crt\nserver.ssl.client-auth=need\nauriga.grpc.server.enabled=true\nauriga.grpc.server.host=0.0.0.0\nauriga.grpc.server.port=9092\nauriga.grpc.server.tls-cert-chain=/etc/auriga/collab/server.crt\nauriga.grpc.server.tls-private-key=/etc/auriga/collab/server.key\nauriga.grpc.server.tls-trust-cert-collection=/etc/auriga/collab/ca.crt\nauriga.grpc.server.tenant-delegates[0]=svc:ai-gateway\n")
	for _, prefix := range []string{"auriga.collab.network.", "auriga.collab.control-plane."} {
		fmt.Fprintf(&p, "%siam-target=platform:9091\n%sservice-token-file=/run/auriga-collab-token/collab/service.jwt\n%stls-cert-chain=/etc/auriga/collab/client.crt\n%stls-private-key=/etc/auriga/collab/client.key\n%stls-trust-cert-collection=/etc/auriga/collab/ca.crt\n", prefix, prefix, prefix, prefix, prefix)
	}
	p.WriteString("auriga.collab.control-plane.enabled=true\nauriga.collab.control-plane.org-target=platform:9091\nauriga.collab.network.key-state-file=/var/lib/auriga/collab/state.json\nauriga.collab.network.key-operations.enabled=true\n")
	for name, value := range map[string]string{"tenant-id": c.TenantID, "realm": c.Realm, "root-kid": c.RootKid, "root-fingerprint": c.RootFingerprint, "minimum-version": c.MinimumVersion, "root-public-file": "/etc/auriga/collab/root.der", "signed-file": "/etc/auriga/collab/signed.json", "announcement-file": "/etc/auriga/collab/announcement.json"} {
		fmt.Fprintf(&p, "auriga.collab.network.key-operations.%s=%s\n", name, value)
	}
	var signed struct {
		Keyset struct {
			Keys []struct{ Kid, Purpose string }
		}
	}
	if err := json.Unmarshal(c.Files["signed.json"], &signed); err != nil {
		return err
	}
	for _, k := range signed.Keyset.Keys {
		name := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}[k.Purpose]
		fmt.Fprintf(&p, "auriga.collab.network.key-operations.private-files[%s]=/etc/auriga/collab/%s\n", k.Kid, name)
	}
	c.Files["collab.properties"] = []byte(p.String())
	for _, name := range collab.Leaves {
		writeFile(filepath.Join(out, "collab-installation", name), c.Files[name], 0600)
	}
	// The per-installation source receipt binds these credentials. Re-rendering
	// reuses them; the SQL refuses roles with elevated attributes or memberships.
	sql := fmt.Sprintf(`DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='auriga_collab_owner') THEN
  CREATE ROLE auriga_collab_owner LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='auriga_collab_app') THEN
  CREATE ROLE auriga_collab_app LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname IN ('auriga_collab_owner','auriga_collab_app') AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit OR rolbypassrls OR NOT rolcanlogin))
 OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member IN (SELECT oid FROM pg_roles WHERE rolname IN ('auriga_collab_owner','auriga_collab_app'))) THEN
  RAISE EXCEPTION 'collab role attributes or memberships refused';
 END IF;
END $$;
CREATE SCHEMA IF NOT EXISTS collab AUTHORIZATION auriga_collab_owner;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='collab' AND nspowner=(SELECT oid FROM pg_roles WHERE rolname='auriga_collab_owner')) THEN
  RAISE EXCEPTION 'collab schema owner refused';
 END IF;
END $$;
GRANT USAGE ON SCHEMA collab TO auriga_collab_app;
ALTER ROLE auriga_collab_owner SET search_path TO collab;
ALTER ROLE auriga_collab_app SET search_path TO collab;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_collab_owner IN SCHEMA collab GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO auriga_collab_app;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_collab_owner IN SCHEMA collab GRANT USAGE,SELECT ON SEQUENCES TO auriga_collab_app;
`, ownerPass, appPass)
	writeFile(filepath.Join(out, "roles.sql"), []byte(sql), 0600)
	gateway, err := os.ReadFile(filepath.Join(base, "network-platform", "gateway.secret"))
	if err != nil {
		return err
	}
	defer clear(gateway)
	collabSecret, err := secret()
	if err != nil {
		return err
	}
	writeFile(filepath.Join(out, "network-platform", "gateway.secret"), gateway, 0600)
	writeFile(filepath.Join(out, "network-platform", "collab.secret"), []byte(collabSecret), 0600)
	// Preserve the base network settings and add the independent collab exporter.
	platform, err := os.ReadFile(filepath.Join(base, "network-platform", "platform.properties"))
	if err != nil {
		return err
	}
	var team strings.Builder
	team.Write(platform)
	team.WriteString("\nauriga.workspace.managed-network-installation-file=/etc/auriga/network/managed-network.json\nauriga.platform.team-token-export.enabled=true\nauriga.iam.ai-gateway-token-export.enabled=false\n")
	for _, svc := range []string{"ai-gateway", "collab"} {
		s, leaf, token, gid := gateway, "gateway.secret", "/run/auriga-network-token/gateway/service.jwt", 65533
		if svc == "collab" {
			s, leaf, token, gid = []byte(collabSecret), "collab.secret", "/run/auriga-collab-token/collab/service.jwt", 65534
		}
		hash := sha256.Sum256(s)
		fmt.Fprintf(&team, "auriga.iam.internal-secret-hashes.%s=%x\nauriga.platform.team-token-export.%s.installation-secret-file=/etc/auriga/network/%s\nauriga.platform.team-token-export.%s.token-file=%s\nauriga.platform.team-token-export.%s.reader-group=%d\nauriga.platform.team-token-export.%s.installation-owner=10001\n", svc, hash, svc, leaf, svc, token, svc, gid, svc)
	}
	writeFile(filepath.Join(out, "network-platform", "platform.properties"), []byte(team.String()), 0600)
	writeJSON(filepath.Join(out, "network-platform", "managed-network.json"), map[string]any{"platformOrigin": publicOrigin, "issuer": publicOrigin, "network": map[string]any{"realm": c.Realm, "wssUrl": strings.Replace(publicOrigin, "https://", "wss://", 1) + "/net/v1/ws", "roots": []map[string]string{{"kid": c.RootKid, "public_key_spki": base64.RawURLEncoding.EncodeToString(c.Files["root.der"])}}}}, 0600)
	var runtime map[string]any
	b, err := os.ReadFile(filepath.Join(base, "network", "runtime.json"))
	if err != nil || json.Unmarshal(b, &runtime) != nil {
		return fmt.Errorf("NAS gateway source refused")
	}
	rpc, ok := runtime["collab"].(map[string]any)
	if !ok {
		return fmt.Errorf("NAS collab RPC refused")
	}
	rpc["Target"] = "collab:9092"
	rpc["ServerName"] = "collab.network.internal"
	runtime["protocolSigning"] = c.Protocol
	writeJSON(filepath.Join(out, "network", "runtime.json"), runtime, 0600)
	writeFile(filepath.Join(out, "network", "protocol.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: c.Files["protocol.der"]}), 0600)
	var edge map[string]any
	b, err = os.ReadFile(filepath.Join(base, "edge.config.json"))
	if err != nil || json.Unmarshal(b, &edge) != nil {
		return fmt.Errorf("NAS edge source refused")
	}
	upstreams, ok := edge["upstreams"].(map[string]any)
	if !ok {
		return fmt.Errorf("NAS edge upstreams refused")
	}
	upstreams["collab"] = "https://collab:8085"
	if publicOrigin != cfg.PortalURL() {
		wss, ok := edge["networkWss"].(map[string]any)
		if !ok {
			return fmt.Errorf("NAS WSS source refused")
		}
		wss["allowedOrigins"] = []string{cfg.PortalURL(), publicOrigin}
	}
	edge["collabHop"] = map[string]string{"certFile": "/etc/auriga/collab-edge/edge-collab.crt", "keyFile": "/etc/auriga/collab-edge/edge-collab.key", "caFile": "/etc/auriga/collab-edge/ca.crt", "serverName": "collab.network.internal"}
	writeJSON(filepath.Join(out, "edge.config.json"), edge, 0600)
	return nil
}

// An HTTP access baseline may be presented through an explicitly approved
// HTTPS reverse proxy. The chosen network URL must also be in NAS's existing
// installation-owned link-origin allowlist; it cannot come from request input.
func nasCollabOrigin(cfg deploymentconfig.Config, requested, allowed string) (string, error) {
	if requested == "" {
		requested = cfg.PortalURL()
	}
	u, err := url.Parse(requested)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != requested || u.Host != strings.ToLower(u.Host) || strings.ContainsAny(requested, "\\\r\n\t ") {
		return "", fmt.Errorf("collab requires an approved canonical HTTPS presentation origin")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("collab presentation port refused")
		}
	}
	if requested == cfg.AdminURL() || u.Hostname() == cfg.InstanceControl().DnsAlias {
		return "", fmt.Errorf("collab presentation origin is not a portal")
	}
	if parent := cfg.InstanceParentDomain(); parent != "" && (u.Hostname() == parent || strings.HasSuffix(u.Hostname(), "."+parent)) {
		return "", fmt.Errorf("instance origins cannot present collab")
	}
	if requested != cfg.PortalURL() {
		found := false
		for _, item := range strings.Split(allowed, ",") {
			if item == requested {
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("collab presentation origin must be in AURIGA_LINKS_ALLOWED_ORIGINS")
		}
	}
	return requested, nil
}
