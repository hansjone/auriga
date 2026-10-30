package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func readyNavigation(rawURL string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, rawURL, nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	return r
}

func assertInstanceWaiting(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("expected retry page: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("waiting must not cache, redirect or issue cookies: %v", w.Header())
	}
}

func TestOpenWaitsForRunningProjection(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(map[bool]string{true: "domain", false: "path"}[domain], func(t *testing.T) {
			var forwarded atomic.Int64
			g, _, routes, route, _ := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				forwarded.Add(1)
				_, _ = w.Write([]byte("DSH ready"))
			}))
			if !domain {
				g.Cfg.InstanceParentDomain = ""
			}
			instanceID := pathInstanceA
			route.InstanceID = instanceID
			route.State = "starting" // DB may already be READY; edge sees only the lagging KV projection.
			routes.Put(route)
			open := func() *httptest.ResponseRecorder {
				r := readyNavigation("http://portal.example.com/open/" + instanceID)
				r.AddCookie(&http.Cookie{Name: "auriga_session", Value: "session"})
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, r)
				return w
			}
			assertInstanceWaiting(t, open())
			assertInstanceWaiting(t, open()) // Retrying does not grant entry while KV is still behind.
			if forwarded.Load() != 0 {
				t.Fatal("starting instance was contacted")
			}
			route.State = "running" // Advance the projection explicitly; no timers or sleeps.
			routes.Put(route)
			w := open()
			if w.Code != http.StatusFound {
				t.Fatalf("running open: %d %s", w.Code, w.Body.String())
			}
			r := readyNavigation(w.Header().Get("Location"))
			for _, cookie := range w.Result().Cookies() {
				r.AddCookie(cookie)
			}
			w = httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusFound {
				t.Fatalf("redeem: %d %s", w.Code, w.Body.String())
			}
			root := "http://" + instanceID + ".i.example.com/"
			if !domain {
				root = "http://portal.example.com/i/" + instanceID + "/"
			}
			r = readyNavigation(root)
			for _, cookie := range w.Result().Cookies() {
				r.AddCookie(cookie)
			}
			w = httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != "DSH ready" || forwarded.Load() != 1 {
				t.Fatalf("first root: %d %s, forwarded=%d", w.Code, w.Body.String(), forwarded.Load())
			}
		})
	}
}

func TestInstanceRootWaitsForRunningProjection(t *testing.T) {
	var forwarded atomic.Int64
	g, _, routes, route, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	get := func() *httptest.ResponseRecorder {
		r := readyNavigation("http://" + testInstanceID + ".i.example.com/")
		r.AddCookie(&http.Cookie{Name: "auriga_inst", Value: cookie})
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w
	}
	// Covers an existing binding or a state change between ticket issue and root GET.
	route.State = "starting"
	routes.Put(route)
	assertInstanceWaiting(t, get())
	if forwarded.Load() != 0 {
		t.Fatal("starting instance was contacted")
	}
	route.State = "running"
	routes.Put(route)
	w := get()
	if w.Code != http.StatusNoContent || forwarded.Load() != 1 {
		t.Fatalf("running root: %d, forwarded=%d", w.Code, forwarded.Load())
	}
}

