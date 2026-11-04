package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDial struct {
	address string
	mu      sync.Mutex
	seen    []string
}

func (d *fakeDial) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.seen = append(d.seen, address)
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}

func (d *fakeDial) targets() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.seen...)
}

func proxyFixture(t *testing.T, address string) (*Server, *fakeDial) {
	t.Helper()
	s := New()
	s.dns = fakeDNS{[]netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	dial := &fakeDial{address: address}
	s.dial = dial
	return s, dial
}

func TestCONNECTPinsDestinationPreservesBufferedTLSAndHalfClose(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	remoteDone := make(chan error, 1)
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			remoteDone <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		body, readErr := io.ReadAll(connection)
		if readErr != nil {
			remoteDone <- readErr
			return
		}
		_, writeErr := fmt.Fprintf(connection, "reply:%s", body)
		remoteDone <- writeErr
	}()
	s, dial := proxyFixture(t, upstream.Addr().String())
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(connection, "CONNECT api.example.com:443 HTTP/1.1\r\nHost: api.example.com:443\r\n\r\nearly-tls")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal(response, err)
	}
	if err = connection.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "reply:early-tls" {
		t.Fatal(string(body), err)
	}
	if err = <-remoteDone; err != nil {
		t.Fatal(err)
	}
	if got := dial.targets(); len(got) != 1 || got[0] != "8.8.8.8:443" {
		t.Fatal("hostname was re-resolved at dial time", got)
	}
}

func TestCONNECTRejectsUnsafeAndNonCONNECTBeforeDial(t *testing.T) {
	s, dial := proxyFixture(t, "127.0.0.1:1")
	for _, raw := range []string{"http://api.example.com/", "http://api.example.com/health?other=1", "http://127.0.0.1/"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, raw, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal(w.Code)
		}
	}
	for _, authority := range []string{"127.0.0.1:443", "api.example.com:80"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodConnect, "http://"+authority, nil)
		r.URL.Scheme = ""
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal(w.Code)
		}
	}
	s.dns = fakeDNS{[]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodConnect, "http://api.example.com:443", nil)
	r.URL.Scheme, r.URL.Path = "", ""
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(dial.targets()) != 0 {
		t.Fatal("mixed DNS reached dial", w.Code, dial.targets())
	}
}

func TestPublicManagementOriginsAreDeniedBeforeDNSOrDial(t *testing.T) {
	s, dial := proxyFixture(t, "127.0.0.1:1")
	if err := s.SetDeniedHosts("portal.example.com,admin.example.com,192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"portal.example.com", "ADMIN.EXAMPLE.COM.", "child.admin.example.com"} {
		r := httptest.NewRequest(http.MethodConnect, "http://"+host+":443", nil)
		r.URL.Scheme, r.URL.Path = "", ""
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || len(dial.targets()) != 0 {
			t.Fatal("protected public origin proxied", host, w.Code)
		}
	}
	for _, csv := range []string{"", "*", "admin.example.com:443", ",admin.example.com"} {
		if err := s.SetDeniedHosts(csv); err == nil {
			t.Fatal("invalid protected host configuration accepted", csv)
		}
	}
}

func TestServeCancellationClosesIdleTunnels(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upstream.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	s, _ := proxyFixture(t, upstream.Addr().String())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Serve(ctx, listener) }()
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(connection, "CONNECT api.example.com:443 HTTP/1.1\r\nHost: api.example.com:443\r\n\r\n")
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal(response, err)
	}
	remote := <-accepted
	defer func() { _ = remote.Close() }()
	cancel()
	if _, err = reader.ReadByte(); err != io.EOF {
		t.Fatal("tunnel stayed open after cancellation", err)
	}
	if err = <-stopped; err != nil {
		t.Fatal(err)
	}
}
