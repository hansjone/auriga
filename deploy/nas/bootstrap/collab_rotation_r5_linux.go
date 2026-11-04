//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func nasRotationHistoricalBinding(old, expected nasRotationBinding) (nasRotationBinding, error) {
	if old.Jar != "" && old.Jar == expected.Jar {
		expected.Image = old.Image
	}
	if old != expected {
		return old, fmt.Errorf("NAS rotation immutable installation binding changed")
	}
	return old, nil
}

func nasRotationReusable(p nasRotationPayload, now time.Time, mature bool) bool {
	// Weekly requests share this archive but may be issued days from now.
	// A single prepare must select a request that is publishable now.
	issued, e := time.Parse(time.RFC3339, p.Issued)
	if e != nil || now.Before(issued) {
		return false
	}
	expiry, e := time.Parse(time.RFC3339, p.Until)
	if e != nil || expiry.Sub(issued) > 24*time.Hour || expiry.Sub(now) < 18*time.Hour {
		return false
	}
	for _, k := range p.Keys {
		if mature && k.State == "next" {
			return false
		}
	}
	return true
}

func (r *nasRotation) runtimePath(rows []nasRotationRelease) (string, error) {
	if len(rows) == 0 {
		return "", nil
	}
	if _, e := os.Lstat(filepath.Join(r.root, "current.json")); os.IsNotExist(e) {
		return "", nil
	} else if e != nil {
		return "", e
	}
	dir, _, e := r.installed(rows, false)
	return dir, e
}

// This is a conservative availability decision, never an authorization to sign.
// A future evaluation supports maintenance planning; a past time cannot extend trust.
func (r *nasRotation) available(rows []nasRotationRelease, at time.Time) error {
	if _, e := os.Lstat(filepath.Join(r.root, ".pending")); !os.IsNotExist(e) {
		return fmt.Errorf("message network degraded: incomplete key operation")
	}
	signed, _, _ := r.latest(rows)
	uid := uint32(0)
	if len(rows) > 0 {
		dir, _, e := r.installed(rows, true)
		if e != nil {
			return e
		}
		signed = filepath.Join(dir, "config/signed.json")
		uid = 10002
	} else if _, e := r.stateCheckpoint(rows); e != nil {
		return e
	}
	b, e := nasRotationRead(signed, uid)
	if e != nil {
		return e
	}
	p, e := nasRotationParsePayload(b)
	if e != nil {
		return e
	}
	expiry, e := time.Parse(time.RFC3339, p.Until)
	if e != nil || !at.Before(expiry) {
		return fmt.Errorf("message network degraded: network keyset expired")
	}
	_, e = nasRotationActive(p, at)
	return e
}

func (r *nasRotation) archiveWork(work, action string) (string, error) {
	if filepath.Dir(work) != r.root || nasRotationDirectory(work, 0) != nil {
		return "", fmt.Errorf("NAS rotation pending archive metadata refused")
	}
	dir := filepath.Join(r.root, "failures")
	if e := os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return "", e
	}
	if e := nasRotationDirectory(dir, 0); e != nil {
		return "", e
	}
	dest, e := os.MkdirTemp(dir, action+"-")
	if e != nil {
		return "", e
	}
	dest = filepath.Join(dest, "pending")
	if e = os.Rename(work, dest); e != nil {
		return "", e
	}
	if e = nasRotationSync(filepath.Dir(dest)); e != nil {
		return "", e
	}
	if e = nasRotationSync(dir); e != nil {
		return "", e
	}
	return dest, nasRotationSync(r.root)
}
