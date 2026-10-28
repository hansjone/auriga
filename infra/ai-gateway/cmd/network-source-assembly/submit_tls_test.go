package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

func TestAssemblySubmitTLSUsesDedicatedClientCAAndRealRuntime(t *testing.T) {
	dir := t.TempDir()
	cert, key, ca, err := tlsFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	material, err := submitClientFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	defer issuer.Wipe()
	xkey, err := nkeys.CreateCurveKeys()
	if err != nil {
		t.Fatal(err)
	}
	defer xkey.Wipe()
	pub, err := issuer.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	xpub, err := xkey.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	configFile := write("broker.conf", []byte(brokerConfig(-1, -1, cert, key, pub, xpub, []string{"auth-private", "ops-private", "data-private"})))
	opts, err := server.ProcessConfigFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	opts.NoLog, opts.NoSigs = true, true
	broker, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	broker.Start()
	defer func() { broker.Shutdown(); broker.WaitForShutdown() }()
	if !broker.ReadyForConnections(5 * time.Second) {
		t.Fatal("broker did not become ready")
	}
	issuerSeed, err := issuer.Seed()
	if err != nil {
		t.Fatal(err)
	}
	xSeed, err := xkey.Seed()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(xSeed)
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(signingKey)
	pk, err := x509.MarshalPKCS8PrivateKey(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(pk)
	submitPort, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	rpc := team.RPCConfig{Target: "127.0.0.1:1", ServiceTokenFile: write("service.jwt", []byte("unit-only-service-ticket"))}
	cfg := network.RuntimeConfig{
		SubmitHTTP:       material.listenerConfig(fmt.Sprintf("127.0.0.1:%d", submitPort), cert, key),
		BrokerConfigFile: configFile, ServerAudience: "assembly-test", ClientAccount: "CLIENTS", IssuerSeedFile: write("issuer.seed", issuerSeed), XKeySeedFile: write("xkey.seed", xSeed), IAM: rpc, Collab: rpc,
		ProtocolSigning:     network.ProtocolSigningConfig{PrivateKeyFile: write("protocol.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})), KeyID: "assembly-test", KeysetVersion: "1", NotBefore: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), NotAfter: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		RedeemTimeoutMillis: 1000, ActiveIntervalMillis: 250, RequestTimeoutMillis: 250, MaxKickAttempts: 3, MaxConnections: 10,
	}
	for i, pair := range []struct {
		name, password string
		target         *network.NATSConfig
	}{{"auth", "auth-private", &cfg.Auth}, {"ops", "ops-private", &cfg.Ops}, {"data", "data-private", &cfg.Data}} {
		*pair.target = network.NATSConfig{URL: broker.ClientURL(), Username: pair.name, PasswordFile: write(fmt.Sprintf("password-%d", i), []byte(pair.password))}
	}
	runtime, err := network.StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	raw, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("no server CA")
	}
	wrong, err := submitClientFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cert, key string
		allowed         bool
	}{{"missing", "", "", false}, {"wrong-ca", wrong.ClientCertFile, wrong.ClientKeyFile, false}, {"dedicated", material.ClientCertFile, material.ClientKeyFile, true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost"}
			if tc.cert != "" {
				pair, err := tls.LoadX509KeyPair(tc.cert, tc.key)
				if err != nil {
					t.Fatal(err)
				}
				c.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil }
			}
			transport := &http.Transport{TLSClientConfig: c, Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Post(fmt.Sprintf("https://127.0.0.1:%d%s", submitPort, network.SubmitPath), "application/json", nil)
			if !tc.allowed {
				if err == nil {
					_ = response.Body.Close()
					t.Fatal("untrusted client reached SubmitHandler")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 401 || !strings.Contains(string(body), "network.unauthorized") {
				t.Fatalf("real SubmitHandler not reached: %d %s", response.StatusCode, body)
			}
		})
	}
	if _, err = submitClientFiles(dir); err == nil {
		t.Fatal("private TLS material overwritten")
	}
}
