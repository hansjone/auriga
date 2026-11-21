package ingress

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDedicatedProfileProxyRequiresExactCurrentModeAndClosedCapabilities(t *testing.T) {
	for _, scenario := range []string{"ready", "wrong-mode", "mode-change", "wrong-binding", "extra-capability", "false-capability", "owner-profile", "upstream-failure", "cookie", "connection-upgrade", "no-edge"} {
		t.Run(scenario, func(t *testing.T) {
			authority := &profileModeAuthority{mode: "dedicated"}
			if scenario == "wrong-mode" {
				authority.mode = "shared"
			}
			calls := 0
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.RequestURI() != "/_auriga/dedicated-profile" || r.Header.Get("Cookie") != "" || r.Header.Get(identityHeader) != "" || r.Header.Get("Auriga-Shared-Profile-Probe") != "" {
					t.Error("profile forwarded caller input")
				}
				if scenario == "mode-change" {
					authority.setMode("shared")
				}
				if scenario == "upstream-failure" {
					w.WriteHeader(503)
					return
				}
				caps := map[string]any{"pluginInstall": true, "privateSkills": true, "includeDefaultRoots": true, "toolPresentation": "native"}
				value := map[string]any{"schemaVersion": 1, "physicalInstanceId": sharedTestID, "profile": "auriga.dedicated.v1", "capabilities": caps}
				switch scenario {
				case "wrong-binding":
					value["physicalInstanceId"] = "00000000-0000-4000-8000-000000000002"
				case "extra-capability":
					caps["owner"] = true
				case "false-capability":
					caps["pluginInstall"] = false
				case "owner-profile":
					value["profile"] = "owner"
				}
				raw, err := sharedMarshal(value)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(raw)
			}))
			defer host.Close()
			up, _ := url.Parse(host.URL)
			s := New(Config{Shared: &SharedIngress{Authority: authority}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Shared: true, Upstream: up})})
			r := httptest.NewRequest("GET", "https://node/i/"+sharedTestID+"/_auriga/dedicated-profile", nil)
			edge, _ := url.Parse("auriga://edge/fixture")
			if scenario != "no-edge" {
				r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{edge}}}}
			} else {
				r.TLS = nil
			}
			if scenario == "cookie" {
				r.Header.Set("Cookie", "synthetic-owner-cookie")
			}
			if scenario == "connection-upgrade" {
				r.Header.Set("Connection", "Upgrade")
			}
			r.Header.Set("Auriga-Shared-Profile-Probe", "synthetic-stripped-proof")
			response := httptest.NewRecorder()
			s.route(response, r)
			if scenario == "ready" {
				if response.Code != 200 || calls != 1 {
					t.Fatal("dedicated probe did not reach exact Host", response.Code)
				}
			} else if response.Code == 200 {
				t.Fatal("untrusted profile probe accepted", scenario)
			}
		})
	}
}
