package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestUnifiedR8SafetyIsolationSurvivesDisableAndRestart(t *testing.T) {
	for _, reason := range []string{"lease_expired", "revoked", "verification_failed", "clock_rollback", "identity_changed", "cleanup_failed"} {
		for _, interrupted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interrupted=%t", reason, interrupted), func(t *testing.T) {
				f := newUnifiedFixture(t)
				current, err := f.manager.Prepare(t.Context(), f.source.spec)
				if err != nil {
					t.Fatal(err)
				}
				f.driver.stopFails = interrupted
				if reason == "lease_expired" && !interrupted {
					f.mono += time.Hour.Milliseconds()
					_ = f.manager.Guard(t.Context(), f.source.spec)
				} else {
					err = f.manager.Isolate(t.Context(), f.source.spec.InstanceId, reason)
				}
				if (err != nil) != interrupted {
					t.Fatalf("isolation result: %v", err)
				}
				f.driver.stopFails = false
				next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
				next.Generation++
				next.ResourceSelectionRevision++
				next.UnifiedResources[0].InstallationRevision++
				next.UnifiedResources[0].Enabled = false
				next.UnifiedResources[0].DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED.Enum()
				f.source.spec = next
				// Recreate the manager before recovery, then again after applying DISABLED.
				for attempt := 0; attempt < 2; attempt++ {
					f.manager = &UnifiedManager{Layout: f.manager.Layout, Driver: f.driver, Specs: f.source, Protocol: f.manager.Protocol, Origin: f.manager.Origin, Client: f.manager.Client, Now: f.manager.Now, Mono: f.manager.Mono, BootID: f.manager.BootID}
					_ = f.manager.Guard(t.Context(), next)
					if _, err := f.manager.Prepare(t.Context(), next); err != nil {
						t.Fatal(err)
					}
					assertUnifiedR8Gate(t, f, true)
					s, _, _, err := f.manager.load(next.InstanceId)
					if err != nil || s.SafetyIsolationReason != reason {
						t.Fatalf("lost original safety cause: %+v %v", s, err)
					}
				}
				entries, err := os.ReadDir(current)
				if err != nil || len(entries) != 0 {
					t.Fatal("disabled shared view was not empty", err)
				}
				f.catalogPrivate = true
				_, guardian, err := f.manager.Observe(t.Context(), next.InstanceId)
				if err != nil || !guardian.GetNewSessionRequired() {
					t.Fatalf("observation cleared safety gate: %v %v", guardian, err)
				}
			})
		}
	}
}

func assertUnifiedR8Gate(t *testing.T, f *unifiedFixture, want bool) {
	t.Helper()
	_, guardian, err := f.manager.Status(f.source.spec.InstanceId)
	if err != nil || guardian.GetState() != "private_only" || guardian.GetNewSessionRequired() != want {
		t.Fatalf("wrong guardian: %v %v", guardian, err)
	}
	paths, err := f.manager.Layout.For(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct{ NewSessionRequired bool }
	raw, err := os.ReadFile(filepath.Join(paths.Meta, "resource-session-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &policy); err != nil || policy.NewSessionRequired != want {
		t.Fatalf("wrong session policy: %s %v", raw, err)
	}
}

func TestUnifiedR8LegacyIsolationAndVoluntaryDisable(t *testing.T) {
	for _, reason := range []string{"lease_expired", "observation_mismatch", "disabled"} {
		t.Run(reason, func(t *testing.T) {
			f := newUnifiedFixture(t)
			if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
				t.Fatal(err)
			}
			next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
			next.Generation++
			next.ResourceSelectionRevision++
			next.UnifiedResources[0].InstallationRevision++
			next.UnifiedResources[0].Enabled = false
			next.UnifiedResources[0].DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED.Enum()
			f.source.spec = next
			if reason == "disabled" {
				_ = f.manager.Guard(t.Context(), next)
			} else if err := f.manager.Isolate(t.Context(), next.InstanceId, "verification_failed"); err != nil {
				t.Fatal(err)
			}
			s, root, _, err := f.manager.load(next.InstanceId)
			if err != nil {
				t.Fatal(err)
			}
			// Model the old ledger, which persisted only Reason and HasShared.
			s.SafetyIsolationReason, s.Reason = "", reason
			if err := f.manager.save(root, s); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Prepare(t.Context(), next); err != nil {
				t.Fatal(err)
			}
			assertUnifiedR8Gate(t, f, reason != "disabled")
		})
	}
}

func TestUnifiedR8GuardianReasonsMatchFrozenSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "resource", "unified.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Type string
				Enum []string
			}
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	reasonSchema := schema.Defs["UnifiedGuardianState"].Properties["reason"]
	if reasonSchema.Type != "string" || len(reasonSchema.Enum) == 0 {
		t.Fatal("guardian reason schema changed")
	}
	inputs := append(slices.Clone(reasonSchema.Enum), "", "disabled", "observation_mismatch", "unknown")
	for _, reason := range inputs {
		t.Run(reason, func(t *testing.T) {
			f := newUnifiedFixture(t)
			if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Isolate(t.Context(), f.source.spec.InstanceId, reason); err != nil {
				t.Fatal(err)
			}
			s, root, _, err := f.manager.load(f.source.spec.InstanceId)
			if err != nil {
				t.Fatal(err)
			}
			_, status, err := f.manager.Status(s.Host)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{s.Reason, status.GetReason(), unifiedGuardian(s).GetReason()} {
				if !slices.Contains(reasonSchema.Enum, value) {
					t.Fatalf("reason %q violates frozen reason schema", value)
				}
			}
			receipts, err := filepath.Glob(filepath.Join(root, "disable-receipts", "*.json"))
			if err != nil || len(receipts) == 0 {
				t.Fatal("missing isolation receipt", err)
			}
			for _, file := range receipts {
				var receipt map[string]any
				if err := unifiedRead(file, &receipt); err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(reasonSchema.Enum, receipt["reason"].(string)) {
					t.Fatalf("receipt reason violates schema: %v", receipt)
				}
			}
		})
	}
}

func TestUnifiedR8MissingHostClockKeepsPrivateWithoutReactivation(t *testing.T) {
	for _, clock := range []string{"boot", "monotonic"} {
		for _, previouslyActive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/previouslyActive=%t", clock, previouslyActive), func(t *testing.T) {
				f := newUnifiedFixture(t)
				if previouslyActive {
					if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
						t.Fatal(err)
					}
				}
				if clock == "boot" {
					f.manager.BootID = ""
				} else {
					f.mono = -1
				}
				stopsAfterFirst := 0
				for attempt := 0; attempt < 3; attempt++ {
					current, err := f.manager.Prepare(t.Context(), f.source.spec)
					if err != nil {
						t.Fatal("private-only startup failed", err)
					}
					entries, err := os.ReadDir(current)
					if err != nil || len(entries) != 0 {
						t.Fatal("unavailable clock activated shared view", err)
					}
					assertUnifiedR8Gate(t, f, previouslyActive)
					if attempt == 0 {
						stopsAfterFirst = f.driver.stops
					} else if f.driver.stops != stopsAfterFirst {
						t.Fatalf("clock failure repeatedly stopped runtime: %d", f.driver.stops)
					}
					stops := f.driver.stops
					if err := f.manager.AuthorizeStart(t.Context(), f.source.spec.InstanceId); err != nil || f.driver.stops != stops {
						t.Fatal("clock failure caused another isolation", err)
					}
				}
			})
		}
	}
}
