package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/egress/internal/policy"
)

func TestUpgradeUnconfiguredAndOpenTunnelToControlledHTTPSUpstream(t *testing.T) {
	for _, mode := range []string{"", "open"} {
		t.Run("mode="+mode, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled-upstream") }))
			t.Cleanup(upstream.Close)
			address, _ := url.Parse(upstream.URL)
			server, dial := proxyFixture(t, address.Host)
			server.ApplyPolicy(policy.Value{Mode: mode})
			proxy := httptest.NewServer(server)
			t.Cleanup(proxy.Close)
			proxyURL, _ := url.Parse(proxy.URL)
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: address.Hostname(), MinVersion: tls.VersionTLS12}}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get("https://api.example.com/probe")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != 200 || string(body) != "controlled-upstream" {
				t.Fatal("controlled HTTPS request failed", err)
			}
			if got := dial.targets(); len(got) != 1 || got[0] != "8.8.8.8:443" {
				t.Fatal("destination was not validated and pinned", got)
			}
		})
	}
}

func TestOpenStillRejectsClashFakeDNSBeforeDial(t *testing.T) {
	server, dial := proxyFixture(t, "127.0.0.1:1")
	server.dns = fakeDNS{[]netip.Addr{netip.MustParseAddr("198.18.0.44")}}
	server.ApplyPolicy(policy.Value{Mode: "open"})
	request := httptest.NewRequest(http.MethodConnect, "http://api.example.com:443", nil)
	request.Host = "api.example.com:443"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 403 || len(dial.targets()) != 0 {
		t.Fatal("fake DNS must be rejected before dial")
	}
	t.Log("198.18.0.44 rejected by reserved 198.18.0.0/15 destination guard")
}
