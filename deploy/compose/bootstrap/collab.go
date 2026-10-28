package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// Only installation projection: Java RootKeyPublication/SigningKeyLoader remain
// authoritative for signatures, schema, state and PostgreSQL anti-rollback.
type collabInput struct {
	TenantID         string            `json:"tenantId"`
	Realm            string            `json:"realm"`
	RootKid          string            `json:"rootKid"`
	RootFingerprint  string            `json:"rootFingerprint"`
	MinimumVersion   string            `json:"minimumVersion"`
	RootPublicFile   string            `json:"rootPublicFile"`
	SignedFile       string            `json:"signedFile"`
	AnnouncementFile string            `json:"announcementFile"`
	StateFile        string            `json:"stateFile"`
	PrivateFiles     map[string]string `json:"privateFiles"`
	files            map[string][]byte
	protocol         map[string]string
}

var collabLeaves = []string{"root.der", "signed.json", "announcement.json", "state.json", "business.der", "protocol.der", "management.der", "collab.properties", "browser.crt", "ca.crt", "server.crt", "server.key", "client.crt", "client.key"}
var collabKid = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func (c *collabInput) clear() {
	if c != nil {
		for _, raw := range c.files {
			clear(raw)
		}
	}
}
func (c *collabInput) digest() string {
	if c == nil {
		return "disabled"
	}
	h := sha256.New()
	raw, _ := json.Marshal(c)
	_, _ = h.Write(raw)
	for _, name := range collabLeaves {
		_, _ = h.Write(c.files[name])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func readCollabInput(path string, access deploymentconfig.Config) (*collabInput, error) {
	if path == "" {
		return nil, nil
	}
	invalid := fmt.Errorf("invalid explicit collab installation input")
	if access.PortalOrigin().Scheme != "https" {
		return nil, invalid
	}
	raw, err := collabRead(path, 65536, false)
	if err != nil {
		return nil, invalid
	}
	c := &collabInput{files: map[string][]byte{}}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(c) != nil || decoder.Decode(new(any)) != io.EOF || c.TenantID != access.TenantID() || !collabKid.MatchString(c.RootKid) {
		return nil, invalid
	}
	realm, e := strconv.ParseUint(c.Realm, 10, 64)
	minimum, e2 := strconv.ParseInt(c.MinimumVersion, 10, 64)
	if e != nil || realm == 0 || strconv.FormatUint(realm, 10) != c.Realm || e2 != nil || minimum < 1 || strconv.FormatInt(minimum, 10) != c.MinimumVersion {
		return nil, invalid
	}
	read := func(name, source string) error {
		if source == "" {
			return invalid
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(filepath.Dir(path), source)
		}
		secret := name == "business.der" || name == "protocol.der" || name == "management.der"
		b, err := collabRead(source, 65536, secret)
		if err != nil {
			return invalid
		}
		c.files[name] = b
		return nil
	}
	for name, source := range map[string]string{"root.der": c.RootPublicFile, "signed.json": c.SignedFile, "announcement.json": c.AnnouncementFile, "state.json": c.StateFile} {
		if read(name, source) != nil {
			c.clear()
			return nil, invalid
		}
	}
	pub, err := x509.ParsePKIXPublicKey(c.files["root.der"])
	sum := sha256.Sum256(c.files["root.der"])
	if _, ok := pub.(ed25519.PublicKey); err != nil || !ok || c.RootFingerprint != fmt.Sprintf("sha256:%x", sum) {
		c.clear()
		return nil, invalid
	}
	var signed struct {
		Keyset struct {
			Realm   string `json:"realm"`
			Version string `json:"keyset_version"`
			Issued  string `json:"issued_at"`
			Until   string `json:"valid_until"`
			Keys    []struct {
				Kid     string `json:"kid"`
				Purpose string `json:"purpose"`
				Public  string `json:"public_key_spki"`
				Before  string `json:"not_before"`
				After   string `json:"not_after"`
				State   string `json:"state"`
			} `json:"keys"`
		} `json:"keyset"`
	}
	if json.Unmarshal(c.files["signed.json"], &signed) != nil {
		c.clear()
		return nil, invalid
	}
	p := signed.Keyset
	version, e := strconv.ParseInt(p.Version, 10, 64)
	issued, e2 := time.Parse(time.RFC3339, p.Issued)
	until, e3 := time.Parse(time.RFC3339, p.Until)
	now := time.Now()
	if e != nil || version < minimum || e2 != nil || e3 != nil || now.Before(issued) || !now.Before(until) || until.Sub(issued) <= 0 || until.Sub(issued) > 24*time.Hour || p.Realm != c.Realm || len(p.Keys) != 3 || len(c.PrivateFiles) != 3 {
		c.clear()
		return nil, invalid
	}
	purposes := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}
	seen := map[string]bool{}
	for _, key := range p.Keys {
		name, ok := purposes[key.Purpose]
		if !ok || seen[name] || !collabKid.MatchString(key.Kid) || key.State != "active" || read(name, c.PrivateFiles[key.Kid]) != nil {
			c.clear()
			return nil, invalid
		}
		seen[name] = true
		private, err := x509.ParsePKCS8PrivateKey(c.files[name])
		secret, ok := private.(ed25519.PrivateKey)
		before, eb := time.Parse(time.RFC3339, key.Before)
		after, ea := time.Parse(time.RFC3339, key.After)
		if err != nil || !ok || eb != nil || ea != nil || now.Before(before) || !now.Before(after) {
			c.clear()
			return nil, invalid
		}
		der, _ := x509.MarshalPKIXPublicKey(secret.Public())
		if base64.RawURLEncoding.EncodeToString(der) != key.Public || bytes.Equal(der, c.files["root.der"]) {
			c.clear()
			return nil, invalid
		}
		if key.Purpose == "protocol_response" {
			c.protocol = map[string]string{"keyId": key.Kid, "keysetVersion": p.Version, "notBefore": key.Before, "notAfter": key.After, "privateKeyFile": "/etc/auriga/network/protocol.key"}
		}
	}
	return c, nil
}

func collabRead(path string, limit int64, secret bool) ([]byte, error) {
	// Same-file checks and no symlink ancestors, before reading private inputs.
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("collab input metadata refused")
		}
		if p == filepath.Dir(p) || p == "." {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() == 0 || before.Size() > limit {
		return nil, fmt.Errorf("collab input refused")
	}
	if !collabSourceMetadata(before, secret) {
		return nil, fmt.Errorf("collab input ownership refused")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("collab input changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	final, se := os.Lstat(path)
	if err != nil || se != nil || !os.SameFile(opened, final) || !final.ModTime().Equal(before.ModTime()) || int64(len(raw)) != before.Size() {
		clear(raw)
		return nil, fmt.Errorf("collab input changed")
	}
	return raw, nil
}

func writeCollab(out string, c *collabInput, issuer string) error {
	return writeCollabFrom(out, out, c, issuer)
}

func writeCollabFrom(out, base string, c *collabInput, issuer string) error {
	if c == nil {
		return nil
	}
	if err := checkNativeClient(out); err != nil {
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
	c.files["ca.crt"] = caPEM
	c.files["server.crt"], c.files["server.key"] = mustIssue(caKey, ca, "collab", "auriga://collab/compose", []string{"collab.network.internal"}, []net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	c.files["client.crt"], c.files["client.key"] = mustIssue(caKey, ca, "collab-iam", "auriga://collab/compose", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	edgeCert, edgeKey := mustIssue(caKey, ca, "edge-collab", "auriga://edge/collab-compose", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(out, "pki", "edge-collab.crt"), edgeCert, 0644)
	writeFile(filepath.Join(out, "pki", "edge-collab.key"), edgeKey, 0600)
	c.files["browser.crt"], err = os.ReadFile(filepath.Join(base, "browser-tls", "browser.crt"))
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
	_ = json.Unmarshal(c.files["signed.json"], &signed)
	for _, k := range signed.Keyset.Keys {
		name := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}[k.Purpose]
		fmt.Fprintf(&properties, "auriga.collab.network.key-operations.private-files[%s]=/etc/auriga/collab/%s\n", k.Kid, name)
	}
	c.files["collab.properties"] = []byte(properties.String())
	for _, name := range collabLeaves {
		writeFile(filepath.Join(dir, name), c.files[name], 0600)
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
	runtime["protocolSigning"] = c.protocol
	writeJSON(filepath.Join(out, "network", "runtime.json"), runtime, 0600)
	writeFile(filepath.Join(out, "network", "protocol.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: c.files["protocol.der"]}), 0600)
	return nil
}
