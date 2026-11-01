package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNASHistoryLifetimeChecksEveryPurposeAndState(t *testing.T) {
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		for _, state := range []string{"active", "next", "retired", "revoked"} {
			p := nasRotationPayload{Keys: []nasRotationKey{{Kid: "historical", Purpose: purpose, State: state, Before: "2026-01-01T00:00:00Z", After: "2026-01-09T00:00:00Z"}}}
			if err := nasRotationHistoryLifetime(p); err != nil {
				t.Fatal(err)
			}
			for _, after := range []string{"2026-01-09T00:00:00.000000001Z", "2025-12-31T23:59:59Z", "invalid"} {
				p.Keys[0].After = after
				if err := nasRotationHistoryLifetime(p); err == nil {
					t.Fatalf("accepted %s %s %s", purpose, state, after)
				}
			}
		}
	}
}

func TestNASHistoryCheckAuthenticatesCurrentPGAndDoesNotPublish(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	before, err := json.Marshal(f.status)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = f.r.historyCheck(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "history_lifetime_checked") {
		t.Fatal(out.String())
	}
	after, _ := json.Marshal(f.status)
	if !bytes.Equal(before, after) || f.stops != 0 {
		t.Fatal("precheck changed runtime")
	}
	f.status.Version = "999"
	if err = f.r.historyCheck(nil, &out); err == nil {
		t.Fatal("accepted a different PG publication")
	}
}

func TestNASQueueRenewalWarningPreparationWindows(t *testing.T) {
	for _, row := range []struct{ at, start, end string }{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T06:00:00Z"},
		{"2026-01-01T06:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T06:00:00Z"},
		{"2026-01-01T06:00:00.000000001Z", "2026-01-01T12:00:00Z", "2026-01-01T18:00:00Z"},
		{"2026-01-01T18:01:00Z", "2026-01-02T00:00:00Z", "2026-01-02T06:00:00Z"},
	} {
		now, _ := time.Parse(time.RFC3339Nano, row.at)
		warning := nasQueueRenewalWarning(now.Add(48*time.Hour), now)
		if !strings.Contains(warning, row.start) || !strings.Contains(warning, row.end) {
			t.Fatal(warning)
		}
		if nasQueueRenewalWarning(now.Add(48*time.Hour+time.Nanosecond), now) != "" {
			t.Fatal("early warning")
		}
	}
}
