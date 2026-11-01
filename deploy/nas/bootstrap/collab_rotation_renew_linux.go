package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (r *nasRotation) privateKey(kid string, rows []nasRotationRelease) ([]byte, error) {
	if _, e := os.Lstat(filepath.Join(r.root, "current.json")); e == nil {
		dir, _, e := r.installed(rows, false)
		if e != nil {
			return nil, e
		}
		if b, e := nasRotationRead(filepath.Join(dir, "config/keys", kid+".der"), 10002); e == nil {
			return b, nil
		}
	}
	requests, e := os.ReadDir(filepath.Join(r.root, "requests"))
	if e != nil {
		return nil, e
	}
	for _, request := range requests {
		if !nasRotationVersion(request.Name()) {
			return nil, fmt.Errorf("NAS renewal invalid request archive")
		}
		dir := filepath.Join(r.root, "requests", request.Name())
		if nasRotationDirectory(dir, 0) != nil {
			return nil, fmt.Errorf("NAS renewal request metadata refused")
		}
		if b, e := nasRotationRead(filepath.Join(dir, "keys", kid+".der"), 0); e == nil {
			return b, nil
		}
	}
	b, e := nasRotationRead(filepath.Join(r.source, "collab-installation/signed.json"), 0)
	if e != nil {
		return nil, e
	}
	p, e := nasRotationParsePayload(b)
	if e != nil {
		return nil, e
	}
	for _, k := range p.Keys {
		if k.Kid == kid {
			leaf := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}[k.Purpose]
			if leaf != "" {
				return nasRotationRead(filepath.Join(r.source, "collab-installation", leaf), 0)
			}
		}
	}
	return nil, fmt.Errorf("NAS renewal private material unavailable")
}
func nasRotationEligible(p nasRotationPayload, first map[string]string) string {
	var latest time.Time
	found := false
	for _, key := range p.Keys {
		if key.State != "next" {
			continue
		}
		t, e := time.Parse(time.RFC3339Nano, first[key.Kid])
		if e != nil {
			return ""
		}
		t = t.Add(24 * time.Hour)
		if t.Nanosecond() != 0 {
			t = t.Truncate(time.Second).Add(time.Second)
		}
		if t.After(latest) {
			latest = t
		}
		found = true
	}
	if !found {
		return ""
	}
	return latest.UTC().Format(time.RFC3339)
}
func (r *nasRotation) prepare(rows []nasRotationRelease, stdout io.Writer) error {
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
	now := time.Now().UTC()
	if _, e := time.Parse(time.RFC3339, old.Until); e != nil {
		return e
	}
	warning := nasRotationExpiryWarning(old, now)
	entries, e := os.ReadDir(filepath.Join(r.root, "requests"))
	if e != nil {
		return e
	}
	max, _ := strconv.ParseInt(old.Version, 10, 64)
	var reusable string
	eligible := nasRotationEligible(old, status.First)
	mature := false
	if boundary, err := time.Parse(time.RFC3339, eligible); err == nil {
		mature = !now.Before(boundary)
	}
	for _, entry := range entries {
		v := entry.Name()
		if !nasRotationVersion(v) {
			return fmt.Errorf("NAS renewal invalid request version")
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		if n > max {
			max = n
		}
		if n <= mustNASRotationVersion(old.Version) {
			continue
		}
		dir := filepath.Join(r.root, "requests", v)
		b, e := nasRotationRead(filepath.Join(dir, "payload.json"), 0)
		if e != nil {
			return e
		}
		var p nasRotationPayload
		if nasRotationDecode(b, &p) != nil || p.Version != v || p.Realm != old.Realm {
			return fmt.Errorf("NAS renewal pending request changed")
		}
		expiry, e := time.Parse(time.RFC3339, p.Until)
		// A pre-maturity request must not mask the newly eligible active request.
		containsNext := false
		for _, key := range p.Keys {
			if key.State == "next" {
				containsNext = true
			}
		}
		if e == nil && now.Before(expiry.Add(-6*time.Hour)) && (!mature || !containsNext) {
			reusable = dir
		}
	}
	if reusable != "" && !r.recovery {
		return json.NewEncoder(stdout).Encode(map[string]any{"status": "awaiting_offline_root_signature", "requestDirectory": reusable, "warning": warning, "operatorIntervalHours": 12, "earliestNextActivation": nasRotationEligible(old, status.First)})
	}
	var p nasRotationPayload
	var private map[string][]byte
	if r.recovery {
		db, err := time.Parse(time.RFC3339Nano, status.Time)
		if err != nil {
			return err
		}
		p, private, e = nasRotationRecoveryRequest(old, now, db)
	} else {
		p, private, e = nasRotationRequest(old, status.First, now)
	}
	if e != nil {
		return e
	}
	defer func() {
		for _, b := range private {
			clear(b)
		}
	}()
	if max == int64(^uint64(0)>>1) {
		return fmt.Errorf("NAS renewal version exhausted")
	}
	p.Version = strconv.FormatInt(max+1, 10)
	// Each unsigned request is immutable. Root-signed publication/install is separate.
	return r.mutate("prepare", func(work string) error {
		stage := filepath.Join(work, "request")
		if e := os.Mkdir(stage, 0700); e != nil {
			return e
		}
		if e := os.Mkdir(filepath.Join(stage, "keys"), 0700); e != nil {
			return e
		}
		files := map[string]string{}
		for _, k := range p.Keys {
			if k.State != "active" && k.State != "next" {
				continue
			}
			b := private[k.Kid]
			if b == nil {
				b, e = r.privateKey(k.Kid, rows)
				if e != nil {
					return e
				}
				defer clear(b)
			}
			name := "keys/" + k.Kid + ".der"
			if e = nasRotationWrite(filepath.Join(stage, name), b); e != nil {
				return e
			}
			files[k.Kid] = name
		}
		if e = nasRotationWriteJSON(filepath.Join(stage, "payload.json"), p); e != nil {
			return e
		}
		properties, e := nasRotationRead(filepath.Join(r.source, "collab-installation/collab.properties"), 0)
		if e != nil {
			return e
		}
		mode := "regular"
		if r.recovery {
			mode = "expired_recovery"
		}
		trust := map[string]string{"baseReceiptSha256": r.binding.Base, "activationReceiptSha256": r.binding.Source, "platformImage": r.binding.Image, "operatorJarSha256": r.binding.Jar, "publicationMode": mode}
		for _, line := range strings.Split(string(properties), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "auriga.collab.network.key-operations.tenant-id":
				trust["tenantId"] = value
			case "auriga.collab.network.key-operations.realm":
				trust["realm"] = value
			case "auriga.collab.network.key-operations.root-kid":
				trust["rootKid"] = value
			case "auriga.collab.network.key-operations.root-fingerprint":
				trust["rootFingerprint"] = value
			}
		}
		clear(properties)
		if trust["tenantId"] == "" || trust["realm"] != p.Realm || trust["rootKid"] == "" || trust["rootFingerprint"] == "" {
			return fmt.Errorf("NAS renewal trusted installation metadata missing")
		}
		if e = nasRotationWriteJSON(filepath.Join(stage, "trust.json"), trust); e != nil {
			return e
		}
		input := nasRotationInput{Base: r.binding.Base, Source: r.binding.Source, Image: r.binding.Image, Jar: r.binding.Jar, Mode: mode, Signed: "signed.json", Announcement: "announcement.json", Private: files}
		if e = nasRotationWriteJSON(filepath.Join(stage, "install-input.json"), input); e != nil {
			return e
		}
		input.Private = map[string]string{}
		if e = nasRotationWriteJSON(filepath.Join(stage, "input.json"), input); e != nil {
			return e
		}
		if e = nasRotationSync(stage); e != nil {
			return e
		}
		dir := filepath.Join(r.root, "requests", p.Version)
		if e = os.Rename(stage, dir); e != nil {
			return e
		}
		if e = nasRotationSync(filepath.Join(r.root, "requests")); e != nil {
			return e
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"status": "awaiting_offline_root_signature", "requestDirectory": dir, "warning": warning, "operatorIntervalHours": 12, "earliestNextActivation": nasRotationEligible(old, status.First)})
	})
}
func mustNASRotationVersion(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

func nasRotationExpiryWarning(p nasRotationPayload, now time.Time) string {
	boundary := now.Add(6 * time.Hour)
	until, e := time.Parse(time.RFC3339, p.Until)
	if e != nil || !until.After(boundary) {
		return "keyset_expires_within_6h_operator_signature_required"
	}
	for _, key := range p.Keys {
		if key.State != "active" {
			continue
		}
		after, e := time.Parse(time.RFC3339, key.After)
		if e != nil || !after.After(boundary) {
			return "business_keys_expire_within_6h_operator_action_required"
		}
	}
	return ""
}

func nasRotationRecoveryRequest(old nasRotationPayload, now, database time.Time) (nasRotationPayload, map[string][]byte, error) {
	diff := now.Sub(database)
	if diff < -60*time.Second || diff > 60*time.Second {
		return old, nil, fmt.Errorf("recovery application/database clock mismatch")
	}
	now = now.UTC().Truncate(time.Second)
	cutoff := now.Add(-360 * time.Second)
	until, e := time.Parse(time.RFC3339, old.Until)
	if e != nil || until.After(cutoff) {
		return old, nil, fmt.Errorf("old publication has not exhausted its clearance window")
	}
	previous := old
	previous.Keys = append([]nasRotationKey{}, old.Keys...)
	for i, k := range previous.Keys {
		after, e := time.Parse(time.RFC3339, k.After)
		if e != nil || after.After(cutoff) {
			return old, nil, fmt.Errorf("all old keys must be expired for 360 seconds")
		}
		if k.State != "revoked" {
			previous.Keys[i].State = "retired"
		}
	}
	p, private, e := nasRotationRequest(previous, nil, now)
	if e != nil {
		return old, nil, e
	}
	for i, k := range p.Keys {
		if private[k.Kid] != nil {
			p.Keys[i].State = "active"
		}
	}
	return p, private, nil
}
