package main

import (
	"context"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Real HTTP and WebSocket through the browser listener, with only IAM/node
// replaced. Revocation is committed by the IAM substitute before logout returns.
func TestUACK2LogoutClosesInstanceHTTPAndWebSocket(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			sessionCookie, csrf, suppression := testUACSecret(t), testUACSecret(t), testUACSecret(t)
			reference, _ := navigationRequestID()
			var sessions *MemorySessionStore
			var sid string
			var node *httptest.Server
			var receivedSuppression bool
			iam := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/iam/v1/logout" {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get(uacCorrelationHeader) != "" {
					t.Error("browser correlation header trusted")
				}
				value, err := r.Cookie("auriga_portal_sso_suppression")
				receivedSuppression = err == nil && value.Value == suppression
				sessions.Revoke(sid)
				http.SetCookie(w, &http.Cookie{Name: "auriga_portal_sso_suppression", Value: suppression, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
				w.Header().Set(uacCorrelationHeader, reference)
				w.WriteHeader(204)
			})
			ws := websocket.Server{Handshake: func(_ *websocket.Config, _ *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
				defer func() { _ = conn.Close() }()
				for {
					var value string
					if websocket.Message.Receive(conn, &value) != nil {
						return
					}
					if websocket.Message.Send(conn, value) != nil {
						return
					}
				}
			}}
			g, addresses, store := accessFixtureConfigured(t, domain, iam, func(cfg *Config, _ map[string]any) {
				node = testNode(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/ws") {
						ws.ServeHTTP(w, r)
						return
					}
					_, _ = w.Write([]byte("instance"))
				}), testNodeID)
				t.Cleanup(node.Close)
			})
			sessions = store
			sess := putPortalSession(sessions, sessionCookie, csrf, testUACSecret(t))
			sess.TenantID = g.Cfg.Access.TenantID()
			sessions.Put(sessionCookie, sess)
			sid = sess.SessionID
			id := "019a0000-0000-7000-8000-000000000001"
			g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: id, TenantID: sess.TenantID, OwnerID: sess.AccountID, NodeID: testNodeID, Ingress: node.URL, State: "running", Generation: 1, UpdatedAt: time.Now()})
			origin, mount := g.Cfg.Access.PortalURL(), instanceMount(id)
			if domain {
				o := g.Cfg.Access.PortalOrigin()
				o.Host = id + "." + g.Cfg.Access.InstanceParentDomain()
				origin = o.Scheme + "://" + originAuthority(o)
				mount = "/"
			}
			binding, err := g.Tickets.BindInstance(sid, HashCookie(sessionCookie), sess.AccountID, sess.TenantID, id, sess.ExpiresAt, InstanceTarget{Origin: origin, Mount: mount})
			if err != nil {
				t.Fatal(err)
			}
			instanceCookie := instanceCookieName(false, "/i/") + "=" + binding.CookieRaw
			base := origin + mount
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, addresses["portal"])
			}}}
			defer client.CloseIdleConnections()
			get := func() int {
				req, err := http.NewRequest(http.MethodGet, base+"page", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Cookie", instanceCookie)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				_, _ = io.Copy(io.Discard, resp.Body)
				return resp.StatusCode
			}
			if status := get(); status != 200 {
				t.Fatalf("instance read: %d", status)
			}
			config, err := websocket.NewConfig(strings.Replace(base+"ws", "http:", "ws:", 1), origin)
			if err != nil {
				t.Fatal(err)
			}
			config.Header.Set("Cookie", instanceCookie)
			dial := func() (*websocket.Conn, error) {
				raw, err := net.DialTimeout("tcp", addresses["portal"], 3*time.Second)
				if err != nil {
					return nil, err
				}
				_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
				conn, err := websocket.NewClient(config, raw)
				if err != nil {
					_ = raw.Close()
				}
				return conn, err
			}
			conn, err := dial()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := websocket.Message.Send(conn, "ping"); err != nil {
				t.Fatal(err)
			}
			var echo string
			if err := websocket.Message.Receive(conn, &echo); err != nil || echo != "ping" {
				t.Fatal("instance websocket was not usable")
			}
			req, err := http.NewRequest(http.MethodPost, g.Cfg.Access.PortalURL()+"/api/iam/v1/logout", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", g.Cfg.Access.PortalURL())
			req.Header.Set("X-Auriga-CSRF", csrf)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", sessionCookieBase("portal")+"="+sessionCookie+"; auriga_csrf="+csrf+"; auriga_portal_sso_suppression="+suppression)
			req.Header.Set(uacCorrelationHeader, testUACSecret(t))
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 204 || resp.Header.Get(uacCorrelationHeader) != reference || !receivedSuppression {
				t.Fatalf("logout route failed: %d", resp.StatusCode)
			}
			cookies := resp.Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].MaxAge != 0 || !cookies[0].Expires.IsZero() || cookies[0].Domain != "" || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("suppression cookie policy changed at edge")
			}
			if err := websocket.Message.Receive(conn, &echo); err == nil {
				t.Fatal("revoked websocket remained open")
			}
			if status := get(); status != 401 {
				t.Fatalf("old HTTP binding accepted: %d", status)
			}
			replay, err := dial()
			if err == nil {
				_ = replay.Close()
				t.Fatal("revoked websocket reconnected")
			}
		})
	}
}
