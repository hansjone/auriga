// Bootstrap generates PKI, identity keys, InstanceSpec template, and edge config for M2 compose.
// Usage (from repo root):
//
//	go run ./deploy/compose/bootstrap -out deploy/compose/var -image 127.0.0.1:5000/auriga/fake-dsh@sha256:...
package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	out := flag.String("out", "deploy/compose/var", "output directory")
	image := flag.String("image", "", "docker image ref repo@sha256:… for fake DSH template")
	fixture := flag.String("fixture", "fake", "DSH fixture: fake or real")
	flag.Parse()
	if *fixture != "fake" && *fixture != "real" {
		fatalf("-fixture must be fake or real")
	}
	if *image == "" || !containsDigest(*image) {
		fatalf("-image must be repo@sha256:…")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("%v", err)
	}
	pki := filepath.Join(*out, "pki")
	tpl := filepath.Join(*out, "templates")
	// 0755 so edge (nonroot) can traverse the bind-mounted pki dir.
	mustMkdir(pki, 0o755)
	mustMkdir(tpl, 0o755)
	mustMkdir(filepath.Join(*out, "join"), 0o755)

	caKey, caCert, caPEM := mustCA()
	writeFile(filepath.Join(pki, "ca.crt"), caPEM, 0o644)
	writeFile(filepath.Join(pki, "ca.key"), pkcs8PEM(caKey), 0o400)

	wsCert, wsKey := mustIssue(caKey, caCert, "workspace", "auriga://workspace",
		[]string{"localhost", "platform", "platform.example.com"},
		[]net.IP{net.ParseIP("127.0.0.1")},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	// Leaf+CA chain: agent pins CA SPKI and requires the CA to appear in the peer chain.
	writeFile(filepath.Join(pki, "workspace.crt"), append(wsCert, caPEM...), 0o644)
	writeFile(filepath.Join(pki, "workspace.key"), wsKey, 0o400)

	edgeCert, edgeKey := mustIssue(caKey, caCert, "edge-compose", "auriga://edge/compose-e2e",
		nil, nil,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "edge.crt"), edgeCert, 0o644)
	writeFile(filepath.Join(pki, "edge.key"), edgeKey, 0o400)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	kid := "e2e-id-1"
	signing := map[string]any{
		"keys": []map[string]any{{
			"kty":   "OKP",
			"crv":   "Ed25519",
			"x":     base64.RawURLEncoding.EncodeToString(pub),
			"d":     base64.RawURLEncoding.EncodeToString(priv.Seed()),
			"kid":   kid,
			"alg":   "EdDSA",
			"use":   "sig",
			"state": "CURRENT",
		}},
	}
	pubOnly := map[string]any{
		"keys": []map[string]any{{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(pub),
			"kid": kid,
			"alg": "EdDSA",
			"use": "sig",
		}},
	}
	writeJSON(filepath.Join(*out, "identity-signing-keys.json"), signing, 0o600)
	writeJSON(filepath.Join(*out, "identity-keys.json"), pubOnly, 0o644)

	fp := spkiSHA256(caCert)
	writeFile(filepath.Join(*out, "ca-sha256.txt"), []byte(fp+"\n"), 0o644)

	dig := digestOf(*image)
	name, version := "fake-dsh", "0.0.0-fake"
	memoryMB := int64(512)
	if *fixture == "real" {
		name, version = "real-dsh", "0.2.0-rc.2"
		memoryMB = 1024
	}
	spec := &nodev1.InstanceSpec{
		Runtime: nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Resources: &nodev1.Resources{
			CpuMillis: 500,
			MemoryMb:  memoryMB,
			DiskGb:    1,
			Pids:      128,
		},
		Dsh: &nodev1.DshRuntime{
			Version:    version,
			ArtifactId: name,
			Digest:     dig,
			Image:      *image,
			// WorkspaceTemplates requires hasSigned + non-empty signature (e2e placeholder).
			Signed: &nodev1.SignedArtifact{
				Signature:  []byte{0x01},
				SigningKid: "e2e-artifact",
				Statement: &nodev1.ArtifactStatement{
					Purpose: "dsh_runtime",
					Name:    name,
					Version: version,
					Os:      "linux",
					Arch:    "amd64",
					Digest:  dig,
				},
			},
		},
		Profile: profileFor(*fixture),
		Secrets: &nodev1.Secrets{
			IdentityKeys: []*nodev1.IdentityPublicKey{{
				Kid:       kid,
				Alg:       "EdDSA",
				PublicKey: pub,
			}},
		},
		Labels: map[string]string{"template": name + "-e2e"},
	}
	raw, err := proto.Marshal(spec)
	if err != nil {
		fatalf("%v", err)
	}
	// Do not leave two eligible templates when switching fixture on a clean stack.
	for _, old := range []string{"fake-dsh.pb", "real-dsh.pb"} {
		if err := os.Remove(filepath.Join(tpl, old)); err != nil && !os.IsNotExist(err) {
			fatalf("remove stale template: %v", err)
		}
	}
	writeFile(filepath.Join(tpl, name+".pb"), raw, 0o644)

	edgeCfg := map[string]any{
		"listen":               "0.0.0.0:8080",
		"portalHost":           "portal.example.com",
		"adminHost":            "admin.example.com",
		"instanceParentDomain": "i.example.com",
		"secureCookies":        false,
		"allowLocalIngress":    true,
		"trustedProxies":       []string{"127.0.0.1", "::1"},
		"upstreams": map[string]string{
			"iam":       "http://127.0.0.1:8081",
			"org":       "http://127.0.0.1:8081",
			"workspace": "http://127.0.0.1:8081",
		},
		"tls":              map[string]string{"mode": "off"},
		"rateLimit":        map[string]any{"requestsPerSecond": 200, "burst": 400},
		"identityKeysFile": "/etc/auriga/identity-signing-keys.json",
		"sessionStore":     "nats",
		"natsUrl":          "nats://sessions:e2e_nats_sessions@127.0.0.1:4222",
		"nodeTLS": map[string]string{
			"certFile": "/etc/auriga/pki/edge.crt",
			"keyFile":  "/etc/auriga/pki/edge.key",
			"caFile":   "/etc/auriga/pki/ca.crt",
		},
	}
	writeJSON(filepath.Join(*out, "edge.config.json"), edgeCfg, 0o644)

	policy := map[string]any{
		"allowedImageRepos": []string{"127.0.0.1:5000/auriga/", "localhost:5000/auriga/"},
		"maxCPUMillis":      4000,
		"maxMemoryMB":       4096,
		"maxPIDs":           512,
		"maxArtifactBytes":  64 << 20,
	}
	writeYAMLish(filepath.Join(*out, "agent-policy.yaml"), policy)

	fmt.Printf("bootstrap ok out=%s ca_sha256=%s image=%s\n", *out, fp, *image)
}

