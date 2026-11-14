package projection_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection/testfixture"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestBrokerOwnedRestartWithRealMTLS(t *testing.T) {
	binary := os.Getenv("AURIGA_TEST_NATS_BINARY")
	if binary == "" {
		t.Skip("requires repository-locked NATS executable")
	}
	raw, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	f := testfixture.New(t)
	root, control, state := testfixture.Dir(t), testfixture.Dir(t), testfixture.Dir(t)
	schemas, e := projection.LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	pin := f.Pin
	pin.IdentityId = "broker-projector"
	pin.TargetId = "broker-reader"
	pin.Component = "broker"
	trust, e := projection.NewTrust(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", state, pin)
	if e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	template := filepath.Join(control, "broker.template.conf")
	config := fmt.Sprintf("host: 127.0.0.1\nport: %d\ntls { cert_file: \"{{MSGNET_CERT}}\", key_file: \"{{MSGNET_KEY}}\", ca_file: \"{{MSGNET_CA}}\", verify: true }\n", port)
	if e = os.WriteFile(template, []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	supervisor := &projection.BrokerSupervisor{Binary: binary, ExpectedSHA256: projection.Digest(raw), Template: template, Control: control, Trust: trust}
	defer supervisor.Close()
	reader, e := projection.NewReader(root, control, state, supervisor.Prepare)
	if e != nil {
		t.Fatal(e)
	}
	publish := func(revision string, n byte) api.LocalProjectionSelector {
		t.Helper()
		nonce := make([]byte, 32)
		for j := range nonce {
			nonce[j] = n
		}
		pd, _ := projection.ObjectDigest(f.Initial.Keyset)
		m := api.ProjectionMaterial{TenantId: f.Initial.Keyset.Payload.TenantId, Realm: "1", InstallationId: pin.IdentityId, Component: "broker", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Generation: "1", KeysetVersion: "2", PublicationDigest: pd, Bundle: f.Bundle, Leaves: []api.TlsLeaf{f.BrokerLeaf}}
		g, e := projection.BuildGeneration(m, f.Initial, revision)
		if e != nil {
			t.Fatal(e)
		}
		defer g.Clear()
		if e = projection.Publish(root, g, func(g *projection.Generation) error {
			s, e := trust.Load(g)
			if s != nil {
				s.Clear()
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
		_, phase, code, e := projection.ResponseFields(reader.Handle(api.LocalProjectionRequest{Operation: "reload", Selector: g.Selector}))
		if e != nil || phase != "loaded" {
			t.Fatal(phase, code, e)
		}
		return g.Selector
	}
	roots := x509.NewCertPool()
	for _, ca := range f.Bundle.Payload.Cas {
		if ca.Role == "server" {
			der, e := base64.RawURLEncoding.DecodeString(ca.CertificateDer)
			if e != nil {
				t.Fatal(e)
			}
			cert, e := x509.ParseCertificate(der)
			if e != nil {
				t.Fatal(e)
			}
			roots.AddCert(cert)
		}
	}
	clientLeaf := f.Leaves[1]
	certDER, _ := base64.RawURLEncoding.DecodeString(clientLeaf.CertificateDer)
	keyDER, _ := base64.RawURLEncoding.DecodeString(clientLeaf.PrivatePkcs8)
	pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	clear(keyDER)
	if e != nil {
		t.Fatal(e)
	}
	dial := func() (*tls.Conn, string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if e != nil {
				if time.Now().After(deadline) {
					t.Fatalf("broker listener: %v (owned process active=%t)", e, supervisor.Active())
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			line, e := bufio.NewReader(conn).ReadString('\n')
			if e != nil || !strings.HasPrefix(line, "INFO ") {
				_ = conn.Close()
				t.Fatalf("broker greeting: %v", e)
			}
			secure := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost", RootCAs: roots, Certificates: []tls.Certificate{pair}})
			if e = secure.Handshake(); e != nil {
				_ = conn.Close()
				t.Fatal(e)
			}
			if _, e = secure.Write([]byte("CONNECT {}\r\nPING\r\n")); e != nil {
				_ = secure.Close()
				t.Fatal(e)
			}
			reader := bufio.NewReader(secure)
			for {
				line, e = reader.ReadString('\n')
				if e != nil {
					_ = secure.Close()
					t.Fatal(e)
				}
				if strings.TrimSpace(line) == "PONG" {
					break
				}
			}
			_ = secure.SetDeadline(time.Time{})
			return secure, secure.ConnectionState().PeerCertificates[0].SerialNumber.String()
		}
	}
	publish("1", 1)
	first, before := dial()
	defer func() { _ = first.Close() }()
	saved := f.Leaves
	f.Leaves = []api.TlsLeaf{f.BrokerLeaf}
	f.RenewLeaves(t)
	f.BrokerLeaf = f.Leaves[0]
	f.Leaves = saved
	publish("2", 2)
	second, after := dial()
	defer func() { _ = second.Close() }()
	if before == after {
		t.Fatal("broker did not adopt renewed leaf")
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	// Shutdown may leave INFO/-ERR bytes in flight. Drain those bytes and
	// require a terminal close; a timeout is not evidence of disconnection.
	n, e := io.Copy(io.Discard, io.LimitReader(first, 65537))
	if n > 65536 || (e != nil && !errors.Is(e, syscall.ECONNRESET)) {
		t.Fatalf("prior connection did not reach EOF/reset: bytes=%d error=%v", n, e)
	}
}
