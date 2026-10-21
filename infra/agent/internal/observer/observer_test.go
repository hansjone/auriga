package observer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestWatch_RecalculatesDueWakeWithoutFileChange(t *testing.T) {
	for _, kind := range []string{"at", "cron", "next-job"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "ops-cron"), 0o700); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
			due := now.Add(time.Minute)
			jobs := fmt.Sprintf(`{"enabled":true,"schedule":{"kind":"at","at":%q}}`, due.Format(time.RFC3339))
			if kind == "cron" {
				jobs = `{"enabled":true,"schedule":{"kind":"cron","expr":"* * * * *","timezone":"UTC"}}`
			}
			if kind == "next-job" {
				jobs += fmt.Sprintf(`,{"enabled":true,"schedule":{"kind":"at","at":%q}}`, due.Add(time.Minute).Format(time.RFC3339))
			}
			path := filepath.Join(dir, "ops-cron", "store.json")
			if err := os.WriteFile(path, []byte(`{"jobs":[`+jobs+`]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Stat(path)
			clock := &MutableClock{T: now}
			rep := &captureReporter{}
			src := &SliceTargets{}
			src.Set([]InstanceTarget{{ID: "inst-1", RootDir: dir, Process: ProcessStopped}})
			obs := New(Config{Source: src, Reporter: rep, Clock: clock, Collector: NopResources{}})
			mod := map[string]time.Time{}
			obs.refreshWakes(mod)
			if len(rep.wakes) != 1 || !rep.wakes[0].GetNextWakeAt().AsTime().Equal(due) {
				t.Fatal("initial wake missing")
			}
			clock.Advance(30 * time.Second)
			obs.refreshWakes(mod)
			if len(rep.wakes) != 1 {
				t.Fatal("unchanged future wake needlessly republished")
			}
			clock.Advance(30 * time.Second)
			obs.refreshWakes(mod)
			if len(rep.wakes) != 2 {
				t.Fatal("due wake was not recalculated without file change")
			}
			got := rep.wakes[1].GetNextWakeAt()
			if kind == "at" {
				if got != nil {
					t.Fatal("consumed one-shot wake was retained")
				}
			} else if got == nil || !got.AsTime().Equal(due.Add(time.Minute)) {
				t.Fatal("next occurrence/job not published")
			}
			after, _ := os.Stat(path)
			if !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("test changed task file")
			}
			obs.refreshWakes(mod)
			if len(rep.wakes) != 2 {
				t.Fatal("consumption repeatedly published")
			}
		})
	}
}

type captureReporter struct {
	mu    sync.Mutex
	wakes []*nodev1.WakeSchedule
	ready map[string]bool
}

func (c *captureReporter) ReportWake(w *nodev1.WakeSchedule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wakes = append(c.wakes, w)
}

func (c *captureReporter) ReportUsage(*nodev1.NodeUsage) {}

func (c *captureReporter) ReportReady(id string, ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready == nil {
		c.ready = map[string]bool{}
	}
	c.ready[id] = ready
}

func TestWatch_FileChangeTriggersWake(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ops-cron"), 0o700)
	clock := &MutableClock{T: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	rep := &captureReporter{}
	src := &SliceTargets{}
	src.Set([]InstanceTarget{{ID: "inst-1", RootDir: dir, Process: ProcessRunning}})

	obs := New(Config{
		Source:      src,
		Reporter:    rep,
		Clock:       clock,
		Collector:   NopResources{},
		WatchPoll:   20 * time.Millisecond,
		HealthEvery: time.Hour,
		UsageEvery:  time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	obs.Start(ctx)
	defer obs.Stop()

	// Write schedule after start; poll should pick it up.
	ops := `{"version":1,"jobs":[{"enabled":true,"schedule":{"kind":"cron","expr":"0 8 * * *","timezone":"UTC"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "ops-cron", "store.json"), []byte(ops), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rep.mu.Lock()
		n := len(rep.wakes)
		rep.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected wake report after file change")
}

func TestProbeHealth_Ready(t *testing.T) {
	t.Parallel()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_auriga/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	res := ProbeHealth(context.Background(), up.Client(), InstanceTarget{
		ID: "i1", BaseURL: u, Process: ProcessRunning,
	})
	if !res.Ready || !res.HTTPOK {
		t.Fatalf("%+v", res)
	}
}

func TestActivity_RecentCPU(t *testing.T) {
	t.Parallel()
	clock := &MutableClock{T: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	a := NewActivityTracker(clock)
	a.Observe("i1", 100)
	if !a.RecentCPUActive("i1", 50) {
		t.Fatal("expected active")
	}
	clock.Advance(6 * time.Minute)
	if a.RecentCPUActive("i1", 50) {
		t.Fatal("sample should expire after window")
	}
}

func TestRefreshWakeNow_InjectedClock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ops-cron"), 0o700)
	ops := `{"version":1,"jobs":[{"enabled":true,"schedule":{"kind":"cron","expr":"0 9 * * *","timezone":"UTC"}}]}`
	_ = os.WriteFile(filepath.Join(dir, "ops-cron", "store.json"), []byte(ops), 0o600)
	clock := FixedClock{T: time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)}
	rep := &captureReporter{}
	obs := New(Config{Reporter: rep, Clock: clock, Collector: NopResources{}})
	w, err := obs.RefreshWakeNow("inst-1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.GetNextWakeAt() == nil {
		t.Fatal("nil next")
	}
	got := w.GetNextWakeAt().AsTime().UTC()
	want := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
