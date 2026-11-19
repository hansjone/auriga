package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/hansjone/auriga/infra/agent/installation"
)

const sharedInstallationGeneration = "v-00000000000000000001"
const sharedTeamOnlyAdapter = "{}"

type sharedInstallationReceipt struct {
	Version          int    `json:"version"`
	Base             string `json:"base"`
	Tenant           string `json:"tenant"`
	Node             string `json:"node"`
	CA               string `json:"ca"`
	Adapter          string `json:"adapter"`
	Profile          string `json:"profile"`
	DedicatedProfile string `json:"dedicatedProfile,omitempty"`
	InitialRuntime   string `json:"initialRuntime"`
	Model            string `json:"model"`
	Files            string `json:"files"`
	TeamOnly         bool   `json:"teamOnly,omitempty"`
}

type sharedInstallationEvent struct {
	Version    int    `json:"version"`
	Sequence   uint64 `json:"sequence"`
	Previous   string `json:"previous"`
	Generation string `json:"generation"`
	Action     string `json:"action"`
}

type sharedInstallationRecord struct {
	Name string
	Raw  []byte
}

func sharedInstallationHistory(generation string, records []sharedInstallationRecord) (state, previous string, err error) {
	state, previous = "staged", generation
	for i, record := range records {
		var event sharedInstallationEvent
		if sharedDecodeJSON(record.Raw, &event) != nil || event.Version != 1 || event.Sequence != uint64(i+2) || record.Name != fmt.Sprintf("e-%020d.json", event.Sequence) || event.Previous != previous || event.Generation != generation {
			return "", "", fmt.Errorf("shared receipt event chain changed or rolled back")
		}
		state, err = sharedInstallationNextState(state, event.Action)
		if err != nil {
			return "", "", err
		}
		previous = sharedSHA(record.Raw)
	}
	return state, previous, nil
}

func sharedSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func sharedSourceLeaves() []string {
	leaves := append([]string{}, sharedMaterialLeaves...)
	leaves = append(leaves, "platform/uac-adapter.json", "public/enterprise-policy.json", "public/profile.yml", "public/initial-runtime.json", "public/host.json")
	sort.Strings(leaves)
	return leaves
}

func sharedInstallationLeaves(dedicated bool) []string {
	leaves := sharedSourceLeaves()
	if dedicated {
		leaves = append(leaves, "public/dedicated-profile.yml")
		sort.Strings(leaves)
	}
	return leaves
}

func sharedDedicatedProfile(files map[string][]byte, approved string) ([]byte, error) {
	raw, present := files["public/dedicated-profile.yml"]
	if approved == "" {
		if present {
			return nil, fmt.Errorf("dedicated profile lacks installation approval")
		}
		return nil, nil
	}
	if !present || approved != sharedSHA(raw) {
		return nil, fmt.Errorf("dedicated profile differs from installation approval")
	}
	if err := installation.ValidateDedicatedProfile(raw); err != nil {
		return nil, fmt.Errorf("approved dedicated profile violates production policy: %w", err)
	}
	return raw, nil
}

// The committed source is not a runtime ledger: stopping a member or disabling
// ingress must never reset runtime sequence, quota allocation or pool identity.
// As with the existing materials upgrade, committed rollback is refused.
func sharedInstallationNextState(state, action string) (string, error) {
	switch {
	case state == "staged" && action == "commit":
		return "committed", nil
	case state == "staged" && action == "rollback":
		return "rolled-back", nil
	case state == "rolled-back" && action == "resume":
		return "staged", nil
	default:
		return "", fmt.Errorf("shared installation transition or committed rollback refused")
	}
}

func sharedSourceDigest(files map[string][]byte) (string, error) {
	_, dedicated := files["public/dedicated-profile.yml"]
	leaves := sharedInstallationLeaves(dedicated)
	if len(files) != len(leaves) {
		return "", fmt.Errorf("shared source leaf set differs")
	}
	h := sha256.New()
	for _, name := range leaves {
		raw, ok := files[name]
		if !ok || len(raw) == 0 || len(raw) > 4<<20 {
			return "", fmt.Errorf("shared source incomplete")
		}
		fmt.Fprintf(h, "%s:%s\n", name, sharedSHA(raw))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
