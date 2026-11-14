package network

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

type projectedBrokerDialer struct {
	runtime    *projectionRuntime
	serverName string
}

// NATS must not wrap the connection a second time: Dial already completes a
// fully verified TLS handshake against the current immutable snapshot.
func (d *projectedBrokerDialer) SkipTLSHandshake() bool { return true }

type prefixedConn struct {
	net.Conn
	prefix *bytes.Reader
}

func (c *prefixedConn) Read(b []byte) (int, error) {
	if c.prefix.Len() > 0 {
		return c.prefix.Read(b)
	}
	return c.Conn.Read(b)
}
func (d *projectedBrokerDialer) Dial(network, address string) (net.Conn, error) {
	snap := d.runtime.snapshot.Load()
	if snap == nil || d.runtime.reader.Withdrawn(time.Now()) {
		return nil, errors.New("network: broker TLS projection pending")
	}
	if e := d.runtime.checkLocalSnapshot(snap); e != nil {
		return nil, e
	}
	cert, ok := snap.TLS.Leaves["broker-client"]
	if !ok {
		return nil, errors.New("network: broker client leaf unavailable")
	}
	raw, e := net.DialTimeout(network, address, 5*time.Second)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
		}
	}()
	if e = raw.SetDeadline(time.Now().Add(5 * time.Second)); e != nil {
		return nil, e
	}
	// Repository-locked NATS native TLS sends a bounded INFO before STARTTLS. Preserve the
	// exact line for nats.go after upgrading this same connection.
	reader := bufio.NewReaderSize(raw, 16384)
	line, e := reader.ReadSlice('\n')
	if e != nil || !bytes.HasPrefix(line, []byte("INFO ")) || reader.Buffered() != 0 {
		return nil, errors.New("network: invalid broker TLS greeting")
	}
	var info struct {
		Required bool `json:"tls_required"`
	}
	if json.Unmarshal(bytes.TrimSpace(line[5:]), &info) != nil || !info.Required {
		return nil, errors.New("network: broker must require TLS")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: d.serverName, RootCAs: snap.TLS.ServerRoots, Certificates: []tls.Certificate{cert}}
	secure := tls.Client(raw, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = secure.HandshakeContext(ctx); e != nil {
		return nil, errors.New("network: broker mTLS handshake rejected")
	}
	if len(secure.ConnectionState().VerifiedChains) == 0 {
		return nil, errors.New("network: broker verified chain missing")
	}
	if e = secure.SetDeadline(time.Time{}); e != nil {
		return nil, e
	}
	select {
	case d.runtime.handshakes <- handshakeObservation{Selector: snap.Selector, Role: "broker-client"}:
	default:
	}
	success = true
	return &prefixedConn{Conn: secure, prefix: bytes.NewReader(append([]byte(nil), line...))}, nil
}
func (r *Runtime) dialBroker(c NATSConfig, closed func(*nats.Conn)) (*nats.Conn, error) {
	if r.projection == nil {
		return dialNATS(c, closed)
	}
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "tls" || u.Host == "" || u.User != nil || c.Username == "" || c.PasswordFile == "" {
		return nil, errors.New("network: projected broker requires fixed TLS endpoint and identity")
	}
	password, e := os.ReadFile(c.PasswordFile)
	if e != nil {
		return nil, e
	}
	defer clear(password)
	if strings.TrimSpace(string(password)) == "" {
		return nil, errors.New("network: broker identity unavailable")
	}
	return nats.Connect(c.URL, nats.UserInfo(c.Username, strings.TrimSpace(string(password))), nats.SetCustomDialer(&projectedBrokerDialer{runtime: r.projection, serverName: u.Hostname()}), nats.Timeout(5*time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second), nats.ClosedHandler(closed))
}
