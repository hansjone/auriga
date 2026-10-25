package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/nats-io/nkeys"
)

// These files are all covered by the installation receipt. "PublicFiles" in
// that API means outside iam-installation, not publicly readable: secrets stay 0600.
var networkMaterialFiles = []string{
	"network/nats.conf", "network/gateway.json",
	"network/callout-account.seed", "network/callout-curve.seed",
	"network/auth.password", "network/ops.password",
	"network/nats.crt", "network/nats.key", "network/ca.crt",
	"network/iam-client.crt", "network/iam-client.key",
}

const networkAudience = "auriga-network-1"

func generateNetworkMaterials(out string, caKey *ecdsa.PrivateKey, ca *x509.Certificate, caPEM []byte) {
	dir := filepath.Join(out, "network")
	mustMkdir(dir, 0o700)
	issuer, err := nkeys.CreateAccount()
	if err != nil {
		fatalf("generate callout account: %v", err)
	}
	defer issuer.Wipe()
	xkey, err := nkeys.CreateCurveKeys()
	if err != nil {
		fatalf("generate callout curve key: %v", err)
	}
	defer xkey.Wipe()
	issuerSeed, err := issuer.Seed()
	if err != nil {
		fatalf("callout seed: %v", err)
	}
	defer clear(issuerSeed)
	xSeed, err := xkey.Seed()
	if err != nil {
		fatalf("curve seed: %v", err)
	}
	defer clear(xSeed)
	issuerPub, err := issuer.PublicKey()
	if err != nil {
		fatalf("callout public key: %v", err)
	}
	xPub, err := xkey.PublicKey()
	if err != nil {
		fatalf("curve public key: %v", err)
	}
	writeFile(filepath.Join(dir, "callout-account.seed"), issuerSeed, 0o600)
	writeFile(filepath.Join(dir, "callout-curve.seed"), xSeed, 0o600)
	password := func() string {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			fatalf("network password: %v", err)
		}
		defer clear(raw)
		return hex.EncodeToString(raw)
	}
	authPassword, opsPassword := password(), password()
	writeFile(filepath.Join(dir, "auth.password"), []byte(authPassword), 0o600)
	writeFile(filepath.Join(dir, "ops.password"), []byte(opsPassword), 0o600)
	cert, certKey := mustIssue(caKey, ca, "network-nats", "auriga://network/broker",
		[]string{"network-nats"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(dir, "nats.crt"), append(cert, caPEM...), 0o644)
	writeFile(filepath.Join(dir, "nats.key"), certKey, 0o600)
	cert, certKey = mustIssue(caKey, ca, "network-iam", "auriga://network/iam",
		nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(dir, "iam-client.crt"), cert, 0o644)
	writeFile(filepath.Join(dir, "iam-client.key"), certKey, 0o600)
	writeFile(filepath.Join(dir, "ca.crt"), caPEM, 0o644)
	// One timeout: the gateway reads authorization.timeout directly. The WSS
	// listener inherits it; there is deliberately no second timeout setting.
	broker := fmt.Sprintf(`server_name: %q
host: "0.0.0.0"
port: 4222
http: "127.0.0.1:8222"
system_account: SYS
tls {
  cert_file: "/etc/auriga/network/nats.crt"
  key_file: "/etc/auriga/network/nats.key"
  ca_file: "/etc/auriga/network/ca.crt"
  verify: true
}
websocket {
  host: "0.0.0.0"
  port: 8443
  tls { cert_file: "/etc/auriga/network/nats.crt", key_file: "/etc/auriga/network/nats.key" }
}
accounts {
  AUTH { users: [{ user: network-auth, password: %q, allowed_connection_types: [STANDARD], permissions: {
    publish: { allow: ["_INBOX.>"] }
    subscribe: { allow: ["$SYS.REQ.USER.AUTH"] }
    allow_responses: { max: 1, expires: "2s" }
  } }] }
  SYS { users: [{ user: network-ops, password: %q, allowed_connection_types: [STANDARD], permissions: {
    publish: { allow: ["$SYS.REQ.SERVER.*.CONNZ", "$SYS.REQ.SERVER.*.KICK"] }
    subscribe: { allow: ["_INBOX.>"] }
  } }] }
  CLIENTS {
    limits { max_payload: 65536, max_subscriptions: 8 }
    exports: [{ stream: "auriga.net.up.>", accounts: [SVC] }]
    imports: [
      { stream: { account: SVC, subject: "auriga.net.down.>" } }
      { stream: { account: SVC, subject: "auriga.net.reply.>" } }
    ]
  }
  SVC {
    exports: [
      { stream: "auriga.net.down.>", accounts: [CLIENTS] }
      { stream: "auriga.net.reply.>", accounts: [CLIENTS] }
    ]
    imports: [{ stream: { account: CLIENTS, subject: "auriga.net.up.>" } }]
  }
}
authorization {
  timeout: 2
  auth_callout { issuer: %q, account: AUTH, auth_users: [network-auth, network-ops], xkey: %q }
}
`, networkAudience, authPassword, opsPassword, issuerPub, xPub)
	writeFile(filepath.Join(dir, "nats.conf"), []byte(broker), 0o600)
	control := func(user, file string) map[string]string {
		return map[string]string{"url": "tls://network-nats:4222", "username": user, "passwordFile": "/etc/auriga/network/" + file,
			"certFile": "/etc/auriga/network/iam-client.crt", "keyFile": "/etc/auriga/network/iam-client.key", "caFile": "/etc/auriga/network/ca.crt"}
	}
	writeJSON(filepath.Join(dir, "gateway.json"), map[string]any{
		"brokerConfigFile": "/etc/auriga/network/nats.conf",
		"serverAudience":   networkAudience, "clientAccount": "CLIENTS",
		"issuerSeedFile": "/etc/auriga/network/callout-account.seed",
		"xkeySeedFile":   "/etc/auriga/network/callout-curve.seed",
		"iam": map[string]string{
			"Target": "host.docker.internal:9091", "ServerName": iamServerName,
			"ServiceTokenFile": "/run/auriga-network-tokens/service.jwt",
			"CertFile":         "/etc/auriga/network/iam-client.crt", "KeyFile": "/etc/auriga/network/iam-client.key",
			"CAFile": "/etc/auriga/network/ca.crt",
		},
		"auth": control("network-auth", "auth.password"), "ops": control("network-ops", "ops.password"),
		"redeemTimeoutMillis": 500, "activeIntervalMillis": 500, "requestTimeoutMillis": 250,
		"maxKickAttempts": 3, "maxConnections": 5000,
	}, 0o600)
}
