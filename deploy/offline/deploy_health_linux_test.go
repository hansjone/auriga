package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeDeployHealthFixture(t *testing.T, s state, failedCore bool) {
	t.Helper()
	var records []map[string]any
	for _, name := range []string{"postgres", "nats", "registry", "platform", "edge", "agent", "bifrost", "egress"} {
		status := "running"
		if failedCore && name == "platform" {
			status = "exited"
		}
		records = append(records, map[string]any{"Service": name, "State": status, "Health": "healthy"})
	}
	for _, name := range []string{"collab", "network", "network-broker", "collab-db"} {
		records = append(records, map[string]any{"Service": name, "State": "exited", "ExitCode": 0})
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(s.Package, "deploy-nas/scripts/oneclick-compose.sh"), "#!/bin/sh\ncat <<'FIXTURE'\n"+string(raw)+"\nFIXTURE\n", 0755)
}

func TestDeployHealthExplicitOfflinePolicy(t *testing.T) {
	for _, collab := range []bool{false, true} {
		for _, offline := range []bool{false, true} {
			for _, coreFailed := range []bool{false, true} {
				name := strings.Join([]string{boolName(collab), boolName(offline), boolName(coreFailed)}, "/")
				t.Run(name, func(t *testing.T) {
					s := state{Data: t.TempDir(), Package: t.TempDir()}
					if collab {
						putFile(t, filepath.Join(s.Data, "config/collab-source/activation.receipt"), "fixture", 0600)
					}
					writeDeployHealthFixture(t, s, coreFailed)
					check := waitHealthyFor
					if offline {
						check = defaultSiteOps.health
					}
					err := check(s, 0)
					wantOK := collab && offline && !coreFailed
					if (err == nil) != wantOK {
						t.Fatalf("collab=%v offline=%v coreFailed=%v: %v", collab, offline, coreFailed, err)
					}
					if coreFailed && (err == nil || !strings.Contains(err.Error(), "platform")) {
						t.Fatalf("core failure hidden: %v", err)
					}
					if collab && offline && coreFailed && !strings.Contains(err.Error(), "离线维护被主动停止") {
						t.Fatalf("missing maintenance diagnosis: %v", err)
					}
				})
			}
		}
	}
}

func TestDeployHealthNormalWithoutCollab(t *testing.T) {
	s := state{Data: t.TempDir(), Package: t.TempDir()}
	writeDeployHealthFixture(t, s, false)
	path := filepath.Join(s.Package, "deploy-nas/scripts/oneclick-compose.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A normal non-collab deployment keeps all present services running.
	raw = []byte(strings.ReplaceAll(string(raw), `"State":"exited"`, `"State":"running"`))
	if err := os.WriteFile(path, raw, 0755); err != nil {
		t.Fatal(err)
	}
	if err := defaultSiteOps.health(s, 0); err != nil {
		t.Fatal(err)
	}
	if err := waitHealthyFor(s, 0); err != nil {
		t.Fatal(err)
	}
}

func TestDeployHealthMessageServiceScope(t *testing.T) {
	var core []map[string]string
	for _, name := range []string{"postgres", "nats", "registry", "platform", "edge", "agent", "bifrost", "egress"} {
		core = append(core, map[string]string{"Service": name, "State": "running"})
	}
	for _, name := range []string{"collab", "network", "network-broker", "collab-db", "collab-renew", "network-other", "edge"} {
		t.Run(name, func(t *testing.T) {
			records := append(append([]map[string]string(nil), core...), map[string]string{"Service": name, "State": "exited"})
			raw, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			if err := healthyContainers(raw); err == nil {
				t.Fatal("strict health accepted stopped service")
			}
			wantOK := name == "collab" || name == "network" || name == "network-broker" || name == "collab-db"
			if err := healthyContainersWithPolicy(raw, true); (err == nil) != wantOK {
				t.Fatalf("maintenance scope changed: %v", err)
			}
		})
	}
}

func boolName(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func TestDeployCollabOfflineFullAndContainers(t *testing.T) {
	for _, mode := range []string{switchModeFull, switchModeContainers} {
		t.Run(mode, func(t *testing.T) {
			s, _, rec, _ := switchFixture(t)
			putFile(t, filepath.Join(s.Data, "config/collab-source/activation.receipt"), "fixture", 0600)
			fixtureState := s
			fixtureState.Package = rec.Release
			writeDeployHealthFixture(t, fixtureState, false)
			site := &recordingSite{}
			ops := site.ops()
			ops.health = defaultSiteOps.health
			plan := switchPlan{Mode: mode, HasCollab: true, Recreate: []string{"platform"}}
			if err := startRecord(ops, &s, &rec, time.Nanosecond, plan); err != nil {
				t.Fatalf("official offline core start failed: %v", err)
			}
			if mode == switchModeFull && strings.Join(site.nasOps, ",") != "render,preflight,start --network-offline" {
				t.Fatalf("lost offline protection: %v", site.nasOps)
			}
			if mode == switchModeContainers && site.collab != 1 {
				t.Fatalf("lost message maintenance: %d", site.collab)
			}
		})
	}
}

func TestDeployCollabRollbackIsBoundedAndKeepsBothFailures(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	putFile(t, filepath.Join(s.Data, "config/collab-source/activation.receipt"), "fixture", 0600)
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &recordingSite{healthy: func(n int) error {
		if n == 1 {
			return errors.New("forward-core-failed")
		}
		return errors.New("rollback-core-failed")
	}}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || site.calls != 2 || strings.Count(strings.Join(site.nasOps, ","), "start --network-offline") != 2 {
		t.Fatalf("rollback retried: calls=%d operations=%v err=%v", site.calls, site.nasOps, err)
	}
	for _, cause := range []string{"forward-core-failed", "rollback-core-failed"} {
		if !strings.Contains(err.Error(), cause) || !strings.Contains(run.receipt.Health, cause) {
			t.Fatalf("lost %s: err=%v receipt=%s", cause, err, run.receipt.Health)
		}
	}
}
