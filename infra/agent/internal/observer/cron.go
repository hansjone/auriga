package observer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/robfig/cron/v3"
)

// WakeInfo is the next scheduled wake for an instance.
type WakeInfo struct {
	InstanceID string
	NextWakeAt *time.Time
	Source     string // ops-cron | schedule
}

// NextWakeFromInstanceDir reads ops-cron/store.json and storages/schedule.json.
func NextWakeFromInstanceDir(instanceDir string, now time.Time) (WakeInfo, error) {
	var best *time.Time
	source := ""

	if t, src, err := nextFromOpsCron(instanceDir, "ops-cron/store.json", now); err == nil && t != nil {
		best = t
		source = src
	}
	if t, src, err := nextFromSchedule(instanceDir, "storages/schedule.json", now); err == nil && t != nil {
		if best == nil || t.Before(*best) {
			best = t
			source = src
		}
	}
	return WakeInfo{NextWakeAt: best, Source: source}, nil
}

func nextFromOpsCron(root, path string, now time.Time) (*time.Time, string, error) {
	raw, err := readSchedule(root, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("schedule read or parse failed")
	}
	var store struct {
		Jobs []struct {
			Enabled   bool   `json:"enabled"`
			NextRunAt *int64 `json:"nextRunAt"`
			Schedule  *struct {
				Kind     string `json:"kind"`
				Expr     string `json:"expr"`
				Timezone string `json:"timezone"`
				At       string `json:"at"`
			} `json:"schedule"`
		} `json:"jobs"`
		Settings struct {
			Timezone string `json:"timezone"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, "", fmt.Errorf("schedule read or parse failed")
	}
	defaultTZ := store.Settings.Timezone
	if defaultTZ == "" {
		defaultTZ = "UTC"
	}
	var best *time.Time
	for _, j := range store.Jobs {
		if !j.Enabled {
			continue
		}
		var next *time.Time
		if j.NextRunAt != nil && *j.NextRunAt > 0 {
			t := time.UnixMilli(*j.NextRunAt)
			if t.After(now) {
				next = &t
			}
		}
		if next == nil && j.Schedule != nil {
			tz := j.Schedule.Timezone
			if tz == "" {
				tz = defaultTZ
			}
			t, err := nextFromScheduleSpec(j.Schedule.Kind, j.Schedule.Expr, j.Schedule.At, tz, now)
			if err == nil {
				next = t
			}
		}
		if next != nil && (best == nil || next.Before(*best)) {
			best = next
		}
	}
	return best, "ops-cron", nil
}

func nextFromSchedule(root, path string, now time.Time) (*time.Time, string, error) {
	raw, err := readSchedule(root, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("schedule read or parse failed")
	}
	var doc struct {
		Tables struct {
			Tasks map[string]struct {
				Status string `json:"status"`
				Record struct {
					Kind        string `json:"kind"`
					Expression  string `json:"expression"`
					TimeZone    string `json:"timeZone"`
					Time        string `json:"time"`
					ScheduledAt string `json:"scheduledAt"`
				} `json:"record"`
			} `json:"tasks"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "", fmt.Errorf("schedule read or parse failed")
	}
	var best *time.Time
	for _, task := range doc.Tables.Tasks {
		if task.Status != "" && task.Status != "active" {
			continue
		}
		r := task.Record
		tz := r.TimeZone
		if tz == "" {
			tz = "UTC"
		}
		var next *time.Time
		switch r.Kind {
		case "at":
			if r.ScheduledAt != "" {
				if t, err := time.Parse(time.RFC3339, r.ScheduledAt); err == nil && t.After(now) {
					next = &t
				}
			}
		case "cron":
			t, err := NextCron(r.Expression, tz, now)
			if err == nil {
				next = &t
			}
		case "daily":
			t, err := nextDaily(r.Time, tz, now)
			if err == nil {
				next = t
			}
		default:
			if r.ScheduledAt != "" {
				if t, err := time.Parse(time.RFC3339, r.ScheduledAt); err == nil && t.After(now) {
					next = &t
				}
			}
		}
		if next != nil && (best == nil || next.Before(*best)) {
			best = next
		}
	}
	return best, "schedule", nil
}

func nextFromScheduleSpec(kind, expr, at, tz string, now time.Time) (*time.Time, error) {
	switch kind {
	case "cron":
		t, err := NextCron(expr, tz, now)
		if err != nil {
			return nil, err
		}
		return &t, nil
	case "at":
		loc, err := loadTZ(tz)
		if err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t, err = time.ParseInLocation("2006-01-02T15:04:05", at, loc)
		}
		if err != nil {
			return nil, err
		}
		if !t.After(now) {
			return nil, nil
		}
		return &t, nil
	default:
		return nil, fmt.Errorf("unsupported schedule kind %q", kind)
	}
}

// NextCron returns the next fire time for a 5-field cron expression in tz.
func NextCron(expr, tz string, now time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" || strings.HasPrefix(expr, "@") {
		return time.Time{}, fmt.Errorf("empty cron")
	}
	loc, err := loadTZ(tz)
	if err != nil {
		return time.Time{}, err
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	next := sched.Next(now.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("no next cron trigger")
	}
	return next, nil
}

func nextDaily(clockHHMMSS, tz string, now time.Time) (*time.Time, error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return nil, err
	}
	clockHHMMSS = strings.TrimSuffix(clockHHMMSS, ".000")
	parts := strings.Split(clockHHMMSS, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("bad daily time %q", clockHHMMSS)
	}
	var h, m, sec int
	_, _ = fmt.Sscanf(parts[0], "%d", &h)
	_, _ = fmt.Sscanf(parts[1], "%d", &m)
	if len(parts) > 2 {
		_, _ = fmt.Sscanf(parts[2], "%d", &sec)
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), h, m, sec, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return &next, nil
}

func loadTZ(name string) (*time.Location, error) {
	if name == "" || strings.EqualFold(name, "UTC") || strings.EqualFold(name, "system") {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}

const maxScheduleBytes = 1 << 20

func openSchedule(root, path string) (*os.File, error) {
	f, err := ingress.OpenBeneath(root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxScheduleBytes {
		_ = f.Close()
		return nil, fmt.Errorf("invalid schedule file")
	}
	return f, nil
}

func readSchedule(root, path string) ([]byte, error) {
	f, err := openSchedule(root, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxScheduleBytes+1))
	if err != nil || len(raw) > maxScheduleBytes {
		return nil, fmt.Errorf("invalid schedule file")
	}
	return raw, nil
}
