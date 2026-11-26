package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Login preference never grants permission to erase a company identity source.
// Existing fixed NAS adapter bytes are preserved exactly, under their original
// protected metadata. Explicit no-adapter first installation cannot shadow UAC
// properties/members either; the operator must approve the real source instead.
func sharedPreserveUACSource(base string, approved []byte, teamOnly bool) error {
	var adapter map[string]json.RawMessage
	if sharedUniqueJSON(approved) != nil || json.Unmarshal(approved, &adapter) != nil || adapter == nil {
		return fmt.Errorf("approved company adapter must be a strict JSON object")
	}
	if _, err := os.Lstat(filepath.Join(base, "uac/adapter.json")); err == nil {
		current, err := sharedBaseLeaf(base, "uac/adapter.json")
		if err != nil {
			return err
		}
		defer clear(current)
		if teamOnly || !bytes.Equal(current, approved) {
			return fmt.Errorf("shared installation must preserve the existing approved company adapter")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if teamOnly || len(adapter) == 0 {
		for _, leaf := range []string{"uac/uac.properties", "uac/members.json"} {
			if _, err := os.Lstat(filepath.Join(base, leaf)); err == nil {
				return fmt.Errorf("no-adapter first installation cannot override an existing UAC identity source")
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func sharedFixedAdapterSHA(base string) (string, error) {
	if _, err := os.Lstat(filepath.Join(base, "uac/adapter.json")); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	raw, err := sharedBaseLeaf(base, "uac/adapter.json")
	if err != nil {
		return "", err
	}
	defer clear(raw)
	return sharedSHA(raw), nil
}

func verifySharedAdapterBinding(base string, receipt *sharedInstallationReceipt, raw []byte) error {
	rotation := receipt.AdapterRotation
	if rotation == nil {
		return sharedPreserveUACSource(base, raw, receipt.TeamOnly)
	}
	if receipt.TeamOnly || !sharedCanonicalSHA(rotation.PreviousAdapter) || rotation.PreviousAdapter == receipt.Adapter ||
		!sharedCanonicalSHA(rotation.PreviousEvent) || (rotation.BaseAdapter != "" && !sharedCanonicalSHA(rotation.BaseAdapter)) {
		return fmt.Errorf("shared adapter rotation receipt refused")
	}
	fixed, err := sharedFixedAdapterSHA(base)
	if err != nil {
		return err
	}
	if fixed != rotation.BaseAdapter {
		return fmt.Errorf("original shared base adapter differs from rotation binding")
	}
	return validateSharedRotatedAdapter(raw)
}

func validateSharedRotatedAdapter(raw []byte) error {
	var doc map[string]json.RawMessage
	if sharedUniqueJSON(raw) != nil || json.Unmarshal(raw, &doc) != nil || len(doc) == 0 {
		return fmt.Errorf("adapter rotation requires a nonempty, strict company adapter JSON object")
	}
	if value, exists := doc["proofSubjectPolicy"]; exists {
		var policy string
		if json.Unmarshal(value, &policy) != nil || (policy != "echo" && policy != "either") {
			return fmt.Errorf("approved adapter has an invalid proofSubjectPolicy")
		}
	}
	return nil
}

func stageSharedAdapterRotation(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, state, rotationKind string, eventNames []string, previousEvent, adapter, previousSHA, newSHA string) error {
	if receipt.TeamOnly {
		return fmt.Errorf("set-adapter requires an existing company adapter")
	}
	if (state != "committed" || rotationKind != "") && (rotationKind != sharedRotationKindAdapter || (state != "staged" && state != "committed")) {
		return fmt.Errorf("set-adapter requires a committed installation or the same staged adapter rotation")
	}
	raw, err := sharedApprovedFile(adapter, newSHA, true)
	if err != nil {
		return err
	}
	defer clear(raw)
	if err := validateSharedRotatedAdapter(raw); err != nil {
		return err
	}
	if receipt.Adapter == newSHA && receipt.AdapterRotation != nil && receipt.AdapterRotation.PreviousAdapter == previousSHA {
		return nil
	}
	if state != "committed" || rotationKind != "" || receipt.Adapter != previousSHA {
		return fmt.Errorf("current shared adapter differs from approval or another adapter rotation is staged")
	}
	var oldAdapter map[string]json.RawMessage
	if json.Unmarshal(files["platform/uac-adapter.json"], &oldAdapter) != nil || len(oldAdapter) == 0 {
		return fmt.Errorf("set-adapter requires an existing nonempty company adapter")
	}
	policy, err := sharedEnterprisePolicy(raw)
	if err != nil {
		return err
	}
	fixed, err := sharedFixedAdapterSHA(base)
	if err != nil {
		return err
	}
	if receipt.AdapterRotation != nil && fixed != receipt.AdapterRotation.BaseAdapter ||
		receipt.AdapterRotation == nil && fixed != "" && fixed != receipt.Adapter {
		return fmt.Errorf("original shared base adapter changed during rotation")
	}
	return replaceSharedRotation(base, out, generation, baseHash, receipt, files, eventNames, sharedRotationKindAdapter, func(stage string, stagedFiles map[string][]byte, next *sharedInstallationReceipt) error {
		for name, body := range map[string][]byte{"platform/uac-adapter.json": raw, "public/enterprise-policy.json": policy} {
			path := filepath.Join(stage, filepath.FromSlash(name))
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := materialsCreate(path, body); err != nil {
				return err
			}
			stagedFiles[name] = body
		}
		next.Adapter = newSHA
		next.AdapterRotation = &sharedAdapterRotation{BaseAdapter: fixed, PreviousAdapter: previousSHA, PreviousEvent: previousEvent}
		return renderSharedRotationHost(stage, stagedFiles, next.Model)
	})
}

// Verify the archived approval chain and preserve every leaf outside the
// adapter and its public projections before commit or staged rollback.
func sharedAdapterPredecessor(base, out, baseHash string, next *sharedInstallationReceipt, nextFiles map[string][]byte) ([]string, error) {
	rotation := next.AdapterRotation
	if rotation == nil {
		return nil, fmt.Errorf("missing shared adapter rotation approval")
	}
	archive := filepath.Join(out, sharedRotationDir)
	if err := materialsPrivateDirectory(archive); err != nil {
		return nil, err
	}
	generation := filepath.Join(archive, "generation")
	old, oldFiles, err := readSharedInstallationPolicy(base, generation, baseHash, false)
	if err != nil {
		return nil, err
	}
	defer sharedClear(oldFiles)
	copyReceipt := *next
	copyReceipt.Adapter, copyReceipt.AdapterRotation, copyReceipt.Files = old.Adapter, old.AdapterRotation, old.Files
	if old.Adapter != rotation.PreviousAdapter || copyReceipt != *old {
		return nil, fmt.Errorf("shared adapter rotation changed installation identity or approval")
	}
	for name, raw := range oldFiles {
		switch name {
		case "platform/uac-adapter.json", "public/enterprise-policy.json", "public/host.json":
		default:
			if !bytes.Equal(raw, nextFiles[name]) {
				return nil, fmt.Errorf("shared adapter rotation changed unrelated source material")
			}
		}
	}
	names, err := readSharedRotationEvents(archive)
	if err != nil {
		return nil, err
	}
	raw, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
	if err != nil {
		return nil, err
	}
	receiptSHA := sharedSHA(raw)
	state, last, err := readSharedInstallationHistory(archive, receiptSHA, names)
	if err != nil || state != "committed" || last != rotation.PreviousEvent {
		return nil, fmt.Errorf("previous shared adapter approval event chain changed")
	}
	binding, err := sharedReplayBinding(old)
	if err != nil {
		return nil, err
	}
	return []string{binding, receiptSHA}, nil
}

func finishSharedRotation(base, out, baseHash, kind string, receipt *sharedInstallationReceipt, files map[string][]byte, stdout io.Writer) error {
	if kind == sharedRotationKindAdapter {
		previous, err := sharedAdapterPredecessor(base, out, baseHash, receipt, files)
		if err != nil {
			return err
		}
		if err := rebindSharedReplayInstallation(base, receipt, files, previous, stdout); err != nil {
			return fmt.Errorf("shared adapter rotation replay binding pending; retry commit: %w", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(out, sharedRotationDir)); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}
