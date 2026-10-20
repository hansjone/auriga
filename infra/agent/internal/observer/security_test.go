package observer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/ingress"
)

func TestCron_ImpossibleHasNoNextWake(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NextCron("0 0 30 2 *", "UTC", now); err == nil {
		t.Fatal("impossible cron accepted")
	}
	if _, err := NextCron("@every 1s", "UTC", now); err == nil {
		t.Fatal("descriptor accepted")
	}
	dir := t.TempDir()
	for _, path := range []string{"ops-cron/store.json", "storages/schedule.json"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ops := `{"jobs":[{"enabled":true,"schedule":{"kind":"cron","expr":"0 0 30 2 *"}}]}`
	sched := `{"tables":{"tasks":{"t":{"record":{"kind":"cron","expression":"0 0 30 2 *"}}}}}`
	if err := os.WriteFile(filepath.Join(dir, "ops-cron/store.json"), []byte(ops), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "storages/schedule.json"), []byte(sched), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := NextWakeFromInstanceDir(dir, now)
	if err != nil || info.NextWakeAt != nil {
		t.Fatalf("wake: %+v %v", info, err)
	}
}

func TestSchedule_BoundedAndSanitized(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "ops-cron"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "ops-cron/store.json")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", maxScheduleBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSchedule(root, "ops-cron/store.json"); err == nil {
		t.Fatal("oversized schedule read")
	}
	last := map[string]time.Time{"i1|ops-cron/store.json": time.Now()}
	if !scheduleFilesChanged(root, "i1", last) || len(last) != 0 {
		t.Fatal("invalid file did not clear previous wake")
	}
	if err := os.WriteFile(file, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := nextFromOpsCron(root, "ops-cron/store.json", time.Now()); err == nil || strings.Contains(err.Error(), "S") {
		t.Fatalf("unsafe parse error: %v", err)
	}
}

func TestSchedule_RejectsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "store.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ops-cron")); err != nil {
		t.Skip(err)
	}
	if _, err := readSchedule(root, "ops-cron/store.json"); err == nil || !strings.Contains(err.Error(), ingress.CodePathEscape) {
		t.Fatalf("symlink followed: %v", err)
	}
	last := map[string]time.Time{}
	if scheduleFilesChanged(root, "i1", last) || len(last) != 0 {
		t.Fatal("symlink watched")
	}
}

type cumulativeCollector struct {
	NopResources
	cpu  int64
	fail bool
}

func (c *cumulativeCollector) CollectInstance(InstanceTarget) (InstanceResources, error) {
	if c.fail {
		return InstanceResources{}, os.ErrPermission
	}
	return InstanceResources{CPUMillis: c.cpu}, nil
}

func TestUsage_CPUUsesDeltaAndCounterReset(t *testing.T) {
	clock := &MutableClock{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	collector := &cumulativeCollector{cpu: 1000}
	src := &SliceTargets{}
	src.Set([]InstanceTarget{{ID: "i1"}})
	o := New(Config{Source: src, Clock: clock, Collector: collector, Reporter: &captureReporter{}})
	sample := func(cpu int64, active bool) {
		t.Helper()
		collector.cpu = cpu
		o.sampleUsage()
		if got := o.cfg.Activity.RecentCPUActive("i1", 50); got != active {
			t.Fatalf("cpu=%d active=%v want=%v", cpu, got, active)
		}
	}
	sample(1000, false)
	sample(1001, false)
	sample(1100, true)
	clock.Advance(6 * time.Minute)
	sample(1101, false)
	sample(0, false)
	sample(60, true)
	clock.Advance(6 * time.Minute)
	collector.fail = true
	o.sampleUsage()
	collector.fail = false
	sample(5000, false)
	src.Set(nil)
	o.sampleUsage()
	if len(o.lastCPU) != 0 {
		t.Fatal("removed instance retained baseline")
	}
}
