package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type nasRotationIntent struct {
	Action string `json:"action"`
	Input  string `json:"inputDirectory"`
	Before string `json:"beforeStateSha256"`
}

// Explicit, locked recovery. Evidence is moved into failures, never deleted.
// PG is neither rolled back nor overwritten and its firstPublished remains truth.
func (r *nasRotation) recoverPending(rows []nasRotationRelease, w io.Writer) error {
	pending := filepath.Join(r.root, ".pending")
	raw, e := nasRotationRead(filepath.Join(pending, "intent.json"), 0)
	var intent nasRotationIntent
	if e != nil || nasRotationDecode(raw, &intent) != nil || !nasRotationDigest.MatchString(intent.Before) {
		return fmt.Errorf("pending lacks a verifiable intent; preserve directory for operator investigation")
	}
	state, e := nasRotationRead(r.state, 10002)
	if e != nil && intent.Action == "install" {
		state, e = nasRotationRead(r.state, 0)
		r.recoveryRootState = e == nil
	}
	if e != nil {
		return e
	}
	r.inspectOnly = true
	defer func() { r.inspectOnly = false; r.recoveryPrevious = nil; r.recoveryRootState = false }()
	// No external change: archive the failed attempt and permit an exact retry.
	if !r.recoveryRootState && nasRotationHash(state) == intent.Before {
		checkpoint, checkpointErr := r.stateCheckpoint(rows)
		if _, err := r.status(rows); err == nil && checkpointErr == nil && checkpoint == nasRotationHash(state) {
			dest, err := r.archiveWork(pending, intent.Action)
			if err != nil {
				return err
			}
			return json.NewEncoder(w).Encode(map[string]string{"status": "safe_retry_allowed", "evidenceDirectory": dest})
		}
	}
	if !filepath.IsAbs(intent.Input) || filepath.Clean(intent.Input) != intent.Input {
		return fmt.Errorf("pending input path refused")
	}
	r.inputDir = intent.Input
	switch intent.Action {
	case "publish", "recover-expired":
		in, signed, ann, p, err := r.loadInput("input.json")
		if err != nil {
			return err
		}
		// Re-query PG with the candidate as checkpoint. Only an exact acknowledged
		// pair can finish the local receipt after a crash following DB commit.
		work, err := os.MkdirTemp(r.root, ".recover-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(work) }()
		plan, err := r.plan(rows, filepath.Join(intent.Input, in.Signed), filepath.Join(intent.Input, in.Announcement), r.state, map[string]string{})
		if err != nil {
			return err
		}
		plan.Expected = plan.Signed
		plan.ExpectedAnn = plan.Announcement
		plan.History = ""
		status, err := r.operator("status", work, plan)
		if err != nil {
			return fmt.Errorf("pending PG is neither previous nor candidate; manual investigation required")
		}
		var proof nasRotationStatus
		if nasRotationDecode(status, &proof) != nil || proof.Version != p.Version || len(proof.First) == 0 || nasRotationHash(state) != intent.Before {
			return fmt.Errorf("pending PG/state recovery refused")
		}
		if len(rows) > 0 && rows[len(rows)-1].Version == p.Version {
			return fmt.Errorf("publication receipt already committed; inspect pending evidence")
		}
		stage := filepath.Join(work, "release")
		if err = os.Mkdir(stage, 0700); err != nil {
			return err
		}
		files := map[string][]byte{"signed.json": signed, "announcement.json": ann, "pg-status.json": status}
		for _, name := range []string{"approval.json", "payload.json"} {
			b, err := nasRotationRead(filepath.Join(intent.Input, name), 0)
			if err != nil {
				return err
			}
			files[name] = b
		}
		hashes := map[string]string{}
		for name, b := range files {
			if err = nasRotationWrite(filepath.Join(stage, name), b); err != nil {
				return err
			}
			hashes[name] = nasRotationHash(b)
		}
		previous := ""
		if len(rows) > 0 {
			b, err := nasRotationRead(filepath.Join(r.releaseDir(rows[len(rows)-1].Version), "publication.receipt"), 0)
			if err != nil {
				return err
			}
			previous = nasRotationHash(b)
		}
		receipt := nasRotationRelease{Binding: r.binding, Version: p.Version, Previous: previous, Files: hashes, Approval: in.Approval, Mode: in.Mode, Refresh: in.Mode == "expired_recovery"}
		if err = nasRotationWriteJSON(filepath.Join(stage, "publication.receipt"), receipt); err != nil {
			return err
		}
		if err = nasRotationSync(stage); err != nil {
			return err
		}
		if err = os.Rename(stage, r.releaseDir(p.Version)); err != nil {
			return err
		}
		if err = nasRotationSync(filepath.Dir(r.releaseDir(p.Version))); err != nil {
			return err
		}
		dest, err := r.archiveWork(pending, intent.Action)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(map[string]string{"status": "acknowledged_publication_recovered_install_required", "version": p.Version, "evidenceDirectory": dest})
	case "install":
		if _, err := r.status(rows); err != nil {
			return err
		}
		// install.receipt + immutable file set already committed: repair selector only.
		if len(rows) == 0 {
			return fmt.Errorf("pending install has no publication")
		}
		v := rows[len(rows)-1].Version
		dest := filepath.Join(r.root, "installs", v)
		if receipt, err := nasRotationRead(filepath.Join(dest, "install.receipt"), 0); err == nil {
			if r.recoveryRootState {
				return fmt.Errorf("committed installation has unexpected state owner; preserve evidence")
			}
			var proof nasRotationInstall
			if nasRotationDecode(receipt, &proof) != nil || proof.State != nasRotationHash(state) {
				return fmt.Errorf("pending installed state differs")
			}
			selector := filepath.Join(pending, "recovered-current.json")
			if err = nasRotationWriteJSON(selector, nasRotationCurrent{Version: v, Receipt: nasRotationHash(receipt)}); err != nil {
				return err
			}
			candidate, err := nasRotationRead(selector, 0)
			if err != nil {
				return err
			}
			r.inspectOnly = false
			_, _, err = r.installedRecord(rows, true, candidate)
			r.inspectOnly = true
			if err != nil {
				return err
			}
			if err = os.Rename(selector, filepath.Join(r.root, "current.json")); err != nil {
				return err
			}
			if err = nasRotationSync(r.root); err != nil {
				return err
			}

		} else if os.IsNotExist(err) {
			before, err := nasRotationRead(filepath.Join(pending, "runtime/previous-state.json"), 0)
			if err != nil || nasRotationHash(before) != intent.Before {
				return fmt.Errorf("pending install requires original before-state snapshot")
			}
			r.recoveryPrevious = before
			if _, err = r.archiveWork(pending, "install"); err != nil {
				return err
			}
			if err = r.install(); err != nil {
				return err
			}
			return json.NewEncoder(w).Encode(map[string]string{"status": "install_replayed_through_production_loader", "version": v})
		} else {
			return err
		}
		evidence, err := r.archiveWork(pending, "install")
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(map[string]string{"status": "installation_selector_recovered", "version": v, "evidenceDirectory": evidence})
	default:
		return fmt.Errorf("pending action requires manual investigation; no automatic repair")
	}
}
