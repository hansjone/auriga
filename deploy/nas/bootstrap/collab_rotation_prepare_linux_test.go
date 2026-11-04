package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func prepareNASRotationRequest(t *testing.T, r *nasRotation) (string, nasRotationPayload) {
	t.Helper()
	var out bytes.Buffer
	if err := r.prepare(nil, &out); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Directory string `json:"requestDirectory"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Directory == "" {
		t.Fatalf("prepare output: %s (%v)", out.String(), err)
	}
	raw, err := nasRotationRead(filepath.Join(result.Directory, "payload.json"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var p nasRotationPayload
	if err := nasRotationDecode(raw, &p); err != nil {
		t.Fatal(err)
	}
	return result.Directory, p
}

// Snapshot all fixture files, including the fake root, requests, approvals,
// history and signer state. prepare may append, but must not rewrite any of them.
func snapshotNASRotationPrepare(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = b
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func assertNASRotationPrepareUnchanged(t *testing.T, files map[string][]byte) {
	t.Helper()
	for path, before := range files {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("prepare rewrote an existing fixture file: %s (%v)", path, err)
		}
	}
}

func TestNASRotationPrepareFutureWeeklyQueueCannotMaskCurrent(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	now := time.Now().UTC().Truncate(time.Second)
	f.payload.Until = now.Add(24 * time.Hour).Format(time.RFC3339)
	for i := range f.payload.Keys {
		f.payload.Keys[i].After = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	f.sign(t, filepath.Join(f.r.source, "collab-installation"), f.payload, false)
	// Use the real weekly planner and importer with fixture-only signatures.
	// No clock changes, real PG, Docker daemon or network services are used.
	if err := f.r.queuePrepareAt(nil, 14, io.Discard, now.Truncate(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	q := approveNASQueue(t, f)
	if err := f.r.queueImport(io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, en := range q.Entries {
		if !timeMustParse(t, en.Issued).After(now) {
			t.Fatal("fixture must contain only future requests")
		}
	}
	before := snapshotNASRotationPrepare(t, f.r.data)
	status, _ := json.Marshal(f.status)
	start := time.Now().UTC().Truncate(time.Second)
	dir, p := prepareNASRotationRequest(t, f.r)
	end := time.Now().UTC()
	if p.Version != "22" || filepath.Base(dir) != "22" {
		t.Fatalf("future weekly request masked current prepare: got %s, want 22", p.Version)
	}
	issued := timeMustParse(t, p.Issued)
	if issued.Before(start) || issued.After(end) || timeMustParse(t, p.Until).Sub(issued) != 24*time.Hour {
		t.Fatal("new request is not usable at the real current time")
	}
	if !reflect.DeepEqual(p.Keys, f.payload.Keys) {
		t.Fatal("request did not use current published history")
	}
	assertNASRotationPrepareUnchanged(t, before)
	after, _ := json.Marshal(f.status)
	if !bytes.Equal(status, after) || f.stops != 0 {
		t.Fatal("prepare changed publication state or stopped a service")
	}
	stable := snapshotNASRotationPrepare(t, f.r.data)
	again, _ := prepareNASRotationRequest(t, f.r)
	if again != dir {
		t.Fatal("repeated prepare did not reuse its current request")
	}
	if len(snapshotNASRotationPrepare(t, f.r.data)) != len(stable) {
		t.Fatal("repeated prepare allocated another version")
	}
	assertNASRotationPrepareUnchanged(t, stable)
	for _, action := range f.calls {
		if action == "publish" || action == "install" || action == "recover-expired" {
			t.Fatal("prepare attempted a publication")
		}
	}
}

func TestNASRotationPrepareCandidateSelection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		age, lifetime time.Duration
		keepCurrent   bool
		want          string
	}{
		{"current", -time.Minute, 24 * time.Hour, false, "32"},
		{"future-with-current", 72 * time.Hour, 24 * time.Hour, true, "8"},
		{"future-only", 72 * time.Hour, 24 * time.Hour, false, "33"},
		{"stale", -7 * time.Hour, 24 * time.Hour, false, "33"},
		{"expired", -25 * time.Hour, 24 * time.Hour, false, "33"},
		{"overlong", -time.Minute, 25 * time.Hour, false, "33"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nasRotationTestFixture(t, "ip")
			_, p := prepareNASRotationRequest(t, f.r)
			issued := time.Now().UTC().Truncate(time.Second).Add(tc.age)
			p.Issued = issued.Format(time.RFC3339)
			p.Until = issued.Add(tc.lifetime).Format(time.RFC3339)
			for _, v := range []string{"8", "32"} {
				if v == "8" && tc.keepCurrent {
					continue
				}
				p.Version = v
				nasRotationTestJSON(t, filepath.Join(f.r.root, "requests", v, "payload.json"), p)
			}
			before := snapshotNASRotationPrepare(t, f.r.data)
			dir, _ := prepareNASRotationRequest(t, f.r)
			if filepath.Base(dir) != tc.want {
				t.Fatalf("selected %s, want %s", filepath.Base(dir), tc.want)
			}
			assertNASRotationPrepareUnchanged(t, before)
			again, _ := prepareNASRotationRequest(t, f.r)
			if again != dir {
				t.Fatal("prepare is not stable")
			}
		})
	}
}

func TestNASRotationPrepareMaturitySelection(t *testing.T) {
	for _, state := range []string{"immature-next", "mature-next", "mature-active"} {
		t.Run(state, func(t *testing.T) {
			f := nasRotationTestFixture(t, "ip")
			now := time.Now().UTC().Truncate(time.Second)
			_, p := prepareNASRotationRequest(t, f.r)
			// Make next keys part of the current fixture publication, so maturity
			// comes from the fake operator's firstPublished, not request timestamps.
			f.payload.Keys = append([]nasRotationKey{}, p.Keys...)
			for _, k := range p.Keys {
				if k.State == "next" {
					age := -23 * time.Hour
					if state != "immature-next" {
						age = -25 * time.Hour
					}
					f.status.First[k.Kid] = now.Add(age).Format(time.RFC3339Nano)
				}
			}
			f.sign(t, filepath.Join(f.r.source, "collab-installation"), f.payload, false)
			if state == "mature-active" {
				var err error
				p, _, err = nasRotationRequest(f.payload, f.status.First, now)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, v := range []string{"8", "32"} {
				p.Version = v
				nasRotationTestJSON(t, filepath.Join(f.r.root, "requests", v, "payload.json"), p)
			}
			want := "32"
			if state == "mature-next" {
				want = "33"
			}
			dir, got := prepareNASRotationRequest(t, f.r)
			if filepath.Base(dir) != want {
				t.Fatalf("selected %s, want %s", filepath.Base(dir), want)
			}
			for _, k := range got.Keys {
				if k.State == "next" && state != "immature-next" {
					t.Fatal("mature activation masked")
				}
			}
		})
	}
}

func TestNASRotationReusableTimeBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 4, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, issued, until string
		want                bool
	}{
		{"issued-now", now.Format(time.RFC3339), now.Add(24 * time.Hour).Format(time.RFC3339), true},
		{"future-nanosecond", now.Add(time.Nanosecond).Format(time.RFC3339Nano), now.Add(24 * time.Hour).Format(time.RFC3339), false},
		{"18h-reserve", now.Add(-6 * time.Hour).Format(time.RFC3339), now.Add(18 * time.Hour).Format(time.RFC3339), true},
		{"short-reserve", now.Format(time.RFC3339), now.Add(18*time.Hour - time.Nanosecond).Format(time.RFC3339Nano), false},
		{"over-24h", now.Format(time.RFC3339), now.Add(24*time.Hour + time.Nanosecond).Format(time.RFC3339Nano), false},
		{"missing-issued", "", now.Add(24 * time.Hour).Format(time.RFC3339), false},
		{"bad-issued", "invalid", now.Add(24 * time.Hour).Format(time.RFC3339), false},
		{"bad-until", now.Format(time.RFC3339), "invalid", false},
		{"inverted", now.Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := nasRotationPayload{Issued: tc.issued, Until: tc.until}
			if got := nasRotationReusable(p, now, false); got != tc.want {
				t.Fatalf("reusable=%v, want %v", got, tc.want)
			}
		})
	}
	for _, mature := range []bool{false, true} {
		p := nasRotationPayload{Issued: now.Format(time.RFC3339), Until: now.Add(24 * time.Hour).Format(time.RFC3339), Keys: []nasRotationKey{{State: "next"}}}
		if nasRotationReusable(p, now, mature) == mature {
			t.Fatal("existing mature/next boundary changed")
		}
	}
}

func TestNASRotationPrepareFutureVersionExhaustion(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	_, p := prepareNASRotationRequest(t, f.r)
	p.Version = "9223372036854775807"
	p.Issued = time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	p.Until = timeMustParse(t, p.Issued).Add(24 * time.Hour).Format(time.RFC3339)
	nasRotationTestJSON(t, filepath.Join(f.r.root, "requests", p.Version, "payload.json"), p)
	// The current v8 request is stale; the high-water mark must not wrap.
	p.Version = "8"
	p.Issued = time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	p.Until = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	nasRotationTestJSON(t, filepath.Join(f.r.root, "requests/8/payload.json"), p)
	before := snapshotNASRotationPrepare(t, f.r.data)
	if err := f.r.prepare(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "version exhausted") {
		t.Fatalf("version high-water mark was bypassed: %v", err)
	}
	assertNASRotationPrepareUnchanged(t, before)
}
