package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type aiAdminCSRF struct{}

func (aiAdminCSRF) Verify(_ context.Context, raw, token string, sess *Session, _ time.Time) error {
	if raw != "admin" || token != "csrf" || sess.Audience != "admin" {
		return errCSRF
	}
	return nil
}

func closeAIAdminTransports(t *testing.T, g *Gateway) {
	t.Helper()
	for _, tr := range g.APITransports {
		if closer, ok := tr.(interface{ CloseIdleConnections() }); ok {
			t.Cleanup(closer.CloseIdleConnections)
		}
	}
	if closer, ok := g.IAMTransport.(interface{ CloseIdleConnections() }); ok {
		t.Cleanup(closer.CloseIdleConnections)
	}
}

func aiAdminRequest(t *testing.T, g *Gateway, addresses map[string]string, role, method, path string, headers http.Header) (int, http.Header, string) {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+addresses[role]+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Cfg.Access.Mode() == "domain" {
		request.Host = g.Cfg.PortalHost
		if role == "admin" {
			request.Host = g.Cfg.AdminHost
		}
	}
	if role == "instanceControl" {
		request.Host = originAuthority(g.Cfg.Access.InstanceControl().Origin)
	}
	if role == "relayAdmin" {
		request.Host = originAuthority(g.Cfg.LiteLLMUI.Origin)
	}
	request.Header = headers.Clone()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(body)
}
