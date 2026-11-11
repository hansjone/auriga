package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/egress/internal/policy"
)

func TestPolicyCONNECTAndRevocation(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	go func() {
		for {
			c, e := upstream.Accept()
			if e != nil {
				return
			}
			go func() { defer func() { _ = c.Close() }(); _, _ = io.Copy(c, c) }()
		}
	}()
	s, dial := proxyFixture(t, upstream.Addr().String())
	s.RequirePolicy()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	connect := func(host string) (net.Conn, int) {
		t.Helper()
		c, e := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
		if e != nil {
			t.Fatal(e)
		}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, e = fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
		if e != nil {
			t.Fatal(e)
		}
		return c, r.StatusCode
	}
	c, status := connect("api.example.com")
	_ = c.Close()
	if status != 403 {
		t.Fatal(status)
	}
	s.ApplyPolicy(policy.Value{AllowDomains: []string{"*.example.com"}, DenyDomains: []string{"blocked.example.com"}})
	c, status = connect("api.example.com")
	defer func() { _ = c.Close() }()
	if status != 200 {
		t.Fatal(status)
	}
	if _, err = c.Write([]byte("allowed")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("allowed"))
	if _, err = io.ReadFull(c, response); err != nil || string(response) != "allowed" {
		t.Fatal("tunnel did not carry upstream traffic", string(response), err)
	}
	for _, host := range []string{"blocked.example.com", "other.example.org"} {
		denied, code := connect(host)
		_ = denied.Close()
		if code != 403 {
			t.Fatal(host, code)
		}
	}
	s.ApplyPolicy(policy.Value{})
	var b [1]byte
	_, err = c.Read(b[:])
	if err == nil {
		t.Fatal("old tunnel survived revocation")
	}
	if len(dial.targets()) != 1 {
		t.Fatal(dial.targets())
	}
}

func TestPolicyCannotOverrideAddressOrDeploymentFloor(t *testing.T) {
	for _, address := range []string{"10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1"} { // gitleaks:allow
		s, dial := proxyFixture(t, "127.0.0.1:1")
		s.ApplyPolicy(policy.Value{AllowDomains: []string{"api.example.com"}})
		s.dns = fakeDNS{[]netip.Addr{netip.MustParseAddr(address)}}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodConnect, "http://api.example.com:443", nil)
		r.URL.Scheme = ""
		s.ServeHTTP(w, r)
		if w.Code != 403 || len(dial.targets()) != 0 {
			t.Fatal(address, w.Code, dial.targets())
		}
	}
	s, dial := proxyFixture(t, "127.0.0.1:1")
	if err := s.SetDeniedHosts("example.com"); err != nil {
		t.Fatal(err)
	}
	s.ApplyPolicy(policy.Value{AllowDomains: []string{"*.example.com"}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodConnect, "http://api.example.com:443", nil)
	r.URL.Scheme = ""
	s.ServeHTTP(w, r)
	if w.Code != 403 || len(dial.targets()) != 0 {
		t.Fatal(w.Code)
	}
}
