package network

// This file is added only by go test -overlay to the network test package.
// Production sources and route selection remain unchanged. Invalid credentials
// and bodies deliberately stop before IAM/PG; no business success is fabricated.
import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/litellm"
	"io"
	"net/http/httptest"
	"os"
	"testing"
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
	// A real LiteLLM member handler rejects the invalid device ticket.
	deviceHandler, err := litellm.New("http://litellm:4000", "unused-key", func(context.Context, string) (litellm.Principal, error) {
		return litellm.Principal{}, errors.New("common.unauthenticated")
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
