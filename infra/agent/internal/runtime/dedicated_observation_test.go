package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestDedicatedHostReceiptRequiresExactCurrentResourceAndProfile(t *testing.T) {
	for _, scenario := range []string{"ready", "applying", "restart-required", "failed", "wrong-binding", "wrong-view", "wrong-intent", "wrong-host", "false-ready", "unknown-status", "extra-receipt", "wrong-profile", "extra-capability", "false-capability"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, views := poolSignedFixture(t)
			view := views[0]
			view.Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
			digest := "sha256:" + strings.Repeat("b", 64)
			binding, err := sharednode.HostValue(view.Binding)
			if err != nil {
				t.Fatal(err)
			}
			profiles := 0
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("probe forwarded identity")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/_auriga/dedicated-profile" {
					profiles++
					caps := map[string]any{"pluginInstall": true, "privateSkills": true, "includeDefaultRoots": true, "toolPresentation": "native"}
					value := map[string]any{"schemaVersion": 1, "physicalInstanceId": view.Binding.PhysicalInstanceId, "profile": "auriga.dedicated.v1", "capabilities": caps}
					switch scenario {
					case "wrong-profile":
						value["profile"] = "owner"
					case "extra-capability":
						caps["owner"] = true
					case "false-capability":
						caps["privateSkills"] = false
					}
					_ = json.NewEncoder(w).Encode(value)
					return
				}
				if r.URL.Path != "/_auriga/dedicated-resources" {
					t.Error("unexpected endpoint", r.URL.Path)
				}
				value := map[string]any{"schemaVersion": 1, "binding": binding, "viewId": view.ViewId, "intentDigest": digest, "status": "ready", "reason": "verified", "hostInstanceId": "00000000-0000-4000-8000-000000000099"}
				status := 200
				switch scenario {
				case "applying":
					value["status"], value["reason"], status = "applying", "resource.applying", 503
				case "restart-required":
					value["status"], value["reason"], status = "restart-required", "resource.restart_required", 503
				case "failed":
					value["status"], value["reason"], status = "failed", "resource.apply_failed", 503
				case "wrong-binding":
					binding.(map[string]any)["revision"] = 2
				case "wrong-view":
					value["viewId"] = "00000000-0000-4000-8000-000000000088"
				case "wrong-intent":
					value["intentDigest"] = "sha256:" + strings.Repeat("c", 64)
				case "wrong-host":
					value["hostInstanceId"] = "invalid"
				case "false-ready":
					status = 503
				case "unknown-status":
					value["status"] = "applied"
				case "extra-receipt":
					value["verified"] = true
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer host.Close()
			receipt, err := readDedicatedReceipt(t.Context(), host.URL, view, digest)
			valid := scenario == "ready" || scenario == "applying" || scenario == "restart-required" || scenario == "failed"
			if valid != (err == nil) {
				t.Fatal("receipt acceptance mismatch", scenario, err)
			}
			if valid && scenario != "ready" && (receipt.Status == "ready" || profiles != 0) {
				t.Fatal("non-ready response became active proof")
			}
		})
	}
}
