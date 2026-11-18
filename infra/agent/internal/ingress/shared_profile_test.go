package ingress

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSharedInitialProfileUsesOnlyEdgeMTLSAndActualRegisteredHost(t *testing.T) {
	calls := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.RequestURI() != "/_auriga/shared-profile" || r.Method != "GET" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get(identityHeader) != "" || r.Header.Get("X-Extra") != "" {
			t.Error("probe forwarded arbitrary input")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schemaVersion":1,"profile":"auriga.shared.v1"}`))
	}))
	defer host.Close()
	up, _ := url.Parse(host.URL)
	s := New(Config{Shared: &SharedIngress{}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Shared: true, Upstream: up})})
	edge, _ := url.Parse("auriga://edge/fixture")
	request := func(path, method, header string, peer bool) int {
		r := httptest.NewRequest(method, "https://node/i/"+sharedTestID+path, nil)
		if peer {
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{edge}}}}
		} else {
			r.TLS = nil
		}
		if header != "" {
			r.Header.Set(header, "injected")
		}
		r.Header.Set("X-Extra", "removed")
		w := httptest.NewRecorder()
		s.route(w, r)
		return w.Code
	}
	if got := request("/_auriga/shared-profile", "GET", "", true); got != 200 {
		t.Fatal("pending profile unavailable", got)
	}
	for _, header := range []string{"Cookie", "Authorization", "Upgrade"} {
		if request("/_auriga/shared-profile", "GET", header, true) == 200 {
			t.Fatal("credential probe accepted", header)
		}
	}
	if request("/_auriga/shared-profile?x=1", "GET", "", true) == 200 || request("/_auriga/shared-profile", "POST", "", true) == 200 || request("/_auriga/shared-profile", "GET", "", false) == 200 {
		t.Fatal("untrusted probe accepted")
	}
	if calls != 1 {
		t.Fatal("invalid probe reached Host", calls)
	}
}

func TestSharedDedicatedIngressUsesExactPlacementAuthority(t *testing.T) {
	// The signature and live W check accept dedicated through the same path;
	// sharedBinding is also used by the canonical verifier.
	b := sharedTestClaims(true, 1)["binding"].(map[string]any)
	b["mode"] = "dedicated"
	raw, _ := sharedMarshal(b)
	decoded, e := sharedDecode(raw)
	if e != nil || !sharedBinding(decoded) {
		t.Fatal("dedicated binding rejected", e)
	}
}
