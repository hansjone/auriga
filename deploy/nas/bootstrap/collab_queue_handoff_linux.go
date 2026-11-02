package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The fixed UTC grid and the 6–12h preparation window cannot both be met
// during every hour of the day. Refuse outside the window with an exact retry
// time, rather than silently reducing the operator's offline signing allowance.
func nasQueueStart(now time.Time) (time.Time, error) {
	next := now.UTC().Truncate(12 * time.Hour).Add(12 * time.Hour)
	if next.Sub(now) < 6*time.Hour {
		return time.Time{}, fmt.Errorf("no UTC 12h slot 6–12h ahead; prepare again at %s", next.Format(time.RFC3339))
	}
	return next, nil
}

func nasQueuePosition(q nasQueue, version, signedHash string) (int, error) {
	if version == q.BaseVersion && signedHash == q.BaseSigned {
		return 0, nil
	}
	for i, en := range q.Entries {
		if version == en.Version && signedHash == en.Files["signed.json"] {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("queue_invalidated: current version or signed digest is not this queue's predecessor")
}

func (r *nasRotation) approvedQueues() ([]nasQueue, []string, error) {
	entries, err := os.ReadDir(filepath.Join(r.root, "queues"))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var queues []nasQueue
	var warnings []string
	for _, entry := range entries {
		v := strings.TrimSuffix(entry.Name(), ".json")
		if !nasRotationVersion(v) || entry.Name() != v+".json" {
			return nil, nil, fmt.Errorf("queue archive path refused")
		}
		b, e := nasRotationRead(filepath.Join(r.root, "queues", entry.Name()), 0)
		var q nasQueue
		if e != nil || nasRotationDecode(b, &q) != nil || q.Binding != r.binding || !nasRotationVersion(q.BaseVersion) || !nasRotationDigest.MatchString(q.BaseSigned) || len(q.Entries) < 1 || len(q.Entries) > 28 || q.Entries[0].Version != v {
			warnings = append(warnings, "queue_invalidated: "+v+" metadata/binding missing; re-export and approve a fresh batch")
			continue
		}
		valid := true
		var previous time.Time
		for i, en := range q.Entries {
			at, e1 := time.Parse(time.RFC3339, en.Issued)
			until, e2 := time.Parse(time.RFC3339, en.Until)
			if !nasRotationVersion(en.Version) || e1 != nil || e2 != nil || until.Sub(at) != 24*time.Hour || len(en.Files) != 6 || (i == 0 && mustNASRotationVersion(en.Version) <= mustNASRotationVersion(q.BaseVersion)) || (i > 0 && (mustNASRotationVersion(en.Version) != mustNASRotationVersion(q.Entries[i-1].Version)+1 || at.Sub(previous) != 12*time.Hour)) {
				valid = false
				break
			}
			for _, name := range []string{"payload.json", "trust.json", "input.json", "signed.json", "announcement.json", "approval.json"} {
				if !nasRotationDigest.MatchString(en.Files[name]) {
					valid = false
				}
			}
			previous = at
		}
		if !valid {
			warnings = append(warnings, "queue_invalidated: "+v+" schedule/file hashes missing; prepare and approve a fresh batch")
			continue
		}
		queues = append(queues, q)
	}
	sort.Slice(queues, func(i, j int) bool {
		return mustNASRotationVersion(queues[i].Entries[0].Version) > mustNASRotationVersion(queues[j].Entries[0].Version)
	})
	return queues, warnings, nil
}

func (r *nasRotation) queueMaterial(en nasQueueEntry) (nasRotationPayload, error) {
	dir := filepath.Join(r.root, "requests", en.Version)
	for _, name := range []string{"payload.json", "trust.json", "input.json", "signed.json", "announcement.json", "approval.json"} {
		b, e := nasRotationRead(filepath.Join(dir, name), 0)
		if e != nil || !nasRotationDigest.MatchString(en.Files[name]) || nasRotationHash(b) != en.Files[name] {
			return nasRotationPayload{}, fmt.Errorf("queue material missing/changed; restore approved files or prepare a fresh batch")
		}
	}
	p, e := r.queuePair(dir)
	if e != nil {
		return p, e
	}
	if p.Version != en.Version || p.Issued != en.Issued || p.Until != en.Until {
		return p, fmt.Errorf("queue signed schedule changed")
	}
	return p, nil
}

// Reject a due proposal before entering a publication transaction. A failed
// public PG preflight can then fall back without leaving .pending or stopping
// signers. A failure after publish begins remains fail-closed for recovery.
func (r *nasRotation) queuePreflight(rows []nasRotationRelease, en nasQueueEntry) error {
	work, e := os.MkdirTemp(r.root, ".queue-preflight-")
	if e != nil {
		return e
	}
	defer func() { _ = os.RemoveAll(work) }()
	dir := filepath.Join(r.root, "requests", en.Version)
	plan, e := r.plan(rows, filepath.Join(dir, "signed.json"), filepath.Join(dir, "announcement.json"), r.state, map[string]string{})
	if e != nil {
		return e
	}
	_, e = r.operator("preflight", work, plan)
	return e
}

// Follow only authenticated continuations of the exact current PG publication.
// This computes the predecessor at takeover time without publishing anything.
func (r *nasRotation) queuePredecessor(rows []nasRotationRelease, first map[string]string, at time.Time) (string, string, nasRotationPayload, map[string]string, error) {
	signed, ann, _ := r.latest(rows)
	b, e := nasRotationRead(signed, 0)
	if e != nil {
		return "", "", nasRotationPayload{}, nil, e
	}
	p, e := nasRotationParsePayload(b)
	if e != nil {
		return "", "", p, nil, e
	}
	queues, _, e := r.approvedQueues()
	if e != nil {
		return "", "", p, nil, e
	}
	history := map[string]string{}
	for k, v := range first {
		history[k] = v
	}
	hash := nasRotationHash(b)
	for step := 0; step <= 28*len(queues); step++ {
		advanced := false
		for _, q := range queues {
			index, err := nasQueuePosition(q, p.Version, hash)
			if err != nil || index >= len(q.Entries) {
				continue
			}
			en := q.Entries[index]
			next, err := r.queueMaterial(en)
			if err != nil {
				continue
			}
			issued, _ := time.Parse(time.RFC3339, en.Issued)
			if !issued.Before(at) {
				break
			}
			p = next
			hash = en.Files["signed.json"]
			signed = filepath.Join(r.root, "requests", en.Version, "signed.json")
			ann = filepath.Join(r.root, "requests", en.Version, "announcement.json")
			for _, k := range p.Keys {
				if history[k.Kid] == "" {
					history[k.Kid] = issued.Add(time.Hour).Format(time.RFC3339)
				}
			}
			advanced = true
			break
		}
		if !advanced {
			until, err := time.Parse(time.RFC3339, p.Until)
			if err != nil || !until.After(at) {
				return "", "", p, nil, fmt.Errorf("queue predecessor coverage missing at takeover; publish/approve a bridging batch first")
			}
			return signed, ann, p, history, nil
		}
	}
	return "", "", p, nil, fmt.Errorf("queue continuation limit exceeded")
}
