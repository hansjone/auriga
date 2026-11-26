package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/hansjone/auriga/infra/agent/installation"
)

const sharedInstallationGeneration = "v-00000000000000000001"
const sharedRotationDir = ".rotation"
const sharedRotationKindModel = "model"
const sharedRotationKindProfile = "profile"
const sharedRotationKindAdapter = "adapter"
const sharedTeamOnlyAdapter = "{}"

func sharedApprovedModelID(model string) error {
	if model == "" || len(model) > 128 || strings.TrimSpace(model) != model || strings.ContainsAny(model, "\r\n") {
		return fmt.Errorf("approved public model ID required")
	}
	return nil
}

type sharedRotationMeta struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
}

type sharedInstallationReceipt struct {
	Version          int                    `json:"version"`
	Base             string                 `json:"base"`
	Tenant           string                 `json:"tenant"`
	Node             string                 `json:"node"`
	CA               string                 `json:"ca"`
	Adapter          string                 `json:"adapter"`
	AdapterRotation  *sharedAdapterRotation `json:"adapterRotation,omitempty"`
	Profile          string                 `json:"profile"`
	DedicatedProfile string                 `json:"dedicatedProfile,omitempty"`
	InitialRuntime   string                 `json:"initialRuntime"`
	Model            string                 `json:"model"`
	Files            string                 `json:"files"`
	TeamOnly         bool                   `json:"teamOnly,omitempty"`
	StorageBackend   string                 `json:"storageBackend,omitempty"`
	QuotaMount       string                 `json:"quotaMount,omitempty"`
	QuotaBinary      string                 `json:"quotaBinary,omitempty"`
	ProjectFirst     uint32                 `json:"projectFirst,omitempty"`
	ProjectLast      uint32                 `json:"projectLast,omitempty"`
}

type sharedAdapterRotation struct {
	BaseAdapter     string `json:"baseAdapter,omitempty"`
	PreviousAdapter string `json:"previousAdapter"`
	PreviousEvent   string `json:"previousEvent"`
}

const (
	sharedStorageDefaultProjectFirst uint32 = 10000
	sharedStorageDefaultProjectLast  uint32 = 199999
	sharedStorageDefaultXFSBinary           = "/usr/sbin/xfs_quota"
)

type sharedStorageSpec struct {
	Backend      string
	QuotaMount   string
	QuotaBinary  string
	ProjectFirst uint32
	ProjectLast  uint32
}

func sharedCanonicalPOSIXPath(raw string) bool {
	if raw == "" || raw[0] != '/' {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		ok := c == '/' || c == '-' || c == '_' || c == '.' ||
			(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !ok {
			return false
		}
	}
	return path.Clean(raw) == raw
}

func normalizeSharedStorageSpec(backend, quotaMount, quotaBinary string, projectFirst, projectLast uint32) (sharedStorageSpec, error) {
	if (projectFirst == 0) != (projectLast == 0) {
		return sharedStorageSpec{}, fmt.Errorf("shared storage project-first and project-last must be supplied together")
	}
	if projectFirst != 0 && projectLast < projectFirst {
		return sharedStorageSpec{}, fmt.Errorf("shared storage project-last must be >= project-first")
	}
	switch backend {
	case "", "ext4-image":
		if quotaMount != "" || quotaBinary != "" {
			return sharedStorageSpec{}, fmt.Errorf("quota-mount and quota-binary are only valid with xfs backend")
		}
		return sharedStorageSpec{
			Backend:      "",
			ProjectFirst: projectFirst,
			ProjectLast:  projectLast,
		}, nil
	case "xfs":
		if !sharedCanonicalPOSIXPath(quotaMount) || quotaMount == "/" {
			return sharedStorageSpec{}, fmt.Errorf("xfs backend requires a canonical absolute quota-mount path")
		}
		if quotaBinary == "" {
			quotaBinary = sharedStorageDefaultXFSBinary
		}
		if !sharedCanonicalPOSIXPath(quotaBinary) || quotaBinary == "/" {
			return sharedStorageSpec{}, fmt.Errorf("xfs backend requires a canonical absolute quota-binary path")
		}
		return sharedStorageSpec{
			Backend:      "xfs",
			QuotaMount:   quotaMount,
			QuotaBinary:  quotaBinary,
			ProjectFirst: projectFirst,
			ProjectLast:  projectLast,
		}, nil
	default:
		return sharedStorageSpec{}, fmt.Errorf("unsupported shared storage backend %q (expected ext4-image or xfs)", backend)
	}
}

func sharedStorageSpecFromReceipt(receipt *sharedInstallationReceipt) (sharedStorageSpec, error) {
	if receipt == nil {
		return sharedStorageSpec{}, fmt.Errorf("shared installation receipt required")
	}
	spec, err := normalizeSharedStorageSpec(receipt.StorageBackend, receipt.QuotaMount, receipt.QuotaBinary, receipt.ProjectFirst, receipt.ProjectLast)
	if err != nil {
		return sharedStorageSpec{}, err
	}
	if receipt.StorageBackend != spec.Backend || receipt.QuotaMount != spec.QuotaMount || receipt.QuotaBinary != spec.QuotaBinary {
		return sharedStorageSpec{}, fmt.Errorf("shared installation receipt storage config is not canonical")
	}
	return spec, nil
}

func (s sharedStorageSpec) effectiveProjectRange() (uint32, uint32) {
	if s.ProjectFirst == 0 {
		return sharedStorageDefaultProjectFirst, sharedStorageDefaultProjectLast
	}
	return s.ProjectFirst, s.ProjectLast
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
