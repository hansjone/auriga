package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func approveNASQueue(t *testing.T, f *nasRotationFixture) nasQueue {
	t.Helper()
	var archive bytes.Buffer
	if e := f.r.queueExport(&archive); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(&archive)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		nasRotationTestWrite(t, filepath.Join(dir, h.Name), b)
	}
	b, e := os.ReadFile(filepath.Join(dir, "batch.json"))
	if e != nil {
		t.Fatal(e)
	}
	var q nasQueue
	if e = json.Unmarshal(b, &q); e != nil {
		t.Fatal(e)
	}
	for _, en := range q.Entries {
		b, e := os.ReadFile(filepath.Join(dir, en.Version, "payload.json"))
		if e != nil {
			t.Fatal(e)
		}
		var p nasRotationPayload
		if e = json.Unmarshal(b, &p); e != nil {
			t.Fatal(e)
		}
		f.sign(t, filepath.Join(dir, en.Version), p, true)
	}
	f.r.inputDir = dir
	return q
}

func TestNASQueueWeeklyHandoffWithOfflineRaceAndFallback(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	now := time.Now().UTC().Truncate(12 * time.Hour)
	f.payload.Until = now.Add(36 * time.Hour).Format(time.RFC3339)
	for i := range f.payload.Keys {
		f.payload.Keys[i].After = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
	if e := r.queuePrepareAt(nil, 14, io.Discard, now); e != nil {
		t.Fatal(e)
	}
	old := approveNASQueue(t, f)
	if e := r.queueImport(io.Discard); e != nil {
		t.Fatal(e)
	}
	x := nasEnableActivationFixture(t, f)
	// Prepare the replacement before the first old slot, then let the old queue
	// advance while its replacement is offline. A stale base must be refused.
	if e := r.queuePrepareAt(nil, 14, io.Discard, now); e != nil {
		t.Fatal(e)
	}
	stale := approveNASQueue(t, f)
	staleDir := r.inputDir
	first := timeMustParse(t, old.Entries[0].Issued)
	if e := r.queueTickAt(nil, io.Discard, first); e != nil {
		t.Fatal(e)
	}
	rows, e := r.releases()
	if e != nil {
		t.Fatal(e)
	}
	r.inputDir = staleDir
	if e = r.queueImport(io.Discard); e == nil {
		t.Fatal("stale offline batch imported", stale.BaseVersion)
	}
	// Fresh preparation uses the published predecessor and jumps above every
	// reserved version; it does not re-sign or overwrite the stale proposal.
	if e = r.queuePrepareAt(rows, 14, io.Discard, first); e != nil {
		t.Fatal(e)
	}
	fresh := approveNASQueue(t, f)
	if e = r.queueImport(io.Discard); e != nil {
		t.Fatal(e)
	}
	if fresh.BaseVersion != old.Entries[0].Version {
		t.Fatal("wrong handoff predecessor")
	}
	// A broken newer queue cannot eclipse the healthy approved continuation.
	broken := fresh
	broken.Entries = append([]nasQueueEntry(nil), fresh.Entries...)
	broken.Entries[0].Version = "999"
	nasRotationTestJSON(t, filepath.Join(r.root, "queues/999.json"), broken)
	var output bytes.Buffer
	if e = r.queueTickAt(rows, &output, first.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(output.String(), "queue_invalidated_fallback") || !strings.Contains(output.String(), "operatorAction") {
		t.Fatal(output.String())
	}
	if x.created != 2 {
		t.Fatal("future handoff activated early")
	}
	for _, en := range fresh.Entries {
		output.Reset()
		if e = r.queueTickAt(rows, &output, timeMustParse(t, en.Issued)); e != nil {
			t.Fatal(en.Version, e)
		}
		if f.status.Version != en.Version || !strings.Contains(output.String(), "scheduled_publication_installed") {
			t.Fatal(output.String())
		}
		rows, e = r.releases()
		if e != nil {
			t.Fatal(e)
		}
	}
	if x.created != 30 {
		t.Fatal("did not simulate all 14 publications", x.created)
	}
}

func TestNASQueueOfflineWindowAndGrid(t *testing.T) {
	for _, hour := range []int{0, 1, 6, 12, 13, 18} {
		now := time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC)
		start, err := nasQueueStart(now)
		if err != nil || start.Sub(now) < 6*time.Hour || start.Sub(now) > 12*time.Hour || start.Unix()%43200 != 0 {
			t.Fatal(hour, start, err)
		}
	}
	// A fixed 12h grid has no point 6–12h ahead in these half-day windows.
	// Refuse with the next preparation time instead of silently signing too early.
	for _, hour := range []int{7, 11, 19, 23} {
		_, err := nasQueueStart(time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC))
		if err == nil || !strings.Contains(err.Error(), "prepare again at") {
			t.Fatal(hour, err)
		}
	}
}

