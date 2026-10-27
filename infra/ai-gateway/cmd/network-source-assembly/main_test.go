package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"encoding/hex"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"github.com/nats-io/nats-server/v2/conf"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
	"google.golang.org/protobuf/proto"
)

func TestLoopbackGuardsRejectRemoteAndHostnames(t *testing.T) {
	for _, v := range []string{"localhost:1", "example.com:443", "192.0.2.10:1", ":1", "127.0.0.1"} {
		if loopback(v) {
			t.Fatalf("accepted %q", v)
		}
	}
	if !loopback("127.0.0.1:1234") || !loopback("[::1]:1234") {
		t.Fatal("numeric loopback rejected")
	}
}
func TestPinnedBrokerRejectsArbitraryExecutableBeforeLaunch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-nats")
	if err := os.WriteFile(file, []byte("not a locked broker"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyBroker(file) == nil {
		t.Fatal("arbitrary binary accepted")
	}
}
func TestBrokerConfigurationKeepsProductionCalloutAccountsAndLimits(t *testing.T) {
	dir := t.TempDir()
	cert, key, _, err := tlsFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Wipe()
	x, err := nkeys.CreateCurveKeys()
	if err != nil {
		t.Fatal(err)
	}
	defer x.Wipe()
	ap, err := a.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	xp, err := x.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "broker.conf")
	if err = os.WriteFile(file, []byte(brokerConfig(-1, -1, cert, key, ap, xp, []string{"a", "b", "c"})), 0600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if opts.AuthCallout == nil || opts.AuthCallout.Issuer != ap || opts.AuthCallout.XKey != xp || opts.AuthCallout.Account != "AUTH" || opts.AuthTimeout != 2 || opts.Websocket.NoTLS {
		t.Fatal("callout/TLS configuration changed")
	}
	parsed, err := conf.ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	limits := parsed["accounts"].(map[string]any)["CLIENTS"].(map[string]any)["limits"].(map[string]any)
	if limits["max_payload"] != int64(network.EncodingMaxPayloadBytes) || limits["max_subscriptions"] != int64(8) {
		t.Fatal("account limits differ")
	}
}
func TestBrokerCertificateChecksRealClockAndIpSan(t *testing.T) {
	cert, key, ca, err := tlsFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "127.0.0.1", CurrentTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example.com", CurrentTime: time.Now()}); err == nil {
		t.Fatal("wrong hostname accepted")
	}
}
func TestPersistedProtoDecodeKeepsUnsignedConnectionAndExactBytes(t *testing.T) {
	expected := &iam.NetworkAuthorityConnection{TenantId: "tenant", Realm: "18446744073709551615", Node: "9223372036854775809", Ingress: "ingress", ServerId: "server", ClientId: "99", ClientAccount: "CLIENTS"}
	raw, err := proto.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"connection": `\x` + hex.EncodeToString(raw)}
	if err = decodeFacts("iam", "network_connection_anchor", row); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(row["connection_decoded"].(json.RawMessage), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["realm"] != expected.Realm || decoded["node"] != expected.Node || row["connection"] != `\x`+hex.EncodeToString(raw) {
		t.Fatal("persistent fact changed")
	}
	if err = decodeFacts("iam", "network_source", map[string]any{"fact": "invalid"}); err == nil {
		t.Fatal("bad persistent bytes accepted")
	}
}
func TestFaultGuardHasNoAuthenticationOrRegisterFallback(t *testing.T) {
	r := &relay{}
	if r.fault("RedeemNetworkTicket", "drop") == nil || r.fault("SourceRegister", "allow") == nil {
		t.Fatal("unsupported authentication mutation accepted")
	}
	if r.fault("SourceGet", "corrupt") != nil {
		t.Fatal("signed transport corruption rejected")
	}
}
