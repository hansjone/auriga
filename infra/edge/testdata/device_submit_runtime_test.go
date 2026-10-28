package network

// This file is added only by go test -overlay to the network test package.
// Production sources and route selection remain unchanged. Invalid credentials
// and bodies deliberately stop before IAM/PG; no business success is fabricated.
import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relayhttp"
)

func TestEdgeSubmitRuntimeFixture(t *testing.T) {
	pair, err := tls.LoadX509KeyPair(os.Getenv("AURIGA_EDGE_FIXTURE_CERT"), os.Getenv("AURIGA_EDGE_FIXTURE_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(os.Getenv("AURIGA_EDGE_FIXTURE_CA"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("missing fixture CA")
	}
	// Runtime.SubmitHandler uses the production route, HTTPS checks and device
	// verifier. The deliberately invalid JWT is rejected before any RPC or READY
	// lookup, so the unused control-plane connections remain unset.
	r := &Runtime{mailbox: &mailboxRouter{}}
	network := httptest.NewUnstartedServer(r.SubmitHandler())
	network.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	network.StartTLS()
	t.Cleanup(network.Close)
	// This is the same boundary-only configuration used by relayhttp's own
	// TestBoundaryRejections. Invalid request framing exits before these unused
	// business components, while the real rawRoute table is still exercised.
	deviceHandler, err := relayhttp.New(relayhttp.Config{
		Core: new(relay.Core), Gate: new(modelgate.Gate), Authority: new(modelgate.Authority), Ledger: new(budget.Ledger),
		ReserveRecovery: func() (func(*relayhttp.Pending), bool) { return func(*relayhttp.Pending) {}, true },
		ReadTimeout:     5 * time.Second, WriteTimeout: 5 * time.Second, QueryTimeout: time.Second,
		MaxResponseBytes: 1 << 20, MaxInFlight: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	device := httptest.NewServer(deviceHandler)
	t.Cleanup(device.Close)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"network": network.URL, "device": device.URL}); err != nil {
		t.Fatal(err)
	}
	// Parent closes stdin after its real edge listeners finish. All servers are
	// random-port fixtures and the test binary is bounded by a parent context.
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}
