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
	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

type collabInput = collab.Input

var collabLeaves = collab.Leaves
var collabKid = collab.KidPattern

func readCollabInput(path string, access deploymentconfig.Config) (*collabInput, error) {
	if path != "" && access.PortalOrigin().Scheme != "https" {
		return nil, fmt.Errorf("invalid explicit collab installation input")
	}
	return collab.Read(path, access.TenantID())
}
func collabRead(path string, limit int64, secret bool) ([]byte, error) {
	return collab.ReadFile(path, limit, secret)
}
func writeCollab(out string, c *collabInput, issuer string) error {
	return writeCollabFrom(out, out, c, issuer)
}

func writeCollabFrom(out, base string, c *collabInput, issuer string) error {
	if c == nil {
		return nil
	}
	if err := checkNativeClient(base); err != nil {
		return err
	}
	dir := filepath.Join(out, "collab-installation")
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("collab source directory refused")
	}
	caPEM, err := os.ReadFile(filepath.Join(base, "pki", "ca.crt"))
	if err != nil {
		return err
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(base, "pki", "ca.key"))
	if err != nil {
		return err
	}
	defer clear(caKeyPEM)
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return fmt.Errorf("collab CA refused")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	block, _ = pem.Decode(caKeyPEM)
	if block == nil {
		return fmt.Errorf("collab CA key refused")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	caKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("collab CA key type refused")
	}
	c.Files["ca.crt"] = caPEM
	c.Files["server.crt"], c.Files["server.key"] = mustIssue(caKey, ca, "collab", "auriga://collab/compose", []string{"collab.network.internal"}, []net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	c.Files["client.crt"], c.Files["client.key"] = mustIssue(caKey, ca, "collab-iam", "auriga://collab/compose", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	edgeCert, edgeKey := mustIssue(caKey, ca, "edge-collab", "auriga://edge/collab-compose", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(out, "pki", "edge-collab.crt"), edgeCert, 0644)
	writeFile(filepath.Join(out, "pki", "edge-collab.key"), edgeKey, 0600)
	c.Files["browser.crt"], err = os.ReadFile(filepath.Join(base, "browser-tls", "browser.crt"))
	if err != nil {
		return err
	}
	var properties strings.Builder
	properties.WriteString("auriga.platform.combined=false\nauriga.service.name=collab\nauriga.service.schema=collab\n")
	for _, service := range []string{"iam", "org", "workspace", "resource", "knowledge", "audit", "ops"} {
		fmt.Fprintf(&properties, "auriga.services.%s.enabled=false\n", service)
	}
	properties.WriteString("auriga.services.collab.enabled=true\nspring.datasource.url=jdbc:postgresql://127.0.0.1:15432/auriga\nspring.datasource.username=auriga_collab_app\nspring.datasource.password=e2e_collab_app\nspring.flyway.user=auriga_collab_owner\nspring.flyway.password=e2e_collab_owner\nspring.flyway.schemas=collab\nspring.flyway.locations=classpath:db/collab\n")
	fmt.Fprintf(&properties, "auriga.security.jwt.issuer=%s\nauriga.security.jwt.jwk-set-uri=%s/oauth2/jwks\n", issuer, issuer)
	properties.WriteString("server.address=172.29.240.1\nserver.port=8085\nserver.ssl.enabled=true\nserver.ssl.certificate=/etc/auriga/collab/server.crt\nserver.ssl.certificate-private-key=/etc/auriga/collab/server.key\nserver.ssl.trust-certificate=/etc/auriga/collab/ca.crt\nserver.ssl.client-auth=need\nauriga.grpc.server.enabled=true\nauriga.grpc.server.host=127.0.0.1\nauriga.grpc.server.port=9092\nauriga.grpc.server.tls-cert-chain=/etc/auriga/collab/server.crt\nauriga.grpc.server.tls-private-key=/etc/auriga/collab/server.key\nauriga.grpc.server.tls-trust-cert-collection=/etc/auriga/collab/ca.crt\nauriga.grpc.server.tenant-delegates[0]=svc:ai-gateway\n") // gitleaks:allow disposable M2 network
	for _, prefix := range []string{"auriga.collab.network.", "auriga.collab.control-plane."} {
		fmt.Fprintf(&properties, "%siam-target=platform:9091\n%sservice-token-file=/run/auriga-collab-token/collab/service.jwt\n%stls-cert-chain=/etc/auriga/collab/client.crt\n%stls-private-key=/etc/auriga/collab/client.key\n%stls-trust-cert-collection=/etc/auriga/collab/ca.crt\n", prefix, prefix, prefix, prefix, prefix)
	}
	properties.WriteString("auriga.collab.control-plane.enabled=true\nauriga.collab.control-plane.org-target=platform:9091\nauriga.collab.network.key-state-file=/var/lib/auriga/collab/state.json\nauriga.collab.network.key-operations.enabled=true\n")
	for name, value := range map[string]string{"tenant-id": c.TenantID, "realm": c.Realm, "root-kid": c.RootKid, "root-fingerprint": c.RootFingerprint, "minimum-version": c.MinimumVersion, "root-public-file": "/etc/auriga/collab/root.der", "signed-file": "/etc/auriga/collab/signed.json", "announcement-file": "/etc/auriga/collab/announcement.json"} {
		fmt.Fprintf(&properties, "auriga.collab.network.key-operations.%s=%s\n", name, value)
	}
	var signed struct {
		Keyset struct {
			Keys []struct {
				Kid     string `json:"kid"`
				Purpose string `json:"purpose"`
			} `json:"keys"`
		} `json:"keyset"`
	}
	_ = json.Unmarshal(c.Files["signed.json"], &signed)
	for _, k := range signed.Keyset.Keys {
		name := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}[k.Purpose]
		fmt.Fprintf(&properties, "auriga.collab.network.key-operations.private-files[%s]=/etc/auriga/collab/%s\n", k.Kid, name)
	}
	c.Files["collab.properties"] = []byte(properties.String())
	for _, name := range collabLeaves {
		writeFile(filepath.Join(dir, name), c.Files[name], 0600)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	encoded := []byte(base64.RawURLEncoding.EncodeToString(secret))
	clear(secret)
	defer clear(encoded)
	writeFile(filepath.Join(out, "network-platform", "collab.secret"), encoded, 0600)
	gateway, err := os.ReadFile(filepath.Join(base, "network-platform", "gateway.secret"))
	if err != nil {
		return err
	}
	defer clear(gateway)
	var team strings.Builder
	team.WriteString(nativeClientProperties)
	managedURL := strings.TrimSuffix(issuer, "/")
	writeJSON(filepath.Join(out, "network-platform", "managed-network.json"), map[string]any{
		"platformOrigin": managedURL, "issuer": managedURL,
		"network": map[string]any{"realm": c.Realm,
			"wssUrl": strings.Replace(managedURL, "https://", "wss://", 1) + "/net/v1/ws",
			"roots":  []map[string]string{{"kid": c.RootKid, "public_key_spki": base64.RawURLEncoding.EncodeToString(c.Files["root.der"])}}},
	}, 0600)
	team.WriteString("auriga.workspace.managed-network-installation-file=/etc/auriga/network/managed-network.json\n")
	team.WriteString("auriga.platform.team-token-export.enabled=true\nauriga.iam.ai-gateway-token-export.enabled=false\nauriga.iam.network.server-audiences[0]=auriga-network\nauriga.iam.network.client-server-audience=auriga-network\n")
	for _, service := range []string{"ai-gateway", "collab"} {
		s, leaf, token, gid := gateway, "gateway.secret", "/run/auriga-network-token/gateway/service.jwt", 65533
		if service == "collab" {
			s, leaf, token, gid = encoded, "collab.secret", "/run/auriga-collab-token/collab/service.jwt", 65534
		}
		hash := sha256.Sum256(s)
		fmt.Fprintf(&team, "auriga.iam.internal-secret-hashes.%s=%x\nauriga.platform.team-token-export.%s.installation-secret-file=/etc/auriga/network/%s\nauriga.platform.team-token-export.%s.token-file=%s\nauriga.platform.team-token-export.%s.reader-group=%d\nauriga.platform.team-token-export.%s.installation-owner=%d\n", service, hash, service, leaf, service, token, service, gid, service, os.Getuid())
	}
	writeFile(filepath.Join(out, "network-platform", "platform.properties"), []byte(team.String()), 0600)
	var runtime map[string]any
	raw, err := os.ReadFile(filepath.Join(base, "network", "runtime.json"))
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &runtime) != nil {
		return fmt.Errorf("invalid gateway config")
	}
	rpc := runtime["collab"].(map[string]any)
	rpc["Target"] = "127.0.0.1:9092"
	rpc["ServerName"] = "collab.network.internal"
	runtime["protocolSigning"] = c.Protocol
	writeJSON(filepath.Join(out, "network", "runtime.json"), runtime, 0600)
	writeFile(filepath.Join(out, "network", "protocol.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: c.Files["protocol.der"]}), 0600)
	return nil
}
