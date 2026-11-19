package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestSharedInstallationReceiptHistoryRejectsBrokenAndCommittedRollback(t *testing.T) {
	generation := sharedSHA([]byte("synthetic-receipt"))
	appendEvent := func(records []sharedInstallationRecord, action string) []sharedInstallationRecord {
		previous := generation
		if len(records) > 0 {
			previous = sharedSHA(records[len(records)-1].Raw)
		}
		sequence := uint64(len(records) + 2)
		raw, err := json.Marshal(sharedInstallationEvent{Version: 1, Sequence: sequence, Previous: previous, Generation: generation, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return append(records, sharedInstallationRecord{Name: fmt.Sprintf("e-%020d.json", sequence), Raw: raw})
	}
	if state, previous, err := sharedInstallationHistory(generation, nil); err != nil || state != "staged" || previous != generation {
		t.Fatal("fresh staged history changed")
	}
	rolled := appendEvent(nil, "rollback")
	resumed := appendEvent(append([]sharedInstallationRecord{}, rolled...), "resume")
	committed := appendEvent(append([]sharedInstallationRecord{}, resumed...), "commit")
	if state, _, err := sharedInstallationHistory(generation, committed); err != nil || state != "committed" {
		t.Fatal("valid chained recovery rejected", err)
	}
	for name, records := range map[string][]sharedInstallationRecord{
		"missing-first":        committed[1:],
		"reordered":            {committed[1], committed[0], committed[2]},
		"duplicate":            {committed[0], committed[0]},
		"post-commit-rollback": appendEvent(append([]sharedInstallationRecord{}, committed...), "rollback"),
		"wrong-generation":     {{Name: committed[0].Name, Raw: bytes.ReplaceAll(committed[0].Raw, []byte(generation), []byte(sharedSHA([]byte("other-installation"))))}},
		"changed-action":       {{Name: committed[0].Name, Raw: bytes.Replace(committed[0].Raw, []byte("rollback"), []byte("commit"), 1)}, committed[1]},
		"duplicate-json-field": {{Name: committed[0].Name, Raw: bytes.Replace(committed[0].Raw, []byte(`"version":`), []byte(`"version":1,"version":`), 1)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sharedInstallationHistory(generation, records); err == nil {
				t.Fatal("broken installation history accepted")
			}
		})
	}
}

func TestSharedInstallationClosedSourceDigestIncludesAllPrivateAndPublicLeaves(t *testing.T) {
	files := map[string][]byte{}
	for _, name := range sharedSourceLeaves() {
		files[name] = []byte("synthetic-" + name)
	}
	original, err := sharedSourceDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		old := files[name]
		files[name] = append(append([]byte{}, old...), byte('x'))
		changed, err := sharedSourceDigest(files)
		if err != nil || changed == original {
			t.Fatal("source digest omitted leaf", name)
		}
		delete(files, name)
		if _, err := sharedSourceDigest(files); err == nil {
			t.Fatal("missing source leaf accepted", name)
		}
		files[name] = old
	}
	files["foreign.secret"] = []byte("synthetic")
	if _, err := sharedSourceDigest(files); err == nil {
		t.Fatal("unknown secret source accepted")
	}
}
