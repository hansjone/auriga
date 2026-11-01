package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Check the complete authenticated history, including retired/revoked keys.
// This does not rewrite history or exempt it from the new verifier's policy.
func nasRotationHistoryLifetime(p nasRotationPayload) error {
	if len(p.Keys) == 0 {
		return fmt.Errorf("history lifetime precheck: empty key history")
	}
	for _, k := range p.Keys {
		before, e1 := time.Parse(time.RFC3339Nano, k.Before)
		after, e2 := time.Parse(time.RFC3339Nano, k.After)
		if e1 != nil || e2 != nil || !after.After(before) || after.After(before.Add(8*24*time.Hour)) {
			return fmt.Errorf("history lifetime precheck refused key %s (%s/%s): require 0 < lifetime <= 8 days; stop upgrade and retain history", k.Kid, k.Purpose, k.State)
		}
	}
	return nil
}

func (r *nasRotation) historyCheck(rows []nasRotationRelease, stdout io.Writer) error {
	signedPath, annPath, _ := r.latest(rows)
	signed, err := nasRotationRead(signedPath, 0)
	if err != nil {
		return err
	}
	ann, err := nasRotationRead(annPath, 0)
	if err != nil {
		return err
	}
	p, err := nasRotationParsePayload(signed)
	if err != nil {
		return err
	}
	if _, err = r.authenticateQueuePair(signed, ann, p); err != nil {
		return err
	}
	if err = nasRotationHistoryLifetime(p); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"status": "history_lifetime_checked", "version": p.Version, "keysChecked": len(p.Keys), "maximumLifetimeDays": 8, "signedSha256": nasRotationHash(signed)})
}

// Optional online cross-check for an explicit upgrade, after PostgreSQL is up.
// The observer never starts or waits for a stopped/unready database.
func (r *nasRotation) historyCheckPG(rows []nasRotationRelease, stdout io.Writer) error {
	status, err := r.status(rows)
	if err != nil {
		return err
	}
	signed, _, _ := r.latest(rows)
	raw, err := nasRotationRead(signed, 0)
	if err != nil {
		return err
	}
	p, err := nasRotationParsePayload(raw)
	if err != nil {
		return err
	}
	if p.Version != status.Version {
		return fmt.Errorf("history precheck: PG publication differs from archive")
	}
	return r.historyCheck(rows, stdout)
}

func nasQueueRenewalWarning(until, now time.Time) string {
	if until.Sub(now) > 48*time.Hour {
		return ""
	}
	start := now.UTC().Truncate(12 * time.Hour)
	if now.After(start.Add(6 * time.Hour)) {
		start = start.Add(12 * time.Hour)
	}
	return fmt.Sprintf("预签队列即将用完；48小时内完成下一周离线签名；可准备时段 UTC %s 至 %s（含截止点），已在时段内请立即准备", start.Format(time.RFC3339), start.Add(6*time.Hour).Format(time.RFC3339))
}