func TestInstanceWaitingPreservesFailures(t *testing.T) {
	g, sessions, routes, route, cookie := instanceFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("non-running instance was contacted")
	}))
	for _, tc := range []struct {
		name, state, method, accept, mode, path string
		want                                    int
	}{
		{"stopped", "stopped", "GET", "text/html", "navigate", "/", 503},
		{"failed", "failed", "GET", "text/html", "navigate", "/", 503},
		{"api", "starting", "GET", "application/json", "", "/", 503},
		{"fetch", "starting", "GET", "text/html", "cors", "/", 503},
		{"asset", "starting", "GET", "text/html", "navigate", "/app.js", 503},
		{"write", "starting", "POST", "text/html", "navigate", "/", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route.State = tc.state
			routes.Put(route)
			r := httptest.NewRequest(tc.method, "http://"+testInstanceID+".i.example.com"+tc.path, nil)
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("Sec-Fetch-Mode", tc.mode)
			r.AddCookie(&http.Cookie{Name: "auriga_inst", Value: cookie})
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), "edge.instance_not_running") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, entry := range []string{"open", "root"} {
		for _, failure := range []string{"anonymous", "owner", "tenant", "missing", "unavailable", "revoked"} {
			t.Run(entry+"/"+failure, func(t *testing.T) {
				route.State = "starting"
				badRoute := *route
				want := http.StatusForbidden
				switch failure {
				case "owner":
					badRoute.OwnerID = "55555555-5555-5555-5555-555555555555"
				case "tenant":
					badRoute.TenantID = "55555555-5555-5555-5555-555555555555"
				case "anonymous", "revoked":
					want = http.StatusUnauthorized
				case "unavailable":
					want = http.StatusServiceUnavailable
				}
				routes.Put(&badRoute)
				g.Routes = routes
				g.Owners = RouteOwnerChecker{Routes: routes}
				if failure == "missing" || failure == "unavailable" {
					var store RouteStore = NewMemoryRouteStore()
					if failure == "unavailable" {
						store = &failingRoutes{err: ErrRouteUnavailable}
					}
					g.Routes = store
					g.Owners = RouteOwnerChecker{Routes: store}
				}
				if failure == "revoked" {
					s := putPortalSession(sessions, "session", "csrf", "token")
					s.Revoked = true
					sessions.Put("session", s)
				} else {
					putPortalSession(sessions, "session", "csrf", "token")
				}
				rawURL := "http://" + testInstanceID + ".i.example.com/"
				name, value := "auriga_inst", cookie
				if entry == "open" {
					rawURL = "http://portal.example.com/open/" + testInstanceID
					name, value = "auriga_session", "session"
				}
				r := readyNavigation(rawURL)
				if failure != "anonymous" {
					r.AddCookie(&http.Cookie{Name: name, Value: value})
				}
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, r)
				if w.Code != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
					t.Fatalf("%d %s, want %d", w.Code, w.Body.String(), want)
				}
			})
		}
	}
}

func TestIPInstanceWaitUsesAuthenticatedEntry(t *testing.T) {
	var forwarded atomic.Int64
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		_, _ = w.Write([]byte("DSH ready"))
	}))
	routes := b.g.Routes.(*MemoryRouteStore)
	route, err := routes.Get(t.Context(), pathInstanceA)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, want int) string {
		r, err := http.NewRequest(http.MethodGet, b.root+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept", "text/html")
		resp, err := b.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body := readIPResponse(t, resp, want, "")
		if want == http.StatusOK && forwarded.Load() == 0 && !strings.Contains(body, `http-equiv="refresh"`) {
			t.Fatalf("missing retry page: %s", body)
		}
		return body
	}
	route.State = "starting"
	routes.Put(route)
	get("/open/"+pathInstanceA, http.StatusOK)
	route.State = "running"
	routes.Put(route)
	b.open(t, pathInstanceA)
	// The instance binding remains valid if the projection changes after /open.
	route.State = "starting"
	routes.Put(route)
	get("/i/"+pathInstanceA+"/", http.StatusOK)
	route.State = "running"
	routes.Put(route)
	if body := get("/i/"+pathInstanceA+"/", http.StatusOK); body != "DSH ready" || forwarded.Load() != 1 {
		t.Fatalf("first routed root: %q forwarded=%d", body, forwarded.Load())
	}
}

func TestOpenNonRunningOnlyWaitsForDocumentGET(t *testing.T) {
	g, _, routes, route, _ := instanceFixture(t, http.NotFoundHandler())
	for _, tc := range []struct{ state, method, accept string }{
		{"starting", "GET", "application/json"},
		{"starting", "GET", "text/html;q=0.0"},
		{"starting", "POST", "text/html"},
		{"stopped", "GET", "text/html"},
		{"failed", "GET", "text/html"},
	} {
		t.Run(tc.state+"/"+tc.method+"/"+tc.accept, func(t *testing.T) {
			route.State = tc.state
			routes.Put(route)
			r := httptest.NewRequest(tc.method, "http://portal.example.com/open/"+testInstanceID, nil)
			r.Header.Set("Accept", tc.accept)
			r.AddCookie(&http.Cookie{Name: "auriga_session", Value: "session"})
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "edge.instance_not_running") || len(w.Result().Cookies()) != 0 {
				t.Fatalf("non-running open: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
		})
	}
}
