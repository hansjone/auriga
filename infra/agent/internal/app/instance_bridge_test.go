package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type endpointDriver struct {
	runtime.Driver
	endpoint string
}

func (d *endpointDriver) Endpoint(string) (runtime.Endpoint, error) {
	return runtime.Endpoint{URL: d.endpoint}, nil
}

type wakeClock struct{ nanos atomic.Int64 }

func (c *wakeClock) Now() time.Time { return time.Unix(0, c.nanos.Load()) }

type bridgeReporter struct{ wakes chan *nodev1.WakeSchedule }

func (r bridgeReporter) ReportWake(w *nodev1.WakeSchedule) { r.wakes <- w }
func (bridgeReporter) ReportUsage(*nodev1.NodeUsage)       {}
func (bridgeReporter) ReportReady(string, bool)            {}

func TestStoppedBridgeKeepsWakeWatcherAndReplacesRunningEndpoint(t *testing.T) {
	cfg := config.Config{DataRoot: t.TempDir()}
	driver := &endpointDriver{endpoint: "http://127.0.0.1:12345"}
	registry, targets := ingress.NewMapRegistry(), &observer.SliceTargets{}
	b := newInstanceBridge(cfg, driver, registry, targets)
	root := filepath.Join(cfg.InstancesDir(), "wake-test", "home")
	if err := os.MkdirAll(filepath.Join(root, "ops-cron"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ops-cron", "store.json")
	now := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	due := now.Add(time.Minute)
	write := func(at time.Time) {
		t.Helper()
		body := fmt.Sprintf(`{"jobs":[{"enabled":true,"schedule":{"kind":"at","at":%q}}]}`, at.Format(time.RFC3339))
		if err := os.WriteFile(path+".next", []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path+".next", at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".next", path); err != nil {
			t.Fatal(err)
		}
	}
	write(due)
	clock := &wakeClock{}
	clock.nanos.Store(now.UnixNano())
	reporter := bridgeReporter{wakes: make(chan *nodev1.WakeSchedule, 16)}
	obs := observer.New(observer.Config{Source: targets, Reporter: reporter, Clock: clock, Collector: observer.NopResources{},
		WatchPoll: 5 * time.Millisecond, HealthEvery: time.Hour, UsageEvery: time.Hour})
	b.Sync(&nodev1.InstanceStatus{InstanceId: "wake-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING})
	obs.Start(context.Background())
	t.Cleanup(obs.Stop)
	next := func(want *time.Time) {
		t.Helper()
		select {
		case wake := <-reporter.wakes:
			if want == nil {
				if wake.NextWakeAt != nil {
					t.Fatalf("one-shot not consumed: %v", wake)
				}
				return
			}
			if wake.NextWakeAt == nil || !wake.NextWakeAt.AsTime().Equal(*want) {
				t.Fatalf("wake=%v want=%v", wake, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("wake watcher lost retained target")
		}
	}
	next(&due)
	b.Sync(&nodev1.InstanceStatus{InstanceId: "wake-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_STOPPED})
	if _, ok := registry.Lookup("wake-test"); ok {
		t.Fatal("stopped ingress retained")
	}
	got := targets.Targets()
	if len(got) != 1 || got[0].RootDir != root || got[0].Process != observer.ProcessStopped || got[0].BaseURL != nil {
		t.Fatalf("stopped watch target missing or still probes: %+v", got)
	}
	before, _ := os.Stat(path)
	clock.nanos.Store(due.UnixNano())
	next(nil)
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("test changed schedule before consumption")
	}
	newDue := due.Add(time.Minute)
	write(newDue)
	next(&newDue)
	clock.nanos.Store(newDue.UnixNano())
	next(nil)
	driver.endpoint = "http://127.0.0.1:23456"
	b.Sync(&nodev1.InstanceStatus{InstanceId: "wake-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING})
	registered, ok := registry.Lookup("wake-test")
	got = targets.Targets()
	if !ok || registered.Upstream.String() != driver.endpoint || len(got) != 1 || got[0].Process != observer.ProcessRunning || got[0].BaseURL.String() != driver.endpoint {
		t.Fatal("restarted endpoint/target not refreshed")
	}
	select {
	case wake := <-reporter.wakes:
		t.Fatalf("consumption republished: %v", wake)
	case <-time.After(30 * time.Millisecond):
	}
	b.Sync(&nodev1.InstanceStatus{InstanceId: "wake-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_ABSENT})
	if len(targets.Targets()) != 0 {
		t.Fatal("absent target retained")
	}
}
