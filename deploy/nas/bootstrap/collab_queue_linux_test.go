package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNASQueueExportImportScheduleAndPause(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	f.payload.Until = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for i := range f.payload.Keys {
		f.payload.Keys[i].After = time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	}
	f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
	if e := r.queuePrepareAt(nil, 14, io.Discard, time.Now().UTC().Truncate(12*time.Hour)); e != nil {
		t.Fatal(e)
	}
	var archive bytes.Buffer
	if e := r.queueExport(&archive); e != nil {
		t.Fatal(e)
	}
	approved := t.TempDir()
	if e := os.Chmod(approved, 0700); e != nil {
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
		if filepath.Base(h.Name) == "keys" || (filepath.Ext(h.Name) != ".json" && h.Name != "base/root.der") {
			t.Fatal("private export")
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		nasRotationTestWrite(t, filepath.Join(approved, h.Name), b)
	}
	raw, _ := os.ReadFile(filepath.Join(approved, "batch.json"))
	var q nasQueue
	_ = json.Unmarshal(raw, &q)
	for _, en := range q.Entries {
		dir := filepath.Join(approved, en.Version)
		b, _ := os.ReadFile(filepath.Join(dir, "payload.json"))
		var p nasRotationPayload
		_ = json.Unmarshal(b, &p)
		f.sign(t, dir, p, true)
	}
	r.inputDir = approved
	if e := r.queueImport(io.Discard); e != nil {
		t.Fatal(e)
	}
	if e := r.queueImport(io.Discard); e != nil {
		t.Fatal("identical import", e)
	}
	// Install input + every private key remain only on NAS. Export cannot contain them.
	for _, en := range q.Entries {
		if _, e := os.Stat(filepath.Join(approved, en.Version, "install-input.json")); !os.IsNotExist(e) {
			t.Fatal("private installation exported")
		}
	}
	if e := r.schedulerControl("pause"); e != nil {
		t.Fatal(e)
	}
	var result bytes.Buffer
	if e := r.queueTick(nil, &result); e != nil || !bytes.Contains(result.Bytes(), []byte("scheduler_paused")) {
		t.Fatal(e, result.String())
	}
	if e := r.schedulerControl("resume"); e != nil {
		t.Fatal(e)
	}
	// Advancing externally cannot fall back to an older queue or prepare/publish.
	f.status.Version = "999"
	if e := r.queueTick(nil, io.Discard); e == nil {
		t.Fatal("emergency version replayed queue")
	}
	if f.stops != 0 {
		t.Fatal("observer stopped signers before schedule check")
	}
}

func TestNASPendingRecoveryBeforeAndAfterPGCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f := nasRotationTestFixture(t, "ip")
			r := f.r
			f.request(t)
			production := r.operator
			r.operator = func(action, work string, p nasRotationPlan) ([]byte, error) {
				if action == "publish" {
					if committed {
						if _, e := production(action, work, p); e != nil {
							return nil, e
						}
					}
					return nil, fmt.Errorf("interrupted")
				}
				if action == "status" {
					b, _ := os.ReadFile(p.Expected)
					expected, _ := nasRotationParsePayload(b)
					if expected.Version != f.status.Version {
						return nil, fmt.Errorf("PG checkpoint differs")
					}
				}
				return production(action, work, p)
			}
			if e := r.publish(); e == nil {
				t.Fatal("failure injection")
			}
			if e := r.recoverPending(nil, io.Discard); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(filepath.Join(r.root, ".pending")); !os.IsNotExist(e) {
				t.Fatal("pending retained after verified recovery")
			}
			rows, e := r.releases()
			if e != nil {
				t.Fatal(e)
			}
			if committed && len(rows) != 1 || !committed && len(rows) != 0 {
				t.Fatal("PG recovery invented/lost publication")
			}
			if committed {
				if e = r.install(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestNASRotationR5AvailabilityOnlyDegradesMessages(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	if e := r.available(nil, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := r.available(nil, time.Now().Add(2*time.Hour)); e == nil {
		t.Fatal("expired manifest accepted")
	}
	dir := f.request(t)
	if e := r.publish(); e != nil {
		t.Fatal(e)
	}
	rows, e := r.releases()
	if e != nil {
		t.Fatal(e)
	}
	if e = r.available(rows, time.Now()); e == nil {
		t.Fatal("published but uninstalled permitted")
	}
	if path, err := r.runtimePath(rows); err != nil || path != "" {
		t.Fatal("stop/status lost initial runtime while first publication awaits install", err)
	}
	r.inspectOnly = true
	if _, e = r.status(rows); e != nil {
		t.Fatal("status blocked pending installation", e)
	}
	r.inspectOnly = false
	if e = r.install(); e != nil {
		t.Fatal(e)
	}
	if e = r.available(rows, time.Now()); e != nil {
		t.Fatal(e)
	}
	// Business expiry is independent of a still-current keyset.
	b, _ := os.ReadFile(filepath.Join(dir, "payload.json"))
	var p nasRotationPayload
	_ = json.Unmarshal(b, &p)
	if _, e = nasRotationActive(p, time.Now().Add(2*time.Hour)); e == nil {
		t.Fatal("expired business key accepted")
	}
}

func TestNASQueueWeeklyPlanAndEmergencyFence(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	now := time.Now().UTC().Truncate(time.Second)
	for i := range f.payload.Keys {
		f.payload.Keys[i].After = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	ps, secrets, e := nasQueuePlan(f.payload, f.status.First, now, 14, 10)
	defer func() {
		for _, b := range secrets {
			clear(b)
		}
	}()
	if e != nil || len(ps) != 14 {
		t.Fatal(e)
	}
	first := map[string]time.Time{}
	for i, p := range ps {
		issued, _ := time.Parse(time.RFC3339, p.Issued)
		until, _ := time.Parse(time.RFC3339, p.Until)
		if p.Version != jsonNumber(int64(11+i)) || issued != now.Add(time.Duration(i)*12*time.Hour) || until.Sub(issued) != 24*time.Hour {
			t.Fatal("noncontinuous weekly schedule")
		}
		for _, k := range p.Keys {
			if _, ok := first[k.Kid]; !ok {
				first[k.Kid] = issued
			}
			if k.State == "active" && secrets[k.Kid] != nil && issued.Sub(first[k.Kid]) < 36*time.Hour {
				t.Fatal("activation lacks delayed-publication margin")
			}
		}
	}
	queue := nasQueue{Entries: []nasQueueEntry{{Version: "11", Files: map[string]string{"signed.json": "approved"}}, {Version: "12"}}}
	if _, e = nasQueuePosition(queue, "99", "approved"); e == nil {
		t.Fatal("emergency jump did not invalidate queue")
	}
	if n, e := nasQueuePosition(queue, "11", "approved"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestNASQueueImportAuthenticatesFuturePairAndAudit(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	dir := f.request(t)
	if _, e := f.r.queuePair(dir); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "signed.json"))
	var signed map[string]any
	_ = json.Unmarshal(b, &signed)
	signed["keyset"].(map[string]any)["issued_at"] = "2099-01-01T00:00:00Z"
	nasRotationTestJSON(t, filepath.Join(dir, "signed.json"), signed)
	if _, e := f.r.queuePair(dir); e == nil {
		t.Fatal("tampered future pair accepted")
	}
}

func TestNASQueueDuePublicationThenFutureWait(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	f.payload.Until = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for i := range f.payload.Keys {
		f.payload.Keys[i].After = time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	}
	f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
	if e := r.queuePrepareAt(nil, 2, io.Discard, time.Now().UTC().Truncate(12*time.Hour)); e != nil {
		t.Fatal(e)
	}
	batch, err := nasRotationRead(filepath.Join(r.root, "batches/8/batch.json"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var q nasQueue
	if err = nasRotationDecode(batch, &q); err != nil {
		t.Fatal(err)
	}
	approved := t.TempDir()
	if err = os.Chmod(approved, 0700); err != nil {
		t.Fatal(err)
	}
	nasRotationTestWrite(t, filepath.Join(approved, "batch.json"), batch)
	for _, name := range []string{"signed.json", "announcement.json", "root.der"} {
		b, err := nasRotationRead(filepath.Join(r.root, "batches/8/base", name), 0)
		if err != nil {
			t.Fatal(err)
		}
		nasRotationTestWrite(t, filepath.Join(approved, "base", name), b)
	}
	for _, en := range q.Entries {
		local := filepath.Join(r.root, "requests", en.Version)
		dest := filepath.Join(approved, en.Version)
		for _, name := range []string{"payload.json", "trust.json", "input.json"} {
			b, e := nasRotationRead(filepath.Join(local, name), 0)
			if e != nil {
				t.Fatal(e)
			}
			nasRotationTestWrite(t, filepath.Join(dest, name), b)
		}
		b, _ := os.ReadFile(filepath.Join(dest, "payload.json"))
		var p nasRotationPayload
		_ = json.Unmarshal(b, &p)
		f.sign(t, dest, p, true)
	}
	r.inputDir = approved
	if err = r.queueImport(io.Discard); err != nil {
		t.Fatal(err)
	}
	x := nasEnableActivationFixture(t, f)
	var first bytes.Buffer
	due := timeMustParse(t, q.Entries[0].Issued)
	if err = r.queueTickAt(nil, &first, due); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first.Bytes(), []byte("scheduled_publication_installed")) || f.status.Version != "8" || x.created != 2 {
		t.Fatal(first.String())
	}
	rows, err := r.releases()
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err = r.queueTickAt(rows, &second, due.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(second.Bytes(), []byte("waiting_for_scheduled_publication")) || f.status.Version != "8" || x.created != 2 {
		t.Fatal("future queued release published early", second.String())
	}
}

func TestNASPendingInstallRecoversJavaCommitAndSelector(t *testing.T) {
	for _, phase := range []string{"java_committed", "selector_missing", "changed_install"} {
		t.Run(phase, func(t *testing.T) {
			f := nasRotationTestFixture(t, "ip")
			r := f.r
			f.request(t)
			before, e := r.stateBytes()
			if e != nil {
				t.Fatal(e)
			}
			if e = r.publish(); e != nil {
				t.Fatal(e)
			}
			rows, e := r.releases()
			if e != nil {
				t.Fatal(e)
			}
			if phase == "java_committed" {
				production := r.operator
				r.operator = func(action, work string, p nasRotationPlan) ([]byte, error) {
					b, e := production(action, work, p)
					if action == "install" && e == nil {
						return nil, fmt.Errorf("crash after Java state commit before chown")
					}
					return b, e
				}
				if e = r.install(); e == nil {
					t.Fatal("injection did not interrupt")
				}
				r.operator = production
			} else {
				if e = r.install(); e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(filepath.Join(r.root, "current.json")); e != nil {
					t.Fatal(e)
				}
				pending := filepath.Join(r.root, ".pending")
				if e = os.Mkdir(pending, 0700); e != nil {
					t.Fatal(e)
				}
				nasRotationTestJSON(t, filepath.Join(pending, "intent.json"), nasRotationIntent{Action: "install", Input: r.inputDir, Before: nasRotationHash(before)})
				if phase == "changed_install" {
					if e = os.WriteFile(filepath.Join(r.root, "installs/8/config/signed.json"), []byte("changed"), 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			e = r.recoverPending(rows, io.Discard)
			if phase == "changed_install" {
				if e == nil {
					t.Fatal("tampered installation adopted")
				}
				if _, err := os.Lstat(filepath.Join(r.root, "current.json")); !os.IsNotExist(err) {
					t.Fatal("invalid selector committed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = r.installed(rows, true); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Lstat(filepath.Join(r.root, ".pending")); !os.IsNotExist(e) {
				t.Fatal("recoverable pending persisted")
			}
		})
	}
}
