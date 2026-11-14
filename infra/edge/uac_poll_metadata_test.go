package main

import (
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUACChallengePollPreservesFetchMetadata(t *testing.T) {
	const path = "/api/iam/v1/login/uac/challenges/11111111-1111-4111-8111-111111111111"
	for _, domain := range []bool{false, true} {
		for _, site := range []string{"same-origin", "cross-site", ""} {
			t.Run(map[bool]string{false: "ip", true: "domain"}[domain]+"/"+site, func(t *testing.T) {
				binding := testUACSecret(t)
				headers := http.Header{}
				if site != "" {
					headers.Set("Sec-Fetch-Site", site)
					headers.Set("Sec-Fetch-Mode", "cors")
					headers.Set("Sec-Fetch-Dest", "empty")
				}
				var calls atomic.Int32
				g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != path || !strings.Contains(r.Header.Get("Cookie"), binding) {
						t.Error("challenge poll lost its method, path or browser binding")
					}
					for _, name := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
						if !reflect.DeepEqual(r.Header.Values(name), headers.Values(name)) {
							t.Errorf("challenge poll changed %s", name)
						}
					}
					if site != "same-origin" {
						writeProblem(w, problem(http.StatusForbidden, "common.forbidden", "Forbidden", "fetch metadata rejected", ""))
						return
					}
					w.WriteHeader(http.StatusOK)
				}))
				headers.Set("Cookie", "auriga_portal_login_tx="+binding)
				status, body := entryRequest(t, addresses["portal"], http.MethodGet, path, g.Cfg.PortalHost, headers)
				want := http.StatusForbidden
				if site == "same-origin" {
					want = http.StatusOK
				}
				if calls.Load() != 1 || status != want || want == http.StatusForbidden && !strings.Contains(body, "common.forbidden") {
					t.Fatalf("challenge poll: upstream calls=%d, status=%d", calls.Load(), status)
				}
			})
		}
	}
}