// profileFor declares what the fixture build lacks. Pure DSH 0.2.0-rc.2 has no
// IM service, so the managed plugin may report activeImBots as a known 0;
// without this declaration it stays unknown and no heartbeat is sent.
func profileFor(fixture string) *nodev1.Profile {
	p := &nodev1.Profile{ListenPort: 7860}
	if fixture == "real" {
		p.Patches = []*nodev1.ConfigPatch{{Path: "plugins.auriga-dsh.imBots", ValueJson: `"none"`}}
	}
	return p
}

func containsDigest(image string) bool {
	_, dig, ok := cut(image, "@")
	return ok && len(dig) > 7 && dig[:7] == "sha256:"
}

func digestOf(image string) string {
	_, dig, _ := cut(image, "@")
	return dig
}

func cut(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func mustCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	uri, _ := url.Parse("auriga://workspace")
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "auriga-e2e-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "platform", "platform.example.com"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		fatalf("%v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		fatalf("%v", err)
	}
	return key, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func mustIssue(caKey *ecdsa.PrivateKey, ca *x509.Certificate, cn, uriSAN string, dns []string, ips []net.IP, eku []x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	uri, err := url.Parse(uriSAN)
	if err != nil {
		fatalf("%v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		fatalf("%v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  eku,
		DNSNames:     dns,
		IPAddresses:  ips,
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		fatalf("%v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pkcs8PEM(key)
}

func pkcs8PEM(key *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		fatalf("%v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func spkiSHA256(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

func writeFile(path string, data []byte, mode os.FileMode) {
	if err := os.WriteFile(path, data, mode); err != nil {
		fatalf("%v", err)
	}
	_ = os.Chmod(path, mode)
}

func writeJSON(path string, v any, mode os.FileMode) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatalf("%v", err)
	}
	writeFile(path, append(raw, '\n'), mode)
}

func writeYAMLish(path string, m map[string]any) {
	// Minimal YAML for agent-policy (avoid extra deps).
	var b []byte
	b = append(b, []byte("allowedImageRepos:\n")...)
	if prefs, ok := m["allowedImageRepos"].([]string); ok {
		for _, p := range prefs {
			b = append(b, []byte("  - "+p+"\n")...)
		}
	}
	for _, k := range []string{"maxCPUMillis", "maxMemoryMB", "maxPIDs", "maxArtifactBytes"} {
		if v, ok := m[k]; ok {
			b = append(b, []byte(fmt.Sprintf("%s: %v\n", k, v))...)
		}
	}
	writeFile(path, b, 0o644)
}

func mustMkdir(path string, mode os.FileMode) {
	if err := os.MkdirAll(path, mode); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