func TestNASQueueFuturePredecessorAndTamperedLatestFallsBack(t *testing.T) {
	for _, failure := range []string{"material", "preflight"} {
		t.Run(failure, func(t *testing.T) {
			f := nasRotationTestFixture(t, "ip")
			r := f.r
			now := time.Now().UTC().Truncate(12 * time.Hour)
			f.payload.Until = now.Add(36 * time.Hour).Format(time.RFC3339)
			for i := range f.payload.Keys {
				f.payload.Keys[i].After = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
			}
			f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
			if e := r.queuePrepareAt(nil, 14, io.Discard, now); e != nil {
				t.Fatal(e)
			}
			old := approveNASQueue(t, f)
			if e := r.queueImport(io.Discard); e != nil {
				t.Fatal(e)
			}
			first := timeMustParse(t, old.Entries[0].Issued)
			if e := r.queuePrepareAt(nil, 14, io.Discard, first); e != nil {
				t.Fatal(e)
			}
			future := approveNASQueue(t, f)
			if future.BaseVersion != old.Entries[0].Version {
				t.Fatal("did not plan from queued future predecessor")
			}
			if e := r.queueImport(io.Discard); e != nil {
				t.Fatal("future connected predecessor refused", e)
			}
			nasEnableActivationFixture(t, f)
			if e := r.queueTickAt(nil, io.Discard, first); e != nil {
				t.Fatal(e)
			}
			if f.status.Version != old.Entries[0].Version {
				t.Fatal("staged replacement eclipsed old queue")
			}
			rows, e := r.releases()
			if e != nil {
				t.Fatal(e)
			}
			warning := "material missing/changed"
			if failure == "material" {
				nasRotationTestWrite(t, filepath.Join(r.root, "requests", future.Entries[0].Version, "signed.json"), []byte("tampered"))
			} else {
				warning = "production preflight rejected"
				production := r.operator
				r.operator = func(action, work string, plan nasRotationPlan) ([]byte, error) {
					if action == "preflight" && strings.Contains(plan.Signed, "/requests/"+future.Entries[0].Version+"/") {
						return nil, fmt.Errorf("fixture semantic rejection")
					}
					return production(action, work, plan)
				}
			}
			var output bytes.Buffer
			if e = r.queueTickAt(rows, &output, timeMustParse(t, old.Entries[1].Issued)); e != nil {
				t.Fatal(e)
			}
			if f.status.Version != old.Entries[1].Version || !strings.Contains(output.String(), "queue_invalidated_fallback") || !strings.Contains(output.String(), warning) {
				t.Fatal(output.String())
			}
			if _, e := os.Stat(filepath.Join(r.root, ".pending")); !os.IsNotExist(e) {
				t.Fatal("fallback left a pending transaction")
			}
		})
	}
}

func TestNASQueuePositionBindsExactPublication(t *testing.T) {
	q := nasQueue{BaseVersion: "7", BaseSigned: strings.Repeat("a", 64), Entries: []nasQueueEntry{{Version: "10", Files: map[string]string{"signed.json": strings.Repeat("b", 64)}}}}
	for _, c := range []struct {
		v, h string
		want int
	}{{"7", q.BaseSigned, 0}, {"10", strings.Repeat("b", 64), 1}} {
		n, e := nasQueuePosition(q, c.v, c.h)
		if e != nil || n != c.want {
			t.Fatal(n, e)
		}
	}
	for _, c := range []struct{ v, h string }{{"10", q.BaseSigned}, {"99", q.BaseSigned}, {"7", strings.Repeat("c", 64)}} {
		if _, e := nasQueuePosition(q, c.v, c.h); e == nil {
			t.Fatal("external publication accepted", c.v)
		}
	}
}
