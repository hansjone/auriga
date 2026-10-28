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
	"runtime"
	"strings"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

const iamRPCPort = 9091

func main() {
	out := flag.String("out", "deploy/compose/var", "output directory")
	image := flag.String("image", "", "docker image ref repo@sha256:… for fake DSH template")
	fixture := flag.String("fixture", "fake", "DSH fixture: fake or real")
	edgeContainer := flag.String("edge-container", "auriga-m2-edge", "trusted edge container identity (must match Compose project)")
	accessFile := flag.String("access-file", "", "trusted deployment access JSON (default: disposable M2 domain recipe)")
	httpsCert := flag.String("https-cert-file", "", "external direct HTTPS PEM certificate chain")
	httpsKey := flag.String("https-key-file", "", "external direct HTTPS PEM private key")
	collabFile := flag.String("network-collab-file", "", "explicit offline-approved standalone collab installation JSON")
	activate := flag.Bool("activate-collab", false, "seal first N1 activation without changing base")
	checkActivation := flag.Bool("check-collab-activation", false, "verify sealed activation and unchanged base")
	baseReceipt := flag.String("base-receipt-sha256", "", "independently checked base receipt SHA256")
	baseIdentity := flag.String("base-identity-sha256", "", "independently checked identity public file SHA256")
	stopped := flag.Bool("stopped-confirmed", false, "operator confirms platform/edge/network stopped")
	validateOnly := flag.Bool("validate-access", false, "validate access and Compose support without writing materials")
	proofs := flag.String("runtime-proofs", "", "isolated collected raw OCI proof root (first installation only)")
	observer := flag.Bool("heartbeat-observer", false, "route all combined APIs through the local authenticated heartbeat observer")
	flag.Parse()
	if *activate || *checkActivation {
		if *activate && *checkActivation {
			fatalf("choose one activation action")
		}
		if err := runActivation(*out, *collabFile, *baseReceipt, *baseIdentity, *checkActivation, *validateOnly, *stopped); err != nil {
			fatalf("%v", err)
		}
		return
	}
	accessJSON, access, err := loadComposeAccess(*accessFile, *edgeContainer)
	if err != nil {
		fatalf("%v", err)
	}
	hosts := []string{access.PortalOrigin().Host, access.AdminOrigin().Host}
	if parent := access.InstanceParentDomain(); parent != "" {
		hosts = append(hosts, "*."+parent)
	}
	browser, err := networkdeploy.ReadBrowserTLS(*httpsCert, *httpsKey, access.PortalOrigin().Scheme == "https", hosts)
	if err != nil {
		fatalf("%v", err)
	}
	defer browser.Clear()
	collab, err := readCollabInput(*collabFile, access)
	if err != nil {
		fatalf("%v", err)
	}
	defer collab.clear()
	if *validateOnly {
		return
	}
	if collab != nil && runtime.GOOS != "linux" {
		fatalf("standalone collab activation requires Linux; use -validate-access")
	}
	if *fixture != "fake" && *fixture != "real" {
		fatalf("-fixture must be fake or real")
	}
	if !fixtureImagePattern.MatchString(*image) || !strings.Contains(*image, "/"+*fixture+"-dsh@") {
		fatalf("-image must use selected exact local fixture repository@sha256:digest")
	}
	input := sha256.Sum256(append(append([]byte{}, accessJSON...), []byte(fmt.Sprintf("\n%s\n%s\n%s\n%t\n%t\n%s", composeRecipe(access.PortalOrigin().Scheme == "https"), *image, *fixture, *observer, *accessFile != "", browser.Digest()))...))
	inputDigest := hex.EncodeToString(input[:])
	if collab != nil {
		combinedInput := sha256.Sum256(append(input[:], []byte(collab.digest())...))
		inputDigest = hex.EncodeToString(combinedInput[:])
	}
	existing, err := preflightRuntimeInstallation(*out)
	if err != nil {
		fatalf("%v", err)
	}
	if existing {
		_, fp, reuse, err := existingIdentityMaterials(*out, inputDigest)
		if err != nil || !reuse {
			fatalf("existing installation refused: %v", err)
		}
		keys, err := os.ReadFile(filepath.Join(*out, "iam-installation", "compose.keys"))
		if err != nil {
			fatalf("read existing compose keys")
		}
		err = validateComposeKeys(keys)
		clear(keys)
		if err != nil {
			fatalf("existing installation refused: %v", err)
		}
		if err := networkdeploy.CheckCertificates(filepath.Join(*out, "network"), time.Now(), os.Stderr); err != nil {
			fatalf("%v", err)
		}
		fmt.Printf("bootstrap ok out=%s ca_sha256=%s image=%s (existing installation)\n", *out, fp, *image)
		return
	}
	manifest, config, err := loadRuntimeProofs(*proofs, *image)
	if err != nil {
		fatalf("%v", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("%v", err)
	}
	pki := filepath.Join(*out, "pki")
	tpl := filepath.Join(*out, "templates")
	mustMkdir(pki, 0o755)
	mustMkdir(tpl, 0o755)
	mustMkdir(filepath.Join(*out, "join"), 0o755)
	if err := prepareRuntimeRootDirectory(*out); err != nil {
		fatalf("%v", err)
	}
	if err := materials.EnsureServiceCredentials(*out); err != nil {
		fatalf("%v", err)
	}
	if err := writeComposeKeys(*out); err != nil {
		fatalf("%v", err)
	}
	generateIdentityMaterials(*out)
	if err := browser.Write(*out); err != nil {
		fatalf("%v", err)
	}
	rpc := map[string]string{"Target": fmt.Sprintf("127.0.0.1:%d", iamRPCPort), "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt", "CertFile": "/etc/auriga/pki/gateway.crt", "KeyFile": "/etc/auriga/pki/gateway.key", "CAFile": "/etc/auriga/pki/ca.crt", "ServerName": iamServerName}
	if err := networkdeploy.Generate(filepath.Join(*out, "network"), networkdeploy.Options{PlatformDirectory: filepath.Join(*out, "network-platform"), Root: "/etc/auriga/network", SubmitListen: "172.29.240.1:18443", BrokerHost: "127.0.0.1", WebsocketHost: "172.29.240.1", BrokerURL: "tls://127.0.0.1:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}); err != nil { // gitleaks:allow disposable M2 network
		fatalf("generate network deployment: %v", err)
	}
	if access.PortalOrigin().Scheme == "https" {
		if err := writeNativeClient(*out); err != nil {
			fatalf("%v", err)
		}
	}
	if err := writeCollab(*out, collab, access.Issuer()); err != nil {
		fatalf("%v", err)
	}
	pub, fp, _, err := readIdentityPublic(*out)
	if err != nil {
		fatalf("%v", err)
	}
	if err := installRuntimeProofs(*out, *image, manifest, config); err != nil {
		fatalf("%v", err)
	}
	kid := "e2e-id-1"

	writeFile(filepath.Join(*out, "access.json"), accessJSON, 0o644)
	writeFile(filepath.Join(*out, "iam-healthcheck.sh"), []byte(iamHealthcheck()), 0o755)
	writeJSON(filepath.Join(*out, "iam-client.json"), map[string]any{
		"baseURL": fmt.Sprintf("https://127.0.0.1:%d", iamPort), "serverName": iamServerName,
		"caFile": "pki/ca.crt", "certFile": "pki/edge-iam.crt", "keyFile": "pki/edge-iam.key",
	}, 0o644)
	writeJSON(filepath.Join(*out, "heartbeat-observer.json"), map[string]any{
		"enabled": *observer, "listenPort": observerPort,
		"upstream":   fmt.Sprintf("https://127.0.0.1:%d", iamPort),
		"serverName": iamServerName, "clientDNSName": edgeHopDNSName,
		"caFile": "pki/ca.crt", "certFile": "pki/iam.crt", "keyFile": "pki/iam.key",
		"clientCertFile": "pki/edge-iam.crt", "clientKeyFile": "pki/edge-iam.key",
	}, 0o644)

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
		Dsh:     &nodev1.DshRuntime{Version: version, ArtifactId: name, Image: *image},
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
	runtimeSigner, err := generateRuntimeRootSigner(*out)
	if err != nil {
		fatalf("%v", err)
	}
	if err := signRuntimeRelease(spec.Dsh, runtimeSigner, time.Now()); err != nil {
		clear(runtimeSigner)
		fatalf("%v", err)
	}
	clear(runtimeSigner)
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

	upstreamPort := iamPort
	if *observer {
		upstreamPort = observerPort
	}
	edgeCfg := map[string]any{
		"accessConfigFile":  "/etc/auriga/access.json",
		"allowLocalIngress": true,
		"upstreams": map[string]string{
			"network-submit": "https://172.29.240.1:18443", // gitleaks:allow disposable M2 network
			"iam":            fmt.Sprintf("https://host.docker.internal:%d", upstreamPort),
			"org":            fmt.Sprintf("https://host.docker.internal:%d", upstreamPort),
			"workspace":      fmt.Sprintf("https://host.docker.internal:%d", upstreamPort),
		},
		"iamHop": map[string]string{
			"certFile": "/etc/auriga/pki/edge-iam.crt", "keyFile": "/etc/auriga/pki/edge-iam.key",
			"caFile": "/etc/auriga/pki/ca.crt", "serverName": iamServerName,
		},
		"networkSubmitHop": map[string]string{"certFile": "/etc/auriga/network-edge/edge.pem", "keyFile": "/etc/auriga/network-edge/edge.key", "caFile": "/etc/auriga/network-edge/server-ca.pem", "serverName": "submit.network.internal"},
		"networkWss": map[string]any{
			"enabled": true, "path": "/net/v1/ws",
			"upstreamUrl": "wss://172.29.240.1:18444", // gitleaks:allow disposable M2 network
			"caFile":      "/etc/auriga/network-edge/server-ca.pem", "serverName": "broker.network.internal",
			"allowedOrigins": []string{access.PortalURL()},
		},
		"iamRpc": map[string]any{
			"address":    fmt.Sprintf("host.docker.internal:%d", iamRPCPort),
			"serverName": iamServerName, "timeoutMillis": 2000,
			"serviceTokenFile": "/run/auriga-internal/edge/service.jwt",
		},
		"tls":              map[string]string{"mode": "off"},
		"rateLimit":        map[string]any{"requestsPerSecond": 200, "burst": 400},
		"identityKeysFile": "/etc/auriga/identity-signing-keys.json",
		"sessionStore":     "nats",
		"natsUrl":          "nats://sessions:e2e_nats_sessions@host.docker.internal:4222",
		"nodeTLS": map[string]string{
			"certFile": "/etc/auriga/pki/edge.crt",
			"keyFile":  "/etc/auriga/pki/edge.key",
			"caFile":   "/etc/auriga/pki/ca.crt",
		},
	}
	if access.PortalOrigin().Scheme == "https" {
		edgeCfg["tls"] = map[string]string{"mode": "files", "certFile": "/etc/auriga/https/browser.crt", "keyFile": "/etc/auriga/https/browser.key"}
	}
	if collab != nil {
		edgeCfg["upstreams"].(map[string]string)["collab"] = "https://172.29.240.1:8085" // gitleaks:allow disposable M2 network
		edgeCfg["collabHop"] = map[string]string{"certFile": "/etc/auriga/collab-edge/edge-collab.crt", "keyFile": "/etc/auriga/collab-edge/edge-collab.key", "caFile": "/etc/auriga/collab-edge/ca.crt", "serverName": "collab.network.internal"}
	}
	writeJSON(filepath.Join(*out, "edge.config.json"), edgeCfg, 0o644)
	// Public values are derived from the validated installation; no credentials.
	accessEnv := composeAccessEnv(access, *accessFile != "")
	if collab != nil {
		accessEnv = append(accessEnv, []byte("COMPOSE_PROFILES='network-collab'\nCOMPOSE_FILE='docker-compose.yml:scripts/collab-required.yml'\nAURIGA_IAM_TENANT_DELEGATES='svc:edge,svc:ai-gateway,svc:collab'\n")...)
	}
	writeFile(filepath.Join(*out, "access.env"), accessEnv, 0o644)

	policy := map[string]any{
		"allowedImageRepos": []string{"127.0.0.1:5000/auriga/", "localhost:5000/auriga/"},
		"maxCPUMillis":      4000,
		"maxMemoryMB":       4096,
		"maxPIDs":           512,
		"maxArtifactBytes":  64 << 20,
	}
	writeYAMLish(filepath.Join(*out, "agent-policy.yaml"), policy)

	if err := writeIdentityManifest(*out, inputDigest); err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("bootstrap ok out=%s ca_sha256=%s image=%s\n", *out, fp, *image)
}

// The pinned web fixture has browser terminalController and no model-PTY/IM
// plugins. Missing undeclared sources remain unknown in its heartbeats.
func profileFor(fixture string) *nodev1.Profile {
	p := &nodev1.Profile{ListenPort: 7860}
	if fixture == "real" {
		p.Patches = []*nodev1.ConfigPatch{
			{Path: "plugins.auriga-dsh.imBots", ValueJson: `"none"`},
			{Path: "plugins.auriga-dsh.terminalSources", ValueJson: `"browser-only"`},
		}
	}
	return p
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

func generateIdentityMaterials(out string) {
	pki := filepath.Join(out, "pki")
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

	iamCert, iamKey := mustIssue(caKey, caCert, "iam-compose", "auriga://iam/compose-e2e",
		[]string{iamServerName}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "iam.crt"), append(iamCert, caPEM...), 0o644)
	writeFile(filepath.Join(pki, "iam.key"), iamKey, 0o400)
	hopCert, hopKey := mustIssue(caKey, caCert, "edge-iam-compose", "auriga://edge/iam-compose-e2e",
		[]string{edgeHopDNSName}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "edge-iam.crt"), hopCert, 0o644)
	writeFile(filepath.Join(pki, "edge-iam.key"), hopKey, 0o400)
	gatewayCert, gatewayKey := mustIssue(caKey, caCert, "gateway-compose", "auriga://ai-gateway/compose", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "gateway.crt"), gatewayCert, 0o644)
	writeFile(filepath.Join(pki, "gateway.key"), gatewayKey, 0o400)
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
	writeJSON(filepath.Join(out, "identity-signing-keys.json"), signing, 0o600)
	writeJSON(filepath.Join(out, "identity-keys.json"), pubOnly, 0o644)

	fp := spkiSHA256(caCert)
	writeFile(filepath.Join(out, "ca-sha256.txt"), []byte(fp+"\n"), 0o644)

}
