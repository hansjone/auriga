package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIngressLocalSocketMTLS(t *testing.T) {
	g, _, routes, route, cookie := instanceFixture(t, http.NotFoundHandler())
	cfg := g.Cfg
	node := testNode(t, &cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("missing verified client")
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}), testNodeID)
	local, endpoint := localTestListener(t)
	srv := &http.Server{Handler: node.Config.Handler}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(tls.NewListener(local, node.TLS.Clone())) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Errorf("local server: %v", err)
		}
	})
	var err error
	g.Ingress.Close()
	g.Ingress, err = loadNodeIngress(cfg.NodeTLS)
	if err != nil {
		t.Fatal(err)
	}
	g.Ingress.allowLocal = cfg.AllowLocalIngress
	t.Cleanup(g.Ingress.Close)
	route.Ingress = endpoint
	routes.Put(route)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest(cookie, "/local"))
	if w.Code != 200 || w.Body.String() != "/i/"+testInstanceID+"/local" {
		t.Fatalf("local %d %s", w.Code, w.Body.String())
	}
}

func TestIngressUploadLimit(t *testing.T) {
	g, _, _, _, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); w.WriteHeader(204) }))
	r := instanceRequest(cookie, "/upload")
	r.Method = http.MethodPost
	r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 2<<20)))
	r.ContentLength = 2 << 20
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("2 MiB upload rejected: %d", w.Code)
	}
	r = instanceRequest(cookie, "/upload")
	r.Method = http.MethodPost
	r.ContentLength = maxInstanceBodyBytes + 1
	r.Body = io.NopCloser(strings.NewReader("x"))
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized upload: %d", w.Code)
	}
	r = instanceRequest(cookie, "/upload")
	r.Method = http.MethodPost
	r.ContentLength = -1
	r.Body = io.NopCloser(io.LimitReader(zeroBody{}, maxInstanceBodyBytes+1))
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("unknown length oversized upload: %d %s", w.Code, w.Body.String())
	}
}

type zeroBody struct{}

func (zeroBody) Read(p []byte) (int, error) { clear(p); return len(p), nil }
