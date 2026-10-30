package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CheckHealth uses public metadata only; it does not replace plugin verification.
func CheckHealth(out string, now time.Time) error {
	if _, err := os.Lstat(filepath.Join(out, ".panel-renew-failed")); !os.IsNotExist(err) {
		return fmt.Errorf("panel renewal failed; inspect host panel-renew.log and panel-prepare logs")
	}
	checked, err := os.Stat(filepath.Join(out, ".panel-renew-checked"))
	if err != nil || now.Sub(checked.ModTime()) > 2*time.Hour || now.Before(checked.ModTime()) {
		return fmt.Errorf("panel renewal scheduler heartbeat missing or stale")
	}
	raw, err := readRegular(filepath.Join(out, "manifest.json"), 256<<10)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil || !now.Before(expires) {
		return fmt.Errorf("panel manifest expired or invalid")
	}
	return nil
}

// RecordCheck exposes both startup signer failures and successful hourly checks.
func RecordCheck(out string, failure error) error {
	if failure != nil {
		return writePublic(filepath.Join(out, ".panel-renew-failed"), []byte("panel publication failed\n"))
	}
	if err := writePublic(filepath.Join(out, ".panel-renew-checked"), []byte("ok\n")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(out, ".panel-renew-failed")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
