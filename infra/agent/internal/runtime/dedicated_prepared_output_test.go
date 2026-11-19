package runtime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestDedicatedPreparedOutputHasClosedAuthority(t *testing.T) {
	m, _, a, command := dedicatedLifecycleFixture(t)
	if err := m.View(t.Context(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(m.Pools.Layout.Root, command.Binding.PhysicalInstanceId, "control")
	raw, err := os.ReadFile(filepath.Join(control, "container-state"))
	if err != nil {
		t.Fatal(err)
	}
	var container agentruntime.PoolContainer
	if err = json.Unmarshal(raw, &container); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(container.Meta, "dedicated-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input renderer.DedicatedPreparation
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(container.DedicatedPrepared, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(container.DedicatedPrepared, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "wrong-attempt", "wrong-image", "wrong-binding", "wrong-view", "extra-proof", "extra-file", "config-directory", "wrong-config", "self-sealed-baseline-change", "extra-sdk-entry"} {
		t.Run(scenario, func(t *testing.T) {
			output := filepath.Join(control, "output-test-"+scenario)
			if err := os.Mkdir(output, 0700); err != nil {
				t.Fatal(err)
			}
			var proof map[string]any
			if err := json.Unmarshal(manifest, &proof); err != nil {
				t.Fatal(err)
			}
			bytes := append([]byte(nil), config...)
			switch scenario {
			case "wrong-attempt":
				proof["attemptId"] = "00000000-0000-4000-8000-000000000888"
			case "wrong-image":
				proof["imageId"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "wrong-binding":
				proof["binding"].(map[string]any)["epoch"] = 2
			case "wrong-view":
				proof["view"].(map[string]any)["viewId"] = "00000000-0000-4000-8000-000000000888"
			case "extra-proof":
				proof["ready"] = true
			case "extra-file":
				if err := os.WriteFile(filepath.Join(output, "unapproved"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-config":
				bytes = []byte("[]")
			case "self-sealed-baseline-change", "extra-sdk-entry":
				var rows []map[string]any
				if err := json.Unmarshal(bytes, &rows); err != nil {
					t.Fatal(err)
				}
				if scenario == "extra-sdk-entry" {
					rows = append(rows, map[string]any{"id": "unapproved-sdk", "name": "file:///opt/dsh/node_modules/@deepseek-ai/dsh-jobs-local/lib/index.js"})
				} else {
					for _, row := range rows {
						if row["id"] == "jobs" {
							row["disabled"] = true
						}
					}
				}
				bytes, err = json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				proof["configDigest"], proof["configBytes"] = testDigest(bytes), len(bytes)
			}
			if scenario == "config-directory" {
				if err := os.Mkdir(filepath.Join(output, "config.json"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(output, "config.json"), bytes, 0600); err != nil {
				t.Fatal(err)
			}
			proofBytes, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(output, "manifest.json"), proofBytes, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = agentruntime.VerifyDedicatedPrepared(m.Pools.Layout.Root, container.Meta, output, a.view, a.digest, input.AttemptID, input.ImageID)
			if (scenario == "valid") != (err == nil) {
				t.Fatal("closed output acceptance mismatch", scenario, err)
			}
		})
	}
	// Optional cross-language evidence from the separate public SDK command.
	// Ordinary CI always executes the preceding real caller/negative cases.
	if root := os.Getenv("AURIGA_DEDICATED_PUBLIC_PREPARED"); root != "" {
		meta := filepath.Join(root, "case-input")
		raw, err := os.ReadFile(filepath.Join(meta, "dedicated-input.json"))
		if err != nil {
			t.Fatal(err)
		}
		var current renderer.DedicatedPreparation
		if err = json.Unmarshal(raw, &current); err != nil {
			t.Fatal(err)
		}
		view := new(nodev1.SharedDshResourceView)
		if err = sharednode.DecodeHostValue(current.View, view); err != nil {
			t.Fatal(err)
		}
		if _, err = agentruntime.VerifyDedicatedPrepared(root, meta, filepath.Join(root, "output"), view, current.IntentDigest, current.AttemptID, current.ImageID); err != nil {
			t.Fatal("actual public SDK output rejected by Go Agent", err)
		}
		t.Log("PASS actual Go-rendered helper → real rc.3 SDK/profile/bundle → closed Go Agent readback")
	}
}
