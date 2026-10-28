package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebsocketListenerHost(t *testing.T) {
	for _, host := range []string{"", "192.0.2.1"} {
		t.Run("host="+host, func(t *testing.T) {
			o := testOptions()
			o.WebsocketHost = host
			dir := filepath.Join(t.TempDir(), "network")
			if err := Generate(dir, o); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "broker.conf"))
			if err != nil {
				t.Fatal(err)
			}
			if host == "" {
				host = o.BrokerHost
			}
			if !strings.Contains(string(raw), "\nhost: \"127.0.0.1\"\nport: 14224\n") || !strings.Contains(string(raw), "websocket {host: \""+host+"\", port: 18444, tls {") {
				t.Fatal("TCP isolation or independent TLS websocket listener lost")
			}
		})
	}
}

// Run against the deployment's pinned nats-server binary without starting the
// gateway, IAM, PostgreSQL or a Docker stack.
func TestWebsocketListenerIsolation(t *testing.T) {
	binary := os.Getenv("AURIGA_NETWORK_BROKER_TEST_BINARY")
	if binary == "" {
		t.Skip("set AURIGA_NETWORK_BROKER_TEST_BINARY to the pinned deployment broker")
	}
	port := func(host string) int {
		t.Helper()
		listener, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Fatal(err)
		}
		p := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	o := testOptions()
	o.BrokerPort, o.WebsocketPort = port("127.0.0.1"), port("127.0.0.2")
	for o.BrokerPort == o.WebsocketPort {
		o.WebsocketPort = port("127.0.0.2")
	}
	o.WebsocketHost = "127.0.0.2"
	dir := filepath.Join(t.TempDir(), "network")
	o.Root = filepath.ToSlash(dir)
	if err := Generate(dir, o); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, binary, "-c", filepath.Join(dir, "broker.conf"))
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	raw, err := os.ReadFile(filepath.Join(dir, "server-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("invalid server CA")
	}
	address := net.JoinHostPort(o.WebsocketHost, strconv.Itoa(o.WebsocketPort))
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "broker.network.internal"}
	dialer := &net.Dialer{Timeout: 200 * time.Millisecond}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := tls.DialWithDialer(dialer, "tcp4", address, tlsConfig)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("generated broker WSS did not become TLS ready")
		case <-ticker.C:
		}
	}
	conn, err := dialer.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.BrokerPort)))
	if err != nil {
		t.Fatal("TCP loopback listener unavailable")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{
		net.JoinHostPort("127.0.0.1", strconv.Itoa(o.WebsocketPort)),
		net.JoinHostPort("127.0.0.2", strconv.Itoa(o.BrokerPort)),
	} {
		conn, err := dialer.Dial("tcp4", address)
		if err == nil {
			_ = conn.Close()
			t.Fatal("listener leaked onto the other interface")
		}
	}
	wrongName := tlsConfig.Clone()
	wrongName.ServerName = "submit.network.internal"
	if conn, err := tls.DialWithDialer(dialer, "tcp4", address, wrongName); err == nil {
		_ = conn.Close()
		t.Fatal("WSS accepted the submit certificate name")
	} else if !errors.As(err, new(x509.HostnameError)) {
		t.Fatalf("wrong rejection for submit SAN: %v", err)
	}
	untrusted := tlsConfig.Clone()
	untrusted.RootCAs = x509.NewCertPool()
	if conn, err := tls.DialWithDialer(dialer, "tcp4", address, untrusted); err == nil {
		_ = conn.Close()
		t.Fatal("WSS accepted an untrusted server CA")
	} else if !errors.As(err, new(x509.UnknownAuthorityError)) {
		t.Fatalf("wrong rejection for untrusted CA: %v", err)
	}
}
