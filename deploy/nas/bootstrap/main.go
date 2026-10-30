// Bootstrap generates PKI and identity keys for the Auriga NAS test stack.
// Optional formal templates consume independently approved signed runtime bundles.
//
//	go run . -out /path/to/config
package main

import (
	"context"
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
	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	"github.com/hansjone/auriga/deploy/bootstrap/panel"
	"github.com/hansjone/auriga/deploy/bootstrap/relayui"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "image-metadata" || os.Args[1] == "image-check" || os.Args[1] == "image-resolve") {
		if err := imageCommand(context.Background(), os.Args[1:], os.Stdout); err != nil {
			fatalf("%v", err)
		}
		return
	}
	out := flag.String("out", "", "private source material directory")
	accessFile := flag.String("access-file", "", "required trusted access JSON")
	httpsCert := flag.String("https-cert-file", "", "external direct HTTPS PEM certificate chain")
	httpsKey := flag.String("https-key-file", "", "external direct HTTPS PEM private key")
	uiOrigin := flag.String("litellm-ui-origin", "", "independent LiteLLM UI origin (default: admin host on port 8086)")
	validateOnly := flag.Bool("validate-access", false, "validate without writing installation materials")
	peerIP := flag.String("edge-peer-ip", "", "fixed edge component-network IPv4")
	subnet := flag.String("internal-subnet", "", "component-network IPv4 prefix")
	lanIP := flag.String("lan-ip", "", "required edge LAN IPv4 excluded from instance control")
	lanSubnet := flag.String("lan-subnet", "", "required disjoint LAN IPv4 prefix")
	registryPort := flag.Int("registry-port", 15000, "host loopback registry port")
	instanceMemory := flag.String("instance-max-memory-mb", "512", "per-instance memory limit in positive canonical decimal MiB")
	runtimeBundle := flag.String("runtime-bundle", "", "public signed runtime bundle directory")
	runtimeRoot := flag.String("runtime-root", "", "independently approved external runtime root protobuf")
	runtimeImage := flag.String("runtime-image", "", "independently approved complete repo@sha256 locator")
	flag.Parse()
	if flag.NArg() != 0 {
		fatalf("unexpected positional arguments")
	}
	maxMemoryMB, err := parseInstanceMaxMemoryMB(*instanceMemory)
	if err != nil {
		fatalf("%v", err)
	}
	runtime := runtimeInputs{*runtimeBundle, *runtimeRoot, *runtimeImage}
	if err := runtime.validate(maxMemoryMB); err != nil {
		fatalf("%v", err)
	}
	raw, cfg, err := loadNASAccess(*accessFile, *peerIP, *subnet, *registryPort)
	if err != nil {
		fatalf("%v", err)
	}
	if err := validateNASLAN(*lanIP, *lanSubnet, *subnet); err != nil {
		fatalf("%v", err)
	}
	ui, err := relayui.Resolve(cfg, *uiOrigin)
	if err != nil {
		fatalf("%v", err)
	}
	directHTTPS := cfg.IngressKind() == "direct" && cfg.PortalOrigin().Scheme == "https"
	hosts := []string{cfg.PortalOrigin().Host, cfg.AdminOrigin().Host, ui.Origin.Host}
	if parent := cfg.InstanceParentDomain(); parent != "" {
		hosts = append(hosts, "*."+parent)
	}
	browser, err := networkdeploy.ReadBrowserTLS(*httpsCert, *httpsKey, directHTTPS, hosts)
	if err != nil {
		fatalf("%v", err)
	}
	defer browser.Clear()
	if !*validateOnly && *out == "" {
		fatalf("-out is required")
	}
	if err := runAsInstaller(*out, raw, *peerIP, *subnet, *registryPort, *lanIP, *lanSubnet, maxMemoryMB, runtime, *validateOnly, *uiOrigin, browser); err != nil {
		fatalf("%v", err)
	}
	verified, err := runtime.verify()
	if err != nil {
		fatalf("%v", err)
	}
	if *validateOnly {
		return
	}
	set := runtimeMaterialSet(verified, directHTTPS)
	guard, err := openNASMaterialGuard(*out, set)
	if err != nil {
		fatalf("%v", err)
	}
	defer guard.Close()
	pki := filepath.Join(*out, "pki")
	inputDigest := nasInputDigest(raw, *peerIP, *subnet, *registryPort, *lanIP, *lanSubnet, maxMemoryMB, runtime, verified, ui.Digest(), browser.Digest())
	reuse, err := materials.CheckReceipt(*out, inputDigest, set)
	if err != nil {
		fatalf("%v", err)
	}
	if err := materials.EnsureServiceCredentials(*out); err != nil {
		fatalf("%v", err)
	}
	if reuse {
		if err := guard.Validate(false); err != nil {
			fatalf("%v", err)
		}
		if err := networkdeploy.CheckCertificates(filepath.Join(*out, "network"), time.Now(), os.Stderr); err != nil {
			fatalf("%v", err)
		}
		fmt.Println("nas-bootstrap ok (existing installation)")
		return
	}
	mustPKI(pki)
	writeLiteLLM(*out, ui)
	if err := browser.Write(*out); err != nil {
		fatalf("%v", err)
	}
	rpc := map[string]string{"Target": "platform:9091", "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt", "CertFile": "/etc/auriga/pki/gateway.crt", "KeyFile": "/etc/auriga/pki/gateway.key", "CAFile": "/etc/auriga/pki/ca.crt", "ServerName": "platform"}
	if err := networkdeploy.Generate(filepath.Join(*out, "network"), networkdeploy.Options{PlatformDirectory: filepath.Join(*out, "network-platform"), Root: "/etc/auriga/network", SubmitListen: "0.0.0.0:18443", BrokerHost: "0.0.0.0", BrokerURL: "tls://network-broker:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}); err != nil {
		fatalf("generate network deployment: %v", err)
	}
	mustIdentity(*out)
	writeNASConfig(*out, raw, cfg, *peerIP, *registryPort, *lanIP, maxMemoryMB, ui)
	if err := writeRuntimeMaterials(*out, verified, maxMemoryMB); err != nil {
		fatalf("%v", err)
	}
	if verified != nil {
		if err := guard.pinRuntimeProof(runtimeProofDirectory(verified)); err != nil {
			fatalf("%v", err)
		}
		writeRuntimePolicy(*out, runtime.image, maxMemoryMB)
	}
	if err := guard.Validate(false); err != nil {
		fatalf("%v", err)
	}
	if err := materials.SealReceipt(*out, inputDigest, set); err != nil {
		fatalf("%v", err)
	}
	if err := guard.Validate(false); err != nil {
		fatalf("%v", err)
	}
	fmt.Println("nas-bootstrap ok")
}

