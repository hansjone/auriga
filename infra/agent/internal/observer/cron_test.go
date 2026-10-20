package observer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNextCron_Timezone(t *testing.T) {
	t.Parallel()
	// 09:00 Asia/Shanghai = 01:00 UTC
	now := time.Date(2026, 3, 10, 0, 30, 0, 0, time.UTC)
	next, err := NextCron("0 9 * * *", "Asia/Shanghai", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 10, 9, 0, 0, 0, mustLoad(t, "Asia/Shanghai"))
	if !next.Equal(want) {
		t.Fatalf("got %v want %v", next, want)
	}
}

func TestNextCron_DSTSpringForward(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "America/Los_Angeles")
	// 2026-03-08 02:00 does not exist (spring forward). Cron "30 2 * * *" —
	// robfig typically lands on the next valid instant after the gap.
	now := time.Date(2026, 3, 8, 1, 0, 0, 0, loc)
	next, err := NextCron("30 2 * * *", "America/Los_Angeles", now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Before(now) {
		t.Fatalf("next %v before now %v", next, now)
	}
	// Should be on 2026-03-08 or 03-09 depending on library; must be in LA tz.
	if next.Location().String() != loc.String() {
		t.Fatalf("tz %v", next.Location())
	}
	// After spring forward, 2:30 doesn't exist; next valid is often 3:30 PDT same day
	// or skipped to next day — assert hour is not stuck in the gap.
	if next.Day() == 8 && next.Hour() == 2 {
		t.Fatalf("landed in DST gap: %v", next)
	}
}

func TestNextCron_DSTFallBack(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "America/Los_Angeles")
	// First 01:30 PDT then 01:30 PST exists twice; from before the fold take first.
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	next, err := NextCron("30 1 * * *", "America/Los_Angeles", now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 1 || next.Minute() != 30 {
		t.Fatalf("got %v", next)
	}
}

func TestNextWakeFromOpsCronAndSchedule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ops-cron"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "storages"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ops := `{
  "version":1,
  "settings":{"timezone":"Asia/Shanghai"},
  "jobs":[{
    "enabled":true,
    "schedule":{"kind":"cron","expr":"0 10 * * *","timezone":"Asia/Shanghai"}
  }]
}`
	if err := os.WriteFile(filepath.Join(dir, "ops-cron", "store.json"), []byte(ops), 0o600); err != nil {
		t.Fatal(err)
	}
	sched := `{
  "tables":{"tasks":{
    "t1":{"status":"active","record":{
      "kind":"at","scheduledAt":"2026-06-01T12:00:00Z"
    }}
  }}
}`
	if err := os.WriteFile(filepath.Join(dir, "storages", "schedule.json"), []byte(sched), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := NextWakeFromInstanceDir(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if info.NextWakeAt == nil {
		t.Fatal("expected next wake")
	}
	// ops-cron 10:00 CST = 02:00 UTC, earlier than schedule 12:00 UTC
	if info.Source != "ops-cron" {
		t.Fatalf("source %s", info.Source)
	}
	want := time.Date(2026, 6, 1, 10, 0, 0, 0, mustLoad(t, "Asia/Shanghai"))
	if !info.NextWakeAt.Equal(want) {
		t.Fatalf("got %v want %v", info.NextWakeAt, want)
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
