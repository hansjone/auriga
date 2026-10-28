package main

// Added only to the edge TEST package by go test -overlay. The listener and
// signing key are fixtures; all ingress, device routing and hop TLS use edge.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSEND4EdgeFixture(t *testing.T) {
	configPath := os.Getenv("AURIGA_SEND4_EDGE_CONFIG")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings Config
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteTestIdentityKeys(settings.IdentityKeysFile, "send4-edge"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfigWithAccess(configPath, settings.AccessConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := LoadIdentitySigner(cfg.IdentityKeysFile)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGateway(cfg, NewMemorySessionStore(), NewMemoryRouteStore(), NewMemoryTicketStore(), signer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, component := range g.APITransports {
			if transport, ok := component.(interface{ CloseIdleConnections() }); ok {
				transport.CloseIdleConnections()
			}
		}
		if transport, ok := g.IAMTransport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	})
	pair, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(g.Handler())
	// Same fixed role context as edge's production portal listener. The external
	// authority remains the proxy origin; the internal TLS port is random.
	server.Config.BaseContext = func(net.Listener) context.Context {
		return context.WithValue(context.Background(), accessRoleKey{}, "portal")
	}
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	t.Cleanup(server.Close)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stage": "edge-ready", "https": server.URL, "ca": cfg.NetworkSubmitHop.CAFile,
		"pid": os.Getpid(), "submitUpstream": cfg.Upstreams["network-submit"],
		"serverName": cfg.NetworkSubmitHop.ServerName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

// Exercise the exact child fixture and its JSON configuration without PG/DSH.
// A transport sentinel proves routing only; no successful SEND is fabricated.
func TestSEND4EdgeFixtureLifecycle(t *testing.T) {
	pki := createAccessPKI(t)
	upstream, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ai/v1/network/submit" || r.Header.Get("Authorization") != "Bearer send4-fixture-probe" {
			t.Error("fixture changed submit path or credential")
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	var access map[string]any
	g, _, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("submit reached platform instead of network")
		w.WriteHeader(http.StatusInternalServerError)
	}), func(c *Config, document map[string]any) {
		access = document
		c.Upstreams["network-submit"] = upstream.URL
		c.NetworkSubmitHop = hop
	})
	closeAIAdminTransports(t, g)
	access["portalOrigin"].(map[string]any)["scheme"] = "https"
	access["adminOrigin"].(map[string]any)["scheme"] = "https"
	access["issuerOrigin"] = access["portalOrigin"]
	access["listeners"].(map[string]any)["portal"].(map[string]any)["tls"] = true
	access["listeners"].(map[string]any)["admin"].(map[string]any)["tls"] = true
	dir := t.TempDir()
	accessFile, configFile := filepath.Join(dir, "access.json"), filepath.Join(dir, "edge.json")
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(accessFile, access)
	cfg := g.Cfg
	cfg.AccessConfigFile = accessFile
	cfg.TLS = writeAccessServerTLS(t, pki)
	writeJSON(configFile, cfg)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	child := exec.CommandContext(ctx, executable, "-test.run=^TestSEND4EdgeFixture$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "AURIGA_SEND4_EDGE_CONFIG="+configFile)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	t.Cleanup(func() {
		_ = stdin.Close()
		remaining, readErr := io.ReadAll(reader)
		if err := child.Wait(); err != nil {
			t.Errorf("edge fixture cleanup: %v (read=%v) %s %s", err, readErr, remaining, stderr.String())
		}
	})
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("edge fixture readiness: %v %s", err, stderr.String())
	}
	var ready struct {
		Stage, HTTPS, SubmitUpstream string
		PID                          int
	}
	if err := json.Unmarshal(line, &ready); err != nil || ready.Stage != "edge-ready" || ready.SubmitUpstream != upstream.URL || ready.PID != child.Process.Pid {
		t.Fatalf("edge fixture metadata: %s %v", line, err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pki.roots, ServerName: "iam.internal.test"}}, Timeout: 3 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	request, err := http.NewRequest(http.MethodPost, ready.HTTPS+"/api/ai/v1/network/submit", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = g.Cfg.PortalHost
	request.Header.Set("Authorization", "Bearer send4-fixture-probe")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTeapot {
		t.Fatalf("edge fixture route: status=%d", response.StatusCode)
	}
}
