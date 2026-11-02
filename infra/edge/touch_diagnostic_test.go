package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTouchDiagnosticsKeepStatusAndProjectionWithoutSecrets(t *testing.T) {
	for _, mode := range []string{"normal", "rpc", "inactive", "projection"} {
		t.Run(mode, func(t *testing.T) {
			g, _, store := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			server, _ := browserRPCFixture(t, g, store)
			row := putBrowserSession(g, server, "touch-cookie", "portal")
			var output bytes.Buffer
			g.Log = newLogLogger(&output)
			if mode == "rpc" {
				server.mode = "touch-error"
			}
			if mode == "inactive" {
				server.inactiveTouch = true
			}
			if mode == "projection" {
				server.onTouch = func() { bad := *row; bad.AccountID = "different"; store.Put("touch-cookie", &bad) }
			}
			_, firstErr := g.touchBrowserSession(context.Background(), "touch-cookie", row)
			if (firstErr == nil) != (mode == "normal") {
				t.Fatalf("unexpected decision %v", firstErr)
			}
			if mode == "projection" {
				store.Put("touch-cookie", row)
			}
			_, secondErr := g.touchBrowserSession(context.Background(), "touch-cookie", row)
			if (secondErr == nil) != (mode == "normal" || mode == "projection") {
				t.Fatalf("cached decision %v", secondErr)
			}
			if server.touches != 1 {
				t.Fatal("diagnostics changed single flight/window")
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("logs: %s", output.String())
			}
			var first, second map[string]any
			if json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil {
				t.Fatal("invalid JSON")
			}
			if first["session_id"] != row.SessionID || second["cache_hit"] != true {
				t.Fatal("lost context")
			}
			if mode == "rpc" && (first["grpc_status"] != "Unavailable" || !strings.Contains(first["error"].(string), "connection reset")) {
				t.Fatal("lost original RPC error")
			}
			if mode == "inactive" && first["reason"] != "inactive" {
				t.Fatal("inactive not classified")
			}
			if mode == "projection" && (first["projection"] != "rejected" || second["projection"] != "valid" || second["reason"] != "ok") {
				t.Fatal("projection failure poisoned cache diagnostics")
			}
			for _, secret := range []string{"touch-cookie", "sentinel-password", "sentinel-ticket"} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("secret in final logger JSON")
				}
			}
		})
	}
}

func TestTouchDiagnosticMaskOnlySecrets(t *testing.T) {
	for _, value := range []string{`ticket="sentinel-ticket with spaces"`, "-----BEGIN PRIVATE KEY-----\nsentinel-key", "amc.fake.sentinel-managed", "AURIGA_FOO_TOKEN=sentinel-env"} {
		masked := Mask("connection reset " + value)
		if strings.Contains(masked, "sentinel-") || !strings.Contains(masked, "connection reset") {
			t.Fatal("lost useful message or leaked secret")
		}
	}
}
