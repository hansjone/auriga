package main

import (
	"archive/tar"
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (r *nasRotation) queueExport(w io.Writer) error {
	parent := filepath.Join(r.root, "batches")
	entries, e := os.ReadDir(parent)
	if e != nil {
		return e
	}
	max := int64(0)
	for _, en := range entries {
		if !nasRotationVersion(en.Name()) {
			return fmt.Errorf("batch path refused")
		}
		if n := mustNASRotationVersion(en.Name()); n > max {
			max = n
		}
	}
	if max == 0 {
		return fmt.Errorf("no prepared batch")
	}
	raw, e := nasRotationRead(filepath.Join(parent, jsonNumber(max), "batch.json"), 0)
	if e != nil {
		return e
	}
	var q nasQueue
	if nasRotationDecode(raw, &q) != nil || q.Binding != r.binding || len(q.Entries) == 0 {
		return fmt.Errorf("batch binding refused")
	}
	tw := tar.NewWriter(w)
	put := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	if e = put("batch.json", raw); e != nil {
		return e
	}
	for _, en := range q.Entries {
		if !nasRotationVersion(en.Version) {
			return fmt.Errorf("batch version refused")
		}
		for _, name := range []string{"payload.json", "trust.json", "input.json"} {
			b, err := nasRotationRead(filepath.Join(r.root, "requests", en.Version, name), 0)
			if err != nil || nasRotationHash(b) != en.Files[name] {
				return fmt.Errorf("batch public request changed")
			}
			if err = put(en.Version+"/"+name, b); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

type nasQueueEntry struct {
	Version string            `json:"version"`
	Issued  string            `json:"issuedAt"`
	Until   string            `json:"validUntil"`
	Files   map[string]string `json:"files"`
}
type nasQueue struct {
	Binding     nasRotationBinding `json:"binding"`
	BaseVersion string             `json:"baseVersion"`
	BaseSigned  string             `json:"baseSignedSha256"`
	Entries     []nasQueueEntry    `json:"entries"`
}

func jsonNumber(n int64) string { return strconv.FormatInt(n, 10) }

// Plan only. The real PG publisher still decides maturity from firstPublished.
// An extra hourly scheduler allowance defers promotion to the 36h boundary.
func nasQueuePlan(old nasRotationPayload, actual map[string]string, start time.Time, count int, maxVersion int64) ([]nasRotationPayload, map[string][]byte, error) {
	secrets := map[string][]byte{}
	fail := func(e error) ([]nasRotationPayload, map[string][]byte, error) {
		for _, b := range secrets {
			clear(b)
		}
		return nil, nil, e
	}
	if count < 1 || count > 28 || maxVersion > int64(^uint64(0)>>1)-int64(count) {
		return fail(fmt.Errorf("invalid queue count/version"))
	}
	first := map[string]string{}
	for k, v := range actual {
		first[k] = v
	}
	var result []nasRotationPayload
	for i := 0; i < count; i++ {
		at := start.UTC().Truncate(time.Second).Add(time.Duration(i) * 12 * time.Hour)
		p, keys, e := nasRotationRequest(old, first, at)
		if e != nil {
			return fail(e)
		}
		p.Version = jsonNumber(maxVersion + int64(i) + 1)
		if _, e = nasRotationActive(p, at); e != nil {
			return fail(e)
		}
		for kid, b := range keys {
			secrets[kid] = b
		}
		for _, k := range p.Keys {
			if first[k.Kid] == "" {
				first[k.Kid] = at.Add(time.Hour).Format(time.RFC3339)
			}
		}
		result = append(result, p)
		old = p
	}
	return result, secrets, nil
}

func nasQueueNext(entries []nasQueueEntry, current string) (int, error) {
	for i, e := range entries {
		if e.Version == current {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("pre-signed queue invalidated by an external version; never replay queued releases")
}
func (r *nasRotation) queuePrepare(rows []nasRotationRelease, count int, stdout io.Writer) error {
	status, e := r.status(rows)
	if e != nil {
		return e
	}
	signed, _, _ := r.latest(rows)
	raw, e := nasRotationRead(signed, 0)
	if e != nil {
		return e
	}
	old, e := nasRotationParsePayload(raw)
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(filepath.Join(r.root, "requests"))
	if e != nil {
		return e
	}
	max := mustNASRotationVersion(old.Version)
	for _, en := range entries {
		if !nasRotationVersion(en.Name()) {
			return fmt.Errorf("invalid request archive")
		}
		if n := mustNASRotationVersion(en.Name()); n > max {
			max = n
		}
	}
	ps, private, e := nasQueuePlan(old, status.First, time.Now(), count, max)
	if e != nil {
		return e
	}
	defer func() {
		for _, b := range private {
			clear(b)
		}
	}()
	batch := nasQueue{Binding: r.binding, BaseVersion: old.Version, BaseSigned: nasRotationHash(raw)}
	for _, p := range ps {
		if e = r.saveRequest(rows, p, private); e != nil {
			return e
		}
		dir := filepath.Join(r.root, "requests", p.Version)
		en := nasQueueEntry{Version: p.Version, Issued: p.Issued, Until: p.Until, Files: map[string]string{}}
		for _, name := range []string{"payload.json", "trust.json", "input.json"} {
			b, err := nasRotationRead(filepath.Join(dir, name), 0)
			if err != nil {
				return err
			}
			en.Files[name] = nasRotationHash(b)
		}
		batch.Entries = append(batch.Entries, en)
	}
	parent := filepath.Join(r.root, "batches")
	if e = os.Mkdir(parent, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if e = nasRotationDirectory(parent, 0); e != nil {
		return e
	}
	dir := filepath.Join(parent, ps[0].Version)
	if e = os.Mkdir(dir, 0700); e != nil {
		return e
	}
	if e = nasRotationWriteJSON(filepath.Join(dir, "batch.json"), batch); e != nil {
		return e
	}
	if e = nasRotationSync(parent); e != nil {
		return e
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"status": "awaiting_weekly_offline_signature", "batchDirectory": dir, "count": count, "coverageUntil": ps[len(ps)-1].Until})
}

// Queue import verifies both root signatures without weakening production time
// checks. This subset has ASCII names, integer schema and strings only, so sorted
// JSON is JCS here. Unknown fields/duplicate fields and non-ASCII are refused.
func nasQueueCanonical(raw []byte) ([]byte, error) {
	var v any
	if e := nasRotationDecode(raw, &v); e != nil {
		return nil, e
	}
	var check func(any) bool
	check = func(v any) bool {
		switch x := v.(type) {
		case string:
			for _, c := range x {
				if c < 32 || c > 126 || c == '<' || c == '>' || c == '&' {
					return false
				}
			}
			return true
		case float64:
			return x == 1
		case []any:
			for _, a := range x {
				if !check(a) {
					return false
				}
			}
			return true
		case map[string]any:
			for k, a := range x {
				if !check(k) || !check(a) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !check(v) {
		return nil, fmt.Errorf("noncanonical queue payload")
	}
	return json.Marshal(v)
}
func (r *nasRotation) queuePair(dir string) (nasRotationPayload, error) {
	saved := r.inputDir
	r.inputDir = dir
	defer func() { r.inputDir = saved }()
	in, signed, ann, p, e := r.loadInput("input.json")
	if e != nil {
		return p, e
	}
	if in.Mode != "regular" || len(in.Private) != 0 {
		return p, fmt.Errorf("queue accepts regular public approvals only")
	}
	rootDER, e := nasRotationRead(filepath.Join(r.source, "collab-installation/root.der"), 0)
	if e != nil {
		return p, e
	}
	root, e := x509.ParsePKIXPublicKey(rootDER)
	if e != nil {
		return p, e
	}
	pub, ok := root.(ed25519.PublicKey)
	if !ok {
		return p, fmt.Errorf("queue root must be Ed25519")
	}
	props, e := nasRotationRead(filepath.Join(r.source, "collab-installation/collab.properties"), 0)
	if e != nil {
		return p, e
	}
	kid, fingerprint := "", ""
	for _, line := range strings.Split(string(props), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			switch k {
			case "auriga.collab.network.key-operations.root-kid":
				kid = v
			case "auriga.collab.network.key-operations.root-fingerprint":
				fingerprint = v
			}
		}
	}
	if fingerprint != "sha256:"+nasRotationHash(rootDER) || !nasRotationKid.MatchString(kid) {
		return p, fmt.Errorf("queue independent root pin refused")
	}
	var s struct {
		Keyset json.RawMessage `json:"keyset"`
		Auth   struct {
			Alg       string `json:"alg"`
			Kid       string `json:"kid"`
			Signature string `json:"signature"`
		} `json:"root_auth"`
	}
	if nasRotationDecode(signed, &s) != nil || s.Auth.Alg != "Ed25519" || s.Auth.Kid != kid {
		return p, fmt.Errorf("queue root auth refused")
	}
	canonical, e := nasQueueCanonical(s.Keyset)
	if e != nil {
		return p, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(s.Auth.Signature)
	if e != nil || !ed25519.Verify(pub, append([]byte("Auriga.NetworkKeyset.v1\x00"), canonical...), sig) {
		return p, fmt.Errorf("queue keyset signature refused")
	}
	var a struct {
		Version   string `json:"keyset_version"`
		Until     string `json:"valid_until"`
		Digest    string `json:"manifest_digest"`
		Kid       string `json:"root_kid"`
		Signature string `json:"root_signature"`
	}
	if nasRotationDecode(ann, &a) != nil || a.Version != p.Version || a.Until != p.Until || a.Kid != kid || a.Digest != "sha256:"+nasRotationHash(canonical) {
		return p, fmt.Errorf("queue announcement binding refused")
	}
	body, _ := json.Marshal(map[string]string{"keyset_version": a.Version, "valid_until": a.Until, "manifest_digest": a.Digest, "root_kid": a.Kid})
	sig, e = base64.RawURLEncoding.DecodeString(a.Signature)
	if e != nil || !ed25519.Verify(pub, append([]byte("Auriga.NetworkKeysetAnnouncement.v1\x00"), body...), sig) {
		return p, fmt.Errorf("queue announcement signature refused")
	}
	return p, nil
}

func (r *nasRotation) queueImport(stdout io.Writer) error {
	if nasRotationDirectory(r.inputDir, 0) != nil {
		return fmt.Errorf("queue approval directory refused")
	}
	b, e := nasRotationRead(filepath.Join(r.inputDir, "batch.json"), 0)
	if e != nil {
		return e
	}
	var q nasQueue
	if nasRotationDecode(b, &q) != nil || q.Binding != r.binding || !nasRotationVersion(q.BaseVersion) || !nasRotationDigest.MatchString(q.BaseSigned) || len(q.Entries) < 1 || len(q.Entries) > 28 {
		return fmt.Errorf("queue binding refused")
	}
	if !nasRotationVersion(q.Entries[0].Version) || mustNASRotationVersion(q.Entries[0].Version) <= mustNASRotationVersion(q.BaseVersion) {
		return fmt.Errorf("queue base version refused")
	}
	originalBatch, err := nasRotationRead(filepath.Join(r.root, "batches", q.Entries[0].Version, "batch.json"), 0)
	if err != nil || !bytes.Equal(originalBatch, b) {
		return fmt.Errorf("queue differs from immutable prepared batch")
	}
	var previous time.Time
	var version int64
	material := map[string]map[string][]byte{}
	for i, en := range q.Entries {
		if len(en.Files) != 3 {
			return fmt.Errorf("queue public request file set refused")
		}
		if !nasRotationVersion(en.Version) || i > 0 && mustNASRotationVersion(en.Version) != version+1 {
			return fmt.Errorf("queue version sequence refused")
		}
		version = mustNASRotationVersion(en.Version)
		issued, e1 := time.Parse(time.RFC3339, en.Issued)
		until, e2 := time.Parse(time.RFC3339, en.Until)
		if e1 != nil || e2 != nil || until.Sub(issued) != 24*time.Hour || i > 0 && issued.Sub(previous) != 12*time.Hour {
			return fmt.Errorf("queue time sequence refused")
		}
		previous = issued
		local := filepath.Join(r.root, "requests", en.Version)
		approved := filepath.Join(r.inputDir, en.Version)
		if nasRotationDirectory(local, 0) != nil {
			return fmt.Errorf("queue requires original private request")
		}
		for _, name := range []string{"payload.json", "trust.json", "input.json"} {
			original, err := nasRotationRead(filepath.Join(local, name), 0)
			if err != nil || nasRotationHash(original) != en.Files[name] {
				return fmt.Errorf("queue original request changed")
			}
			incoming, err := nasRotationRead(filepath.Join(approved, name), 0)
			if err != nil || !bytes.Equal(original, incoming) {
				return fmt.Errorf("queue approved request differs")
			}
		}
		p, err := r.queuePair(approved)
		if err != nil {
			return err
		}
		if p.Version != en.Version || p.Issued != en.Issued || p.Until != en.Until {
			return fmt.Errorf("queue payload schedule differs")
		}
		material[en.Version] = map[string][]byte{}
		for _, name := range []string{"signed.json", "announcement.json", "approval.json"} {
			raw, err := nasRotationRead(filepath.Join(approved, name), 0)
			if err != nil {
				return err
			}
			material[en.Version][name] = raw
			en.Files[name] = nasRotationHash(raw)
		}
		q.Entries[i] = en
	}
	// All pairs are authenticated before recording anything. Interrupted imports
	// are idempotent only for identical bytes; conflicting versions never overwrite.
	for v, files := range material {
		for name, b := range files {
			path := filepath.Join(r.root, "requests", v, name)
			old, e := nasRotationRead(path, 0)
			if e == nil {
				if !bytes.Equal(old, b) {
					return fmt.Errorf("queue same version changed")
				}
			} else if os.IsNotExist(e) {
				if e = nasRotationWrite(path, b); e != nil {
					return e
				}
			} else {
				return e
			}
		}
	}
	parent := filepath.Join(r.root, "queues")
	if e = os.Mkdir(parent, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if e = nasRotationDirectory(parent, 0); e != nil {
		return e
	}
	path := filepath.Join(parent, q.Entries[0].Version+".json")
	raw, _ := json.Marshal(q)
	if old, err := nasRotationRead(path, 0); err == nil {
		if !bytes.Equal(old, raw) {
			return fmt.Errorf("queue immutable approval changed")
		}
	} else if os.IsNotExist(err) {
		if e = nasRotationWrite(path, raw); e != nil {
			return e
		}
	} else {
		return err
	}
	if e = nasRotationSync(parent); e != nil {
		return e
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"status": "weekly_queue_approved", "count": len(q.Entries), "coverageUntil": q.Entries[len(q.Entries)-1].Until})
}

func (r *nasRotation) queueTick(rows []nasRotationRelease, stdout io.Writer) error {
	if _, e := os.Lstat(filepath.Join(r.root, "scheduler-paused.json")); !os.IsNotExist(e) {
		return json.NewEncoder(stdout).Encode(map[string]string{"status": "scheduler_paused_by_operator"})
	}
	parent := filepath.Join(r.root, "queues")
	files, e := os.ReadDir(parent)
	if os.IsNotExist(e) || e == nil && len(files) == 0 {
		return r.prepare(rows, stdout)
	}
	if e != nil {
		return e
	}
	var q nasQueue
	max := int64(0)
	for _, file := range files {
		v := strings.TrimSuffix(file.Name(), ".json")
		if !nasRotationVersion(v) || file.Name() != v+".json" {
			return fmt.Errorf("queue archive path refused")
		}
		if n := mustNASRotationVersion(v); n > max {
			max = n
			b, err := nasRotationRead(filepath.Join(parent, file.Name()), 0)
			if err != nil {
				return err
			}
			if nasRotationDecode(b, &q) != nil {
				return fmt.Errorf("queue record refused")
			}
		}
	}
	if q.Binding != r.binding || len(q.Entries) == 0 {
		return fmt.Errorf("queue binding changed")
	}
	// Production status validates the exact PG pair against the immutable receipt.
	// An emergency jump therefore blocks this queue even before version selection.
	status, e := r.status(rows)
	if e != nil {
		return fmt.Errorf("queue stopped: PG differs or unavailable; inspect emergency publication: %w", e)
	}
	index := 0
	if status.Version == q.BaseVersion {
		signed, _, _ := r.latest(rows)
		b, err := nasRotationRead(signed, 0)
		if err != nil || nasRotationHash(b) != q.BaseSigned {
			return fmt.Errorf("queue base publication changed")
		}
	} else {
		index, e = nasQueueNext(q.Entries, status.Version)
		if e != nil {
			return e
		}
	}
	if index > 0 {
		r.inputDir = filepath.Join(r.root, "requests", q.Entries[index-1].Version)
		if _, _, err := r.installed(rows, true); err != nil {
			if err = r.install(); err != nil {
				return err
			}
		}
		if err := r.engine.activate(r); err != nil {
			return err
		}
	}
	until, e := time.Parse(time.RFC3339, q.Entries[len(q.Entries)-1].Until)
	if e != nil {
		return e
	}
	warning := ""
	if time.Until(until) <= 48*time.Hour {
		warning = "预签队列即将用完；48小时内完成下一周离线签名"
	}
	result := map[string]any{"status": "waiting_for_scheduled_publication", "coverageUntil": until.Format(time.RFC3339), "warning": warning, "activationRequired": false}
	if index >= len(q.Entries) {
		result["status"] = "queue_exhausted"
		return json.NewEncoder(stdout).Encode(result)
	}
	en := q.Entries[index]
	issued, e := time.Parse(time.RFC3339, en.Issued)
	if e != nil {
		return e
	}
	if time.Now().Before(issued) {
		return json.NewEncoder(stdout).Encode(result)
	}
	dir := filepath.Join(r.root, "requests", en.Version)
	for name, hash := range en.Files {
		if name != "payload.json" && name != "trust.json" && name != "input.json" && name != "signed.json" && name != "announcement.json" && name != "approval.json" {
			return fmt.Errorf("queue unexpected file")
		}
		b, err := nasRotationRead(filepath.Join(dir, name), 0)
		if err != nil || nasRotationHash(b) != hash {
			return fmt.Errorf("queue material changed")
		}
	}
	if _, e = r.queuePair(dir); e != nil {
		return e
	}
	r.inputDir = dir
	if e = r.publish(); e != nil {
		return e
	}
	if e = r.install(); e != nil {
		return e
	}
	if e = r.engine.activate(r); e != nil {
		return e
	}
	result["status"] = "scheduled_publication_installed"
	result["version"] = en.Version
	result["activationRequired"] = true
	return json.NewEncoder(stdout).Encode(result)
}

func (r *nasRotation) schedulerControl(action string) error {
	path := filepath.Join(r.root, "scheduler-paused.json")
	if action == "pause" {
		if _, e := nasRotationRead(path, 0); e == nil {
			return nil
		} else if !os.IsNotExist(e) {
			return e
		}
		return nasRotationWriteJSON(path, map[string]string{"status": "operator-paused"})
	}
	if _, e := nasRotationRead(path, 0); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	if e := os.Remove(path); e != nil {
		return e
	}
	return nasRotationSync(r.root)
}