func mustPKI(pki string) {
	if fileExists(filepath.Join(pki, "ca.key")) || fileExists(filepath.Join(pki, "ca.crt")) {
		fatalf("PKI already present under %s; refusing to overwrite", pki)
	}
	caKey, caCert, caPEM := mustCA()
	writeFile(filepath.Join(pki, "ca.crt"), caPEM, 0o644)
	writeFile(filepath.Join(pki, "ca.key"), pkcs8PEM(caKey), 0o600)

	wsCert, wsKey := mustIssue(caKey, caCert, "workspace", "auriga://workspace",
		[]string{"platform", "localhost"},
		[]net.IP{net.ParseIP("127.0.0.1")},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "workspace.crt"), append(wsCert, caPEM...), 0o644)
	writeFile(filepath.Join(pki, "workspace.key"), wsKey, 0o600)

	edgeCert, edgeKey := mustIssue(caKey, caCert, "edge-nas-test", "auriga://edge/nas-test",
		nil, nil,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "edge.crt"), edgeCert, 0o644)
	// 0600 + chown 65532 by init.sh so distroless edge can read without world-readable keys.
	writeFile(filepath.Join(pki, "edge.key"), edgeKey, 0o600)

	iamCert, iamKey := mustIssue(caKey, caCert, "iam-nas", "auriga://iam/nas-test", []string{"platform"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "iam.crt"), append(iamCert, caPEM...), 0o644)
	writeFile(filepath.Join(pki, "iam.key"), iamKey, 0o600)
	hopCert, hopKey := mustIssue(caKey, caCert, "edge-iam-nas", "auriga://edge/iam-nas-test", []string{"edge.auriga.internal"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "edge-iam.crt"), hopCert, 0o644)
	writeFile(filepath.Join(pki, "edge-iam.key"), hopKey, 0o600)
	gatewayCert, gatewayKey := mustIssue(caKey, caCert, "gateway-nas", "auriga://ai-gateway/nas", []string{"ai-relay"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "gateway.crt"), gatewayCert, 0o644)
	writeFile(filepath.Join(pki, "gateway.key"), gatewayKey, 0o600)

	// Front proxy trust is independent of IAM/NodeControl identities and CA.
	proxyKey, proxyCA, proxyCAPEM := mustCA()
	writeFile(filepath.Join(pki, "proxy-ca.crt"), proxyCAPEM, 0o644)
	writeFile(filepath.Join(pki, "proxy-ca.key"), pkcs8PEM(proxyKey), 0o600)
	server, serverKey := mustIssue(proxyKey, proxyCA, "edge-proxy-nas", "auriga://edge/proxy-nas", []string{"edge.auriga.internal"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "proxy-edge.crt"), append(server, proxyCAPEM...), 0o644)
	writeFile(filepath.Join(pki, "proxy-edge.key"), serverKey, 0o600)
	client, clientKey := mustIssue(proxyKey, proxyCA, "proxy-client-nas", "auriga://proxy/nas-test", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "proxy-client.crt"), client, 0o644)
	writeFile(filepath.Join(pki, "proxy-client.key"), clientKey, 0o600)

	fp := spkiSHA256(caCert)
	writeFile(filepath.Join(filepath.Dir(pki), "ca-sha256.txt"), []byte(fp+"\n"), 0o644)
}

func mustIdentity(out string) {
	if err := panel.Generate(out); err != nil {
		fatalf("generate panel key: %v", err)
	}
	privPath := filepath.Join(out, "identity-signing-keys.json")
	pubPath := filepath.Join(out, "identity-keys.json")
	if fileExists(privPath) || fileExists(pubPath) {
		fatalf("identity keys already present under %s; refusing to overwrite", out)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	kid := "nas-id-1"
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
	writeJSON(privPath, signing, 0o600)
	writeJSON(pubPath, pubOnly, 0o644)
}

func mustCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	uri, _ := url.Parse("auriga://workspace")
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "auriga-nas-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"platform", "localhost"},
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
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
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

func writeJSON(path string, v any, mode os.FileMode) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatalf("%v", err)
	}
	writeFile(path, append(raw, '\n'), mode)
}

func writeFile(path string, b []byte, mode os.FileMode) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		fatalf("%v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		fatalf("%v", err)
	}
	_ = os.Chmod(path, mode)
}

func mustMkdir(path string, mode os.FileMode) {
	if err := os.MkdirAll(path, mode); err != nil {
		fatalf("%v", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
