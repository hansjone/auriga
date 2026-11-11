package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestOpenNonceInterleaving(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, order := range []string{"AB", "BA", "failA"} {
			t.Run(order, func(t *testing.T) {
				b := newIPBrowser(t, secure, http.NotFoundHandler())
				a := b.get(t, "/open/"+pathInstanceA)
				readIPResponse(t, a, 302, "")
				c := b.get(t, "/open/"+pathInstanceA)
				readIPResponse(t, c, 302, "")
				locA, locB := a.Header.Get("Location"), c.Header.Get("Location")
				if order == "failA" {
					u := a.Cookies()[0]
					u.Value = "wrong-nonce"
					target, _ := a.Location()
					b.client.Jar.SetCookies(target, []*http.Cookie{u})
					resp, err := b.client.Get(locA)
					if err != nil {
						t.Fatal(err)
					}
					readIPResponse(t, resp, 403, "edge.bad_ticket_nav")
					resp, err = b.client.Get(locB)
					if err != nil {
						t.Fatal(err)
					}
					readIPResponse(t, resp, 302, "")
					return
				}
				locs := []string{locA, locB}
				if order == "BA" {
					locs = []string{locB, locA}
				}
				for _, loc := range locs {
					resp, err := b.client.Get(loc)
					if err != nil {
						t.Fatal(err)
					}
					readIPResponse(t, resp, 302, "")
				}
			})
		}
	}
}

func TestOpenNonceFlowLimit(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	var locations []string
	var firstName string
	for i := 0; i < maxOpenFlows+1; i++ {
		resp := b.get(t, "/open/"+pathInstanceA)
		readIPResponse(t, resp, 302, "")
		locations = append(locations, resp.Header.Get("Location"))
		if i == 0 {
			firstName = resp.Cookies()[0].Name
		}
		if i == maxOpenFlows {
			found := false
			for _, c := range resp.Cookies() {
				if c.Name == firstName && c.MaxAge < 0 {
					found = true
				}
			}
			if !found {
				t.Fatal("oldest flow cookie not expired")
			}
		}
	}
	u, _ := url.Parse(locations[0])
	if len(b.client.Jar.Cookies(u)) != maxOpenFlows+1 {
		t.Fatal("browser did not bound outstanding cookies")
	}
	for _, loc := range locations[1:] {
		resp, err := b.client.Get(loc)
		if err != nil {
			t.Fatal(err)
		}
		readIPResponse(t, resp, 302, "")
		if len(resp.Cookies()) != 2 || resp.Cookies()[0].MaxAge != -1 || resp.Cookies()[0].Path != instanceMount(pathInstanceA) {
			t.Fatal("redemption cleanup escaped flow")
		}
	}
	if _, err := b.g.Tickets.Redeem(strings.SplitN(u.Query().Get("t"), ".", 2)[1], strings.SplitN(u.Query().Get("t"), ".", 2)[0]); err == nil {
		t.Fatal("evicted ticket accepted")
	}
}

func TestOpenNonceWrongFlowKeepsOtherOpen(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	a := b.get(t, "/open/"+pathInstanceA)
	readIPResponse(t, a, 302, "")
	c := b.get(t, "/open/"+pathInstanceA)
	readIPResponse(t, c, 302, "")
	au, _ := a.Location()
	bu, _ := c.Location()
	q := au.Query()
	q.Set("t", strings.SplitN(bu.Query().Get("t"), ".", 2)[0]+"."+strings.SplitN(au.Query().Get("t"), ".", 2)[1])
	au.RawQuery = q.Encode()
	resp, err := b.client.Get(au.String())
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 401, "edge.bad_ticket")
	if len(resp.Cookies()) != 0 {
		t.Fatal("wrong flow deleted another nonce")
	}
	for _, loc := range []string{a.Header.Get("Location"), c.Header.Get("Location")} {
		resp, err = b.client.Get(loc)
		if err != nil {
			t.Fatal(err)
		}
		readIPResponse(t, resp, 302, "")
	}
}

func TestOpenNonceDiagnostics(t *testing.T) {
	for _, state := range []string{"missing", "empty", "duplicate", "single"} {
		t.Run(state, func(t *testing.T) {
			b := newIPBrowser(t, false, http.NotFoundHandler())
			var output bytes.Buffer
			b.g.Log = newLogLogger(&output)
			opened := b.get(t, "/open/"+pathInstanceA)
			readIPResponse(t, opened, 302, "")
			c := *opened.Cookies()[0]
			output.Reset()
			req, err := http.NewRequest("GET", opened.Header.Get("Location"), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("traceparent", "00-12345678901234567890123456789012-1234567890123456-01")
			count := 0
			switch state {
			case "empty":
				c.Value = ""
				req.AddCookie(&c)
				count = 1
			case "duplicate":
				req.AddCookie(&c)
				req.AddCookie(&c)
				count = 2
			case "single":
				c.Value = "wrong-secret-nonce"
				req.AddCookie(&c)
				count = 1
			}
			raw := *b.client
			raw.Jar = nil
			resp, err := raw.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			readIPResponse(t, resp, 403, "edge.bad_ticket_nav")
			if len(resp.Cookies()) != 1 || resp.Cookies()[0].Name != c.Name {
				t.Fatal("cleanup is not flow scoped")
			}
			found := false
			for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var row map[string]any
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatal(err)
				}
				if row["msg"] != "Open nonce exchange" {
					continue
				}
				found = true
				if row["nonce_state"] != state || row["nonce_count"] != float64(count) || row["nonce_name"] != c.Name || row["trace_id"] != "12345678901234567890123456789012" || row["request_id"] == "" {
					t.Fatalf("missing diagnostic fields: %v", row)
				}
				want := "rejected"
				if state == "single" {
					want = "mismatch"
				}
				if row["outcome"] != want {
					t.Fatal("missing outcome")
				}
			}
			if !found {
				t.Fatal("no nonce diagnostic")
			}
			u, _ := opened.Location()
			for _, secret := range []string{opened.Cookies()[0].Value, "wrong-secret-nonce", u.Query().Get("t"), opened.Header.Get("Location")} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("secret in log")
				}
			}
		})
	}
}

func TestOpenNonceInvalidFlow(t *testing.T) {
	for _, flow := range []string{"", "short", strings.Repeat("a", 17), strings.Repeat("a", 15) + "!"} {
		t.Run(flow, func(t *testing.T) {
			b := newIPBrowser(t, false, http.NotFoundHandler())
			a := b.get(t, "/open/"+pathInstanceA)
			readIPResponse(t, a, 302, "")
			u, _ := a.Location()
			q := u.Query()
			q.Set("t", flow+"."+strings.SplitN(q.Get("t"), ".", 2)[1])
			u.RawQuery = q.Encode()
			resp, err := b.client.Get(u.String())
			if err != nil {
				t.Fatal(err)
			}
			readIPResponse(t, resp, 400, "edge.bad_ticket")
			if len(resp.Cookies()) != 0 {
				t.Fatal("invalid flow set cookie")
			}
			resp, err = b.client.Get(a.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			readIPResponse(t, resp, 302, "")
		})
	}
}

func TestOpenNonceLifecycleLog(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	var output bytes.Buffer
	b.g.Log = newLogLogger(&output)
	b.open(t, pathInstanceA)
	for _, outcome := range []string{"issued", "redeemed"} {
		if !strings.Contains(output.String(), `"outcome":"`+outcome+`"`) {
			t.Fatal("missing lifecycle outcome")
		}
	}
}
