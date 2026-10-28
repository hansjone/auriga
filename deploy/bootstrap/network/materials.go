// Package network generates private deployment inputs for the existing network
// callout runtime. It never runs a second submit handler or changes IAM trust.
package network

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
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nkeys"
)

// Files are receipt-managed, including all private keys and broker passwords.
var Files = []string{"server-ca.pem", "client-ca.pem", "submit.pem", "submit.key", "edge.pem", "edge.key", "broker.pem", "broker.key", "broker-client.pem", "broker-client.key", "issuer.seed", "xkey.seed", "auth.password", "ops.password", "data.password", "protocol.key", "runtime.json", "broker.conf"}
var CredentialFiles = []string{"gateway.secret", "platform.properties"}

type Options struct {
	Root               string            `json:"root"`
	SubmitListen       string            `json:"submitListen"`
	BrokerHost         string            `json:"brokerHost"`
	BrokerURL          string            `json:"brokerURL"`
	BrokerPort         int               `json:"brokerPort"`
	WebsocketPort      int               `json:"websocketPort"`
	Audience           string            `json:"audience"`
	IAM                map[string]string `json:"iam"`
	Collab             map[string]string `json:"collab"`
	ExportServiceToken bool              `json:"exportServiceToken"`
}

func Generate(dir string, o Options) error {
	if o.Root == "" || o.SubmitListen == "" || o.BrokerHost == "" || o.BrokerURL == "" || o.BrokerPort < 1 || o.WebsocketPort < 1 || o.Audience == "" || o.IAM["ServiceTokenFile"] == "" || o.Collab["ServiceTokenFile"] == "" {
		return fmt.Errorf("incomplete network deployment options")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		info, statErr := os.Lstat(dir)
		entries, readErr := os.ReadDir(dir)
		if !os.IsExist(err) || statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || readErr != nil || len(entries) != 0 {
			return fmt.Errorf("network directory must be new: %w", err)
		}
	}
	write := func(name string, raw []byte) error {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if o.ExportServiceToken {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		secret := []byte(base64.RawURLEncoding.EncodeToString(raw))
		clear(raw)
		hash := sha256.Sum256(secret)
		err := write("gateway.secret", secret)
		clear(secret)
		if err != nil {
			return err
		}
		properties := fmt.Sprintf("auriga.iam.internal-secret-hashes.ai-gateway=%x\nauriga.iam.ai-gateway-token-export.enabled=true\nauriga.iam.ai-gateway-token-export.installation-secret-file=%s/gateway.secret\nauriga.iam.ai-gateway-token-export.token-file=/run/auriga-network-token/gateway/service.jwt\nauriga.iam.ai-gateway-token-export.reader-group=65533\nauriga.iam.ai-gateway-token-export.installation-owner=%d\nauriga.iam.network.server-audiences[0]=%s\nauriga.iam.network.client-server-audience=%s\n", hash, strings.TrimRight(o.Root, "/"), os.Getuid(), o.Audience, o.Audience)
		if err := write("platform.properties", []byte(properties)); err != nil {
			return err
		}
	}
	ca := func(name string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if err != nil {
			return nil, nil, err
		}
		cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Auriga network " + name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if err == nil {
			err = write(name+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		}
		return cert, key, err
	}
	serverCA, serverKey, err := ca("server-ca")
	if err != nil {
		return err
	}
	clientCA, clientKey, err := ca("client-ca")
	if err != nil {
		return err
	}
	for _, name := range []string{"submit", "edge", "broker", "broker-client"} {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if err != nil {
			return err
		}
		parent, signer, usage := serverCA, serverKey, x509.ExtKeyUsageServerAuth
		if name == "edge" {
			parent, signer, usage = clientCA, clientKey, x509.ExtKeyUsageClientAuth
		}
		if name == "broker-client" {
			usage = x509.ExtKeyUsageClientAuth
		}
		cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name + ".network.internal"}, DNSNames: []string{name + ".network.internal"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: parent.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if name == "broker" {
			cert.DNSNames = append(cert.DNSNames, "network-broker")
			cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, parent, &key.PublicKey, signer)
		if err != nil {
			return err
		}
		if err = write(name+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
			return err
		}
		pk, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		if err = write(name+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})); err != nil {
			return err
		}
	}
	issuer, err := nkeys.CreateAccount()
	if err != nil {
		return err
	}
	defer issuer.Wipe()
	xkey, err := nkeys.CreateCurveKeys()
	if err != nil {
		return err
	}
	defer xkey.Wipe()
	for name, kp := range map[string]nkeys.KeyPair{"issuer.seed": issuer, "xkey.seed": xkey} {
		seed, err := kp.Seed()
		if err != nil {
			return err
		}
		defer clear(seed)
		err = write(name, seed)
		if err != nil {
			return err
		}
	}
	issuerPub, err := issuer.PublicKey()
	if err != nil {
		return err
	}
	xpub, err := xkey.PublicKey()
	if err != nil {
		return err
	}
	p := func(name string) string { return strings.TrimRight(filepath.ToSlash(o.Root), "/") + "/" + name }
	passwords := map[string]string{}
	for _, user := range []string{"auth", "ops", "data"} {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		passwords[user] = hex.EncodeToString(raw)
		clear(raw)
		if err = write(user+".password", []byte(passwords[user])); err != nil {
			return err
		}
	}
	_, protocolKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	defer clear(protocolKey)
	pk, err := x509.MarshalPKCS8PrivateKey(protocolKey)
	if err != nil {
		return err
	}
	defer clear(pk)
	if err = write("protocol.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})); err != nil {
		return err
	}
	broker := fmt.Sprintf(`server_name: "auriga-network"
host: %q
port: %d
tls {cert_file: %q, key_file: %q, ca_file: %q, verify: true, timeout: 2}
system_account: SYS
websocket {host: %q, port: %d, tls {cert_file: %q, key_file: %q}}
accounts {
 AUTH {users: [{user: "auth", password: %q}]}
 CLIENTS {
  limits {max_payload: 65536, max_subscriptions: 8}
  exports: [{stream: "auriga.net.up.>", accounts: [SVC]}]
  imports: [{stream: {account: SVC, subject: "auriga.net.down.>"}}, {stream: {account: SVC, subject: "auriga.net.reply.>"}}]
 }
 SVC {
  users: [{user: "data", password: %q, permissions: {publish: {allow: ["auriga.net.down.*", "auriga.net.reply.>", "auriga.internal.>"]}, subscribe: {allow: ["auriga.net.up.*.*", "auriga.internal.>", "_INBOX.>"]}}}]
  exports: [{stream: "auriga.net.down.>", accounts: [CLIENTS]}, {stream: "auriga.net.reply.>", accounts: [CLIENTS]}]
  imports: [{stream: {account: CLIENTS, subject: "auriga.net.up.>"}}]
 }
 SYS {users: [{user: "ops", password: %q, permissions: {publish: {allow: ["$SYS.REQ.SERVER.*.KICK", "$SYS.REQ.SERVER.*.LDM", "$SYS.REQ.SERVER.*.CONNZ"]}, subscribe: {allow: ["_INBOX.>"]}}}]}
}
authorization {timeout: 2, auth_callout {issuer: %q, account: AUTH, auth_users: ["auth", "ops", "data"], xkey: %q}}
`, o.BrokerHost, o.BrokerPort, p("broker.pem"), p("broker.key"), p("server-ca.pem"), o.BrokerHost, o.WebsocketPort, p("broker.pem"), p("broker.key"), passwords["auth"], passwords["data"], passwords["ops"], issuerPub, xpub)
	if err = write("broker.conf", []byte(broker)); err != nil {
		return err
	}
	nats := func(user string) any {
		return map[string]any{"url": o.BrokerURL, "username": user, "passwordFile": p(user + ".password"), "certFile": p("broker-client.pem"), "keyFile": p("broker-client.key"), "caFile": p("server-ca.pem")}
	}
	cfg := map[string]any{"submitHTTP": map[string]string{"listen": o.SubmitListen, "tlsCertFile": p("submit.pem"), "tlsKeyFile": p("submit.key"), "clientCAFile": p("client-ca.pem")}, "brokerConfigFile": p("broker.conf"), "serverAudience": o.Audience, "clientAccount": "CLIENTS", "issuerSeedFile": p("issuer.seed"), "xkeySeedFile": p("xkey.seed"), "iam": o.IAM, "collab": o.Collab, "auth": nats("auth"), "ops": nats("ops"), "data": nats("data"), "protocolSigning": map[string]string{"privateKeyFile": p("protocol.key"), "keyId": "network-protocol-1", "keysetVersion": "1", "notBefore": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "notAfter": serverCA.NotAfter.UTC().Format(time.RFC3339)}, "redeemTimeoutMillis": 1000, "activeIntervalMillis": 250, "requestTimeoutMillis": 250, "maxKickAttempts": 3, "maxConnections": 1000}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return write("runtime.json", raw)
}
