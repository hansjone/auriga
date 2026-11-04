package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func TestInterruptedRuntimeRecoveryChecksEngineAndSignedEvidence(t *testing.T) {
	for _, mode := range []string{"success", "older-stopped-execution", "running-instance", "running-agent", "unknown-state", "wrong-image", "unknown-managed", "wrong-mount", "changed-expectation", "signature", "missing-execution"} {
		t.Run(mode, func(t *testing.T) {
			agent := strings.Repeat("a", 64)
			dataRoot := t.TempDir()
			target := "recovery-target"
			var record *state.ExecutionRecord
			writes := 0
			stopped := func() map[string]any {
				return map[string]any{"Status": "exited", "Running": false, "Restarting": false, "Paused": false, "Dead": false}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					http.Error(w, "unexpected mutation", 500)
					return
				}
				if r.URL.Path == "/containers/"+agent+"/json" {
					s := stopped()
					if mode == "running-agent" {
						s["Running"] = true
					}
					mount := dataRoot
					if mode == "wrong-mount" {
						mount = filepath.Join(dataRoot, "other")
					}
					writeRecoveryJSON(t, w, map[string]any{"Id": agent, "State": s, "Config": map[string]any{"Labels": map[string]any{"com.docker.compose.service": "agent"}}, "Mounts": []any{map[string]any{"Type": "bind", "Source": mount, "Destination": mount}}})
					return
				}
				if r.URL.Path == "/containers/json" {
					labels := map[string]any{"auriga.managed": "true", "auriga.instance": target, "auriga.generation": "1"}
					id := fixtureContainerID
					if mode == "unknown-managed" {
						id = strings.Repeat("b", 64)
					}
					items := []any{map[string]any{"Id": id, "Labels": labels}}
					if mode == "older-stopped-execution" {
						items = append(items, map[string]any{"Id": strings.Repeat("d", 64), "Labels": map[string]any{"auriga.managed": "true", "auriga.instance": "old-stopped", "auriga.generation": "1"}})
					}
					writeRecoveryJSON(t, w, items)
					return
				}
				if mode == "older-stopped-execution" && r.URL.Path == "/containers/"+strings.Repeat("d", 64)+"/json" {
					writeRecoveryJSON(t, w, map[string]any{"Id": strings.Repeat("d", 64), "Image": fixtureConfigID, "State": stopped(), "Config": map[string]any{"Labels": map[string]any{"auriga.managed": "true", "auriga.instance": "old-stopped", "auriga.generation": "1"}}})
					return
				}
				if r.URL.Path == "/containers/"+fixtureContainerID+"/json" {
					if mode == "missing-execution" {
						http.NotFound(w, r)
						return
					}
					s := stopped()
					if mode == "running-instance" {
						s["Running"] = true
					}
					if mode == "unknown-state" {
						delete(s, "Running")
					}
					image := fixtureConfigID
					if mode == "wrong-image" {
						image = "sha256:" + strings.Repeat("d", 64)
					}
					writeRecoveryJSON(t, w, map[string]any{"Id": fixtureContainerID, "Image": image, "State": s, "Config": map[string]any{"Labels": map[string]any{"auriga.managed": "true", "auriga.instance": target, "auriga.generation": "1"}}})
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			client, err := runtime.DialEngine(server.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			d := runtime.NewDockerWithClient(client, dataRoot)
			d.Policy.AllowAnyImage = true
			f := dockerFixture(t, d)
			seedDockerRecord(t, d, target, 1)
			record, err = f.store.GetExecution(target)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "older-stopped-execution" {
				old := *record
				old.InstanceID = "old-stopped"
				old.ContainerID = strings.Repeat("d", 64)
				old.DSH = proto.Clone(record.DSH).(*nodev1.DshRuntime)
				old.DSH.ReleaseEnvelope.ReleaseSequence = 6
				signRelease(t, old.DSH, f.key)
				ring, e := f.store.GetReleaseTrust()
				if e != nil {
					t.Fatal(e)
				}
				v, e := releaseverify.Verify(old.DSH, ring, releaseverify.Target{OS: goruntime.GOOS, Arch: goruntime.GOARCH, Driver: "docker", AllowImage: d.Policy.AllowImage})
				if e != nil {
					t.Fatal(e)
				}
				old.Candidate.Sequence = v.Sequence()
				old.Candidate.BodyDigest = v.BodyDigest()
				if e := f.store.PutExecution(old); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "signature" {
				record.DSH.ReleaseEnvelope.Signature.Value = "invalid"
				if err := f.store.PutExecution(*record); err != nil {
					t.Fatal(err)
				}
			}
			c := record.Candidate
			err = f.store.WithReleaseExecution(context.Background(), c, func(context.Context) error { return nil }, func(_ context.Context, e *state.ReleaseExecution) error {
				finish, err := e.BeginAction()
				if err != nil {
					return err
				}
				finish(false)
				return nil
			})
			if !errors.Is(err, state.ErrReleaseUnresolved) {
				t.Fatal(err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			offline, err := state.OpenOfflineRecovery(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := offline.Close(); err != nil {
					t.Error(err)
				}
			})
			d.Activation.Store = offline
			snapshot, err := d.RecoverInterruptedRuntime(context.Background(), offline, target, agent, "")
			success := mode == "success" || mode == "older-stopped-execution"
			if !success && mode != "changed-expectation" {
				if err == nil {
					t.Fatal("unsafe inspection accepted")
				}
				if e := offline.CheckReleaseExecution(c); !errors.Is(e, state.ErrReleaseUnresolved) {
					t.Fatal("failed inspection changed intent", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e := offline.CheckReleaseExecution(c); !errors.Is(e, state.ErrReleaseUnresolved) {
				t.Fatal("inspect cleared intent", e)
			}
			expect := snapshot.Expectation
			if mode == "changed-expectation" {
				expect = "sha256:" + strings.Repeat("0", 64)
			}
			_, err = d.RecoverInterruptedRuntime(context.Background(), offline, target, agent, expect)
			if (err == nil) != success {
				t.Fatal(err)
			}
			if success {
				if e := offline.CheckReleaseExecution(c); e != nil {
					t.Fatal(e)
				}
			}
			got, found, err := offline.GetReleaseHighWater(c.Partition)
			if err != nil || !found || got != c {
				t.Fatal("high water changed", err)
			}
			actual, err := offline.GetExecution(target)
			if err != nil || actual == nil || actual.ContainerID != record.ContainerID {
				t.Fatal("actual execution changed", err)
			}
			if writes != 0 {
				t.Fatal("recovery mutated Engine")
			}
		})
	}
}

func writeRecoveryJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write recovery Engine fixture: %v", err)
	}
}
