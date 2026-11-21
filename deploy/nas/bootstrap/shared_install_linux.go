package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/installation"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"golang.org/x/sys/unix"
)

func nasSharedRuntime(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("shared-runtime", flag.ContinueOnError)
	base := f.String("base", "", "original sealed NAS materials")
	out := f.String("out", "", "fixed config/shared-source sibling")
	action := f.String("action", "status", "stage, set-model, set-profile, commit, rollback, check, path, project or status")
	expected := f.String("expected-base-sha256", "", "independently approved original receipt SHA256")
	adapter := f.String("adapter-file", "", "approved private UAC adapter containing sharedOutbound")
	adapterSHA := f.String("adapter-sha256", "", "independently approved private adapter SHA256")
	profile := f.String("profile-file", "", "approved complete shared runtime profile")
	profileSHA := f.String("profile-sha256", "", "independently approved profile SHA256")
	dedicatedProfile := f.String("dedicated-profile-file", "", "optional independently approved complete dedicated profile")
	dedicatedSHA := f.String("dedicated-profile-sha256", "", "independently approved dedicated profile SHA256")
	model := f.String("model", "", "approved public model ID")
	teamOnly := f.Bool("team-only", false, "explicit first installation without an existing company adapter")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || os.Geteuid() != 0 || !sharedCanonicalPath(*base) || *out != filepath.Join(filepath.Dir(*base), "shared-source") {
		return fmt.Errorf("shared installation requires root and canonical fixed sibling paths")
	}
	mutating := *action == "stage" || *action == "set-model" || *action == "set-profile" || *action == "commit" || *action == "rollback"
	if !mutating && *action != "status" && *action != "check" && *action != "path" && *action != "project" {
		return fmt.Errorf("unsupported shared installation operation")
	}
	if *teamOnly && (*adapter != "" || *adapterSHA != "") {
		return fmt.Errorf("team-only and approved adapter inputs are mutually exclusive")
	}
	if (*dedicatedProfile == "") != (*dedicatedSHA == "") {
		return fmt.Errorf("dedicated profile file and approval SHA256 must be supplied together")
	}
	switch *action {
	case "set-model":
		if *adapter != "" || *adapterSHA != "" || *profile != "" || *profileSHA != "" || *dedicatedProfile != "" || *dedicatedSHA != "" || *teamOnly {
			return fmt.Errorf("set-model only accepts model and original base approval")
		}
		if err := sharedApprovedModelID(*model); err != nil {
			return err
		}
	case "set-profile":
		if *adapter != "" || *adapterSHA != "" || *model != "" || *dedicatedProfile != "" || *dedicatedSHA != "" || *teamOnly {
			return fmt.Errorf("set-profile only accepts profile file, profile approval and original base approval")
		}
		if *profile == "" || *profileSHA == "" {
			return fmt.Errorf("set-profile requires profile file and independent SHA256")
		}
	case "stage":
	default:
		if *adapter != "" || *adapterSHA != "" || *profile != "" || *profileSHA != "" || *dedicatedProfile != "" || *dedicatedSHA != "" || *model != "" || *teamOnly {
			return fmt.Errorf("staging inputs are only accepted by stage")
		}
	}
	baseHash, err := verifyNASCollabBase(*base)
	if err != nil {
		return err
	}
	if mutating && *expected != baseHash {
		return fmt.Errorf("original shared installation receipt differs from approval")
	}
	if err := checkNASCollabParent(filepath.Dir(*out)); err != nil {
		return err
	}
	if _, err := os.Lstat(*out); os.IsNotExist(err) {
		if *action != "stage" {
			if *action == "status" {
				_, err = fmt.Fprintln(stdout, "state=absent")
				return err
			}
			return fmt.Errorf("shared installation absent")
		}
		if err := os.Mkdir(*out, 0700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := materialsPrivateDirectory(*out); err != nil {
		return err
	}
	unlock, err := sharedInstallationLock(*out)
	if err != nil {
		return err
	}
	defer unlock()
	generation := filepath.Join(*out, sharedInstallationGeneration)
	entries, err := os.ReadDir(*out)
	if err != nil {
		return err
	}
	var eventNames []string
	hasGeneration := false
	hasRotation := false
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" {
			continue
		}
		if name == sharedInstallationGeneration {
			hasGeneration = true
			continue
		}
		if name == sharedRotationDir {
			hasRotation = true
			continue
		}
		if !regexp.MustCompile(`^e-[0-9]{20}\.json$`).MatchString(name) {
			return fmt.Errorf("unknown or interrupted shared installation; preserve source for inspection")
		}
		eventNames = append(eventNames, name)
	}
	if !hasGeneration {
		if hasRotation || len(eventNames) != 0 || *action != "stage" {
			return fmt.Errorf("shared generation absent; activation refused")
		}
		if err := stageSharedInstallation(*base, *out, baseHash, *adapter, *adapterSHA, *profile, *profileSHA, *dedicatedProfile, *dedicatedSHA, *model, *teamOnly); err != nil {
			return err
		}
	}
	rotationKind := ""
	if hasRotation {
		kind, err := readSharedRotationKind(*out)
		if err != nil {
			return err
		}
		rotationKind = kind
	}
	receipt, files, err := readSharedInstallation(*base, generation, baseHash)
	if err != nil {
		return err
	}
	defer sharedClear(files)
	if *action == "stage" {
		approvedAdapter := *adapterSHA
		if *teamOnly {
			approvedAdapter = sharedSHA([]byte(sharedTeamOnlyAdapter))
		}
		if receipt.Adapter != approvedAdapter || receipt.TeamOnly != *teamOnly || receipt.Profile != *profileSHA || receipt.DedicatedProfile != *dedicatedSHA || receipt.Model != *model {
			return fmt.Errorf("shared staging approval differs; implicit identity or policy rotation refused")
		}
	}
	receiptRaw, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
	if err != nil {
		return err
	}
	generationHash := sharedSHA(receiptRaw)
	var records []sharedInstallationRecord
	for _, name := range eventNames {
		raw, err := runtimeReadLeaf(filepath.Join(*out, name), 0600, 65536)
		if err != nil {
			return err
		}
		records = append(records, sharedInstallationRecord{Name: name, Raw: raw})
	}
	state, previous, err := sharedInstallationHistory(generationHash, records)
	if err != nil {
		return err
	}
	if *action == "status" {
		_, err = fmt.Fprintf(stdout, "state=%s version=1 records=%d\n", state, len(eventNames)+1)
		return err
	}
	if *action == "set-model" {
		return stageSharedModelRotation(*base, *out, generation, baseHash, receipt, files, state, rotationKind, eventNames, *model)
	}
	if *action == "set-profile" {
		return stageSharedProfileRotation(*base, *out, generation, baseHash, receipt, files, state, rotationKind, eventNames, *profile, *profileSHA)
	}
	if *action == "check" || *action == "path" || *action == "project" {
		if state != "committed" {
			return fmt.Errorf("shared installation is not committed; ingress remains disabled")
		}
		if *action == "project" {
			projected, err := projectSharedInstallation(*base, receipt, files)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(stdout, projected)
			return err
		}
		if err := checkSharedInstallationEnvironment(*base, receipt); err != nil {
			return err
		}
		if *action == "path" {
			_, err = fmt.Fprint(stdout, generation)
		}
		return err
	}
	if *action == "rollback" && hasRotation {
		if state != "staged" {
			return fmt.Errorf("shared rotation rollback requires a staged set-model or set-profile generation")
		}
		return restoreSharedRotation(*out, generation)
	}
	eventAction := *action
	if eventAction == "stage" {
		if state == "staged" || state == "committed" {
			return nil
		}
		eventAction = "resume"
	}
	if eventAction == "commit" && state == "committed" {
		if hasRotation {
			if err := os.RemoveAll(filepath.Join(*out, sharedRotationDir)); err != nil {
				return err
			}
			return runtimeSyncDir(*out)
		}
		return nil
	}
	if eventAction == "rollback" && state == "rolled-back" {
		return nil
	}
	if _, err := sharedInstallationNextState(state, eventAction); err != nil {
		return err
	}
	if eventAction == "commit" {
		if err := checkSharedInstallationEnvironment(*base, receipt); err != nil {
			return err
		}
	}
	if fresh, err := verifyNASCollabBase(*base); err != nil || fresh != baseHash {
		return fmt.Errorf("sealed base changed before shared activation event")
	}
	event := sharedInstallationEvent{Version: 1, Sequence: uint64(len(eventNames) + 2), Previous: previous, Generation: generationHash, Action: eventAction}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := materialsAppend(*out, fmt.Sprintf("e-%020d.json", event.Sequence), raw); err != nil {
		return err
	}
	if eventAction == "commit" && hasRotation {
		if err := os.RemoveAll(filepath.Join(*out, sharedRotationDir)); err != nil {
			return err
		}
		return runtimeSyncDir(*out)
	}
	return nil
}

func sharedCanonicalPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\\\r\n\x00")
}
func sharedCanonicalSHA(value string) bool {
	return regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value)
}
func sharedClear(files map[string][]byte) {
	for _, raw := range files {
		clear(raw)
	}
}

func sharedInstallationLock(out string) (func(), error) {
	fd, err := unix.Open(filepath.Join(out, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink != 1 || stat.Mode&07777 != 0600 || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("shared installation lock metadata refused")
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
}

// Read only fixed public leaves from the original UID10001 sealed base, using
// the same pinned directory and exact metadata rules as its existing guard.
func sharedBasePublic(base, name string) ([]byte, error) {
	if name == "uac/adapter.json" {
		return nil, fmt.Errorf("private adapter is not a public base leaf")
	}
	return sharedBaseLeaf(base, name)
}

func sharedBaseLeaf(base, name string) ([]byte, error) {
	if name != "access.json" && name != "identity-keys.json" && name != "pki/ca.crt" && name != "templates/real-dsh.pb" && name != "bifrost-runtime.json" {
		if name != "uac/adapter.json" {
			return nil, fmt.Errorf("unsupported fixed shared base leaf")
		}
	}
	mode := uint32(0644)
	if name == "uac/adapter.json" {
		mode = 0640
	}
	current, err := openNASSourceRoot(base)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := openNASDirectory(int(current.Fd()), part, true)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	defer current.Close()
	leaf := parts[len(parts)-1]
	var before, opened, after unix.Stat_t
	valid := func(s unix.Stat_t) bool {
		return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&07777 == mode && s.Uid == 10001 && s.Gid == 10001 && s.Nlink == 1 && s.Size > 0 && s.Size <= 4<<20
	}
	if unix.Fstatat(int(current.Fd()), leaf, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || !valid(before) {
		return nil, fmt.Errorf("shared public base metadata refused")
	}
	fd, err := unix.Openat(int(current.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), leaf)
	defer file.Close()
	if unix.Fstat(fd, &opened) != nil || !sameNASMetadata(before, opened) {
		return nil, fmt.Errorf("shared public base changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if unix.Fstatat(int(current.Fd()), leaf, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !valid(after) || !sameNASMetadata(opened, after) || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || len(raw) > 4<<20 {
		return nil, fmt.Errorf("shared public base changed while reading")
	}
	return raw, nil
}

func sharedLegacyPublic(base string) (ed25519.PublicKey, error) {
	raw, err := sharedBasePublic(base, "identity-keys.json")
	if err != nil {
		return nil, err
	}
	var keys struct {
		Keys []sharedJWK `json:"keys"`
	}
	if sharedUniqueJSON(raw) != nil || json.Unmarshal(raw, &keys) != nil || len(keys.Keys) != 1 {
		return nil, fmt.Errorf("single existing owner public key required")
	}
	key := keys.Keys[0]
	public, err := base64.RawURLEncoding.Strict().DecodeString(key.X)
	if err != nil || len(public) != ed25519.PublicKeySize || key.Kty != "OKP" || key.Crv != "Ed25519" || key.D != "" {
		return nil, fmt.Errorf("existing owner public key refused")
	}
	return ed25519.PublicKey(public), nil
}

func sharedNodeIdentity(base string) (node, caFingerprint string, err error) {
	caPEM, err := sharedBasePublic(base, "pki/ca.crt")
	if err != nil {
		return "", "", err
	}
	block, rest := pem.Decode(caPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return "", "", fmt.Errorf("single sealed Node CA required")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !ca.IsCA {
		return "", "", fmt.Errorf("sealed Node CA refused")
	}
	dataRoot := filepath.Dir(filepath.Dir(base))
	path := filepath.Join(dataRoot, "data/agent/agent/node.crt")
	if err := checkNASCollabParent(filepath.Dir(path)); err != nil {
		return "", "", err
	}
	raw, err := runtimeReadLeaf(path, 0600, 65536)
	if err != nil {
		return "", "", fmt.Errorf("existing enrolled Node public certificate required: %w", err)
	}
	certBlock, remainder := pem.Decode(raw)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return "", "", fmt.Errorf("Node public certificate refused")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("Node public certificate refused")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	intermediates := x509.NewCertPool()
	for len(bytes.TrimSpace(remainder)) > 0 {
		next, remaining := pem.Decode(remainder)
		if next == nil || next.Type != "CERTIFICATE" {
			return "", "", fmt.Errorf("Node certificate chain refused")
		}
		parsed, e := x509.ParseCertificate(next.Bytes)
		if e != nil {
			return "", "", fmt.Errorf("Node certificate chain refused")
		}
		intermediates.AddCert(parsed)
		remainder = remaining
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return "", "", fmt.Errorf("current Node certificate does not verify against sealed CA")
	}
	for _, uri := range cert.URIs {
		if strings.HasPrefix(uri.String(), "auriga://node/") {
			if node != "" {
				return "", "", fmt.Errorf("ambiguous Node identity")
			}
			node = strings.TrimPrefix(uri.String(), "auriga://node/")
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(node) {
		return "", "", fmt.Errorf("canonical enrolled Node UUID required")
	}
	return node, sharedSHA(ca.RawSubjectPublicKeyInfo), nil
}

func sharedApprovedFile(path, expected string, private bool) ([]byte, error) {
	if !sharedCanonicalPath(path) || !sharedCanonicalSHA(expected) {
		return nil, fmt.Errorf("canonical approved input path and independent SHA256 required")
	}
	if err := checkNASCollabParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	mode := os.FileMode(0644)
	if private {
		mode = 0600
	}
	raw, err := runtimeReadLeaf(path, mode, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("approved shared input metadata refused: %w", err)
	}
	if sharedSHA(raw) != expected {
		clear(raw)
		return nil, fmt.Errorf("shared input differs from independent approval")
	}
	return raw, nil
}

func sharedCommittedRuntime(base string) ([]byte, error) {
	return sharedRuntimeSnapshot(base, false)
}

func sharedRuntimeSnapshot(base string, allowVerifiedPredecessor bool) ([]byte, error) {
	out := filepath.Join(filepath.Dir(base), "runtime-source")
	var resolved bytes.Buffer
	if err := nasRuntimeUpgrade([]string{"-base", base, "-out", out, "-action", "path"}, &resolved); err != nil {
		return nil, err
	}
	path := resolved.String()
	if path == base {
		raw, err := sharedBasePublic(base, "templates/real-dsh.pb")
		if err != nil {
			return nil, err
		}
		return sharedRuntimeSpecJSON(raw)
	}
	if !sharedCanonicalPath(path) || filepath.Dir(path) != out {
		return nil, fmt.Errorf("runtime verifier returned unexpected source")
	}
	unlock, err := sharedInstallationLock(out)
	if err != nil {
		return nil, err
	}
	defer unlock()
	activeRaw, err := runtimeReadLeaf(filepath.Join(out, "active.json"), 0600, 65536)
	if err != nil {
		return nil, err
	}
	var active runtimeActivation
	if sharedUniqueJSON(activeRaw) != nil || runtimeDecode(activeRaw, &active) != nil || active.Current != filepath.Base(path) || (!active.Pending && active.Previous != "") || (active.Pending && (!allowVerifiedPredecessor || active.Previous == active.Current)) {
		return nil, fmt.Errorf("runtime must be committed and stable before shared installation")
	}
	baseHash, err := verifyNASCollabBase(base)
	if err != nil {
		return nil, err
	}
	generations, err := readRuntimeGenerations(out, baseHash, filepath.Join(base, "runtime-keys/root.pb"))
	if err != nil {
		return nil, err
	}
	found := false
	for _, generation := range generations {
		if generation.name == active.Current {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("committed runtime source absent")
	}
	if active.Pending {
		// The public upgrader's pending pointer retains exactly its previously
		// committed source. Never project the candidate before original commit.
		if active.Previous == "" {
			raw, err := sharedBasePublic(base, "templates/real-dsh.pb")
			if err != nil {
				return nil, err
			}
			return sharedRuntimeSpecJSON(raw)
		}
		found = false
		for _, generation := range generations {
			if generation.name == active.Previous {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("verified committed runtime predecessor absent")
		}
		path = filepath.Join(out, active.Previous)
	}
	raw, err := runtimeReadLeaf(filepath.Join(path, "templates/real-dsh.pb"), 0644, 4<<20)
	if err != nil {
		return nil, err
	}
	return sharedRuntimeSpecJSON(raw)
}

func readSharedRotationKind(out string) (string, error) {
	raw, err := runtimeReadLeaf(filepath.Join(out, sharedRotationDir, "rotation.json"), 0600, 4096)
	if err != nil {
		return "", fmt.Errorf("shared rotation metadata refused: %w", err)
	}
	var meta sharedRotationMeta
	if sharedUniqueJSON(raw) != nil || runtimeDecode(raw, &meta) != nil || meta.Version != 1 || (meta.Kind != sharedRotationKindModel && meta.Kind != sharedRotationKindProfile) {
		return "", fmt.Errorf("shared rotation metadata refused")
	}
	return meta.Kind, nil
}

func stageSharedModelRotation(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, state, rotationKind string, eventNames []string, model string) error {
	if err := sharedApprovedModelID(model); err != nil {
		return err
	}
	switch state {
	case "committed":
		if receipt.Model == model {
			return nil
		}
		if rotationKind != "" {
			return fmt.Errorf("interrupted shared rotation; preserve source for inspection")
		}
		return replaceSharedModel(base, out, generation, baseHash, receipt, files, eventNames, model)
	case "staged":
		if rotationKind == "" {
			return fmt.Errorf("initial shared staging is not a model rotation; use stage with the approved model")
		}
		if rotationKind != sharedRotationKindModel {
			return fmt.Errorf("shared profile rotation in progress; overlapping set-model refused")
		}
		if receipt.Model == model {
			return nil
		}
		return fmt.Errorf("shared model rotation differs; concurrent or conflicting set-model refused")
	case "rolled-back":
		return fmt.Errorf("rolled-back installation cannot set-model; resume or reinstall explicitly")
	default:
		return fmt.Errorf("shared model rotation refused in state %s", state)
	}
}

func stageSharedProfileRotation(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, state, rotationKind string, eventNames []string, profile, profileSHA string) error {
	switch state {
	case "committed":
		if receipt.Profile == profileSHA {
			approved, err := sharedApprovedFile(profile, profileSHA, false)
			if err != nil {
				return err
			}
			clear(approved)
			return nil
		}
		if rotationKind != "" {
			return fmt.Errorf("interrupted shared rotation; preserve source for inspection")
		}
		return replaceSharedProfile(base, out, generation, baseHash, receipt, files, eventNames, profile, profileSHA)
	case "staged":
		if rotationKind == "" {
			return fmt.Errorf("initial shared staging is not a profile rotation; use stage with the approved profile")
		}
		if rotationKind != sharedRotationKindProfile {
			return fmt.Errorf("shared model rotation in progress; overlapping set-profile refused")
		}
		if receipt.Profile == profileSHA {
			approved, err := sharedApprovedFile(profile, profileSHA, false)
			if err != nil {
				return err
			}
			clear(approved)
			return nil
		}
		return fmt.Errorf("shared profile rotation differs; concurrent or conflicting set-profile refused")
	case "rolled-back":
		return fmt.Errorf("rolled-back installation cannot set-profile; resume or reinstall explicitly")
	default:
		return fmt.Errorf("shared profile rotation refused in state %s", state)
	}
}

func replaceSharedModel(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, eventNames []string, model string) error {
	return replaceSharedRotation(base, out, generation, baseHash, receipt, files, eventNames, sharedRotationKindModel, func(stage string, stagedFiles map[string][]byte, next *sharedInstallationReceipt) error {
		if err := installation.RenderSharedHostConfig(installation.SharedHostInputs{
			IdentityKeysFile:     filepath.Join(stage, "public/identity.jwks.json"),
			EnterpriseKeysFile:   filepath.Join(stage, "public/enterprise.jwks.json"),
			ModelKeysFile:        filepath.Join(stage, "public/model.jwks.json"),
			MessageKeysFile:      filepath.Join(stage, "public/message.jwks.json"),
			EnterprisePolicyFile: filepath.Join(stage, "public/enterprise-policy.json"),
			Model:                model,
			OutputFile:           filepath.Join(stage, "public/host.json"),
		}); err != nil {
			return err
		}
		host, err := runtimeReadLeaf(filepath.Join(stage, "public/host.json"), 0444, 256<<10)
		if err != nil {
			return err
		}
		stagedFiles["public/host.json"] = host
		next.Model = model
		return nil
	})
}

func replaceSharedProfile(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, eventNames []string, profile, profileSHA string) error {
	profileRaw, err := sharedApprovedFile(profile, profileSHA, false)
	if err != nil {
		return err
	}
	defer clear(profileRaw)
	if err := installation.ValidateSharedProfile(profileRaw); err != nil {
		return fmt.Errorf("approved shared profile violates production policy: %w", err)
	}
	return replaceSharedRotation(base, out, generation, baseHash, receipt, files, eventNames, sharedRotationKindProfile, func(stage string, stagedFiles map[string][]byte, next *sharedInstallationReceipt) error {
		path := filepath.Join(stage, "public/profile.yml")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := materialsCreate(path, profileRaw); err != nil {
			return err
		}
		stagedFiles["public/profile.yml"] = append([]byte{}, profileRaw...)
		next.Profile = profileSHA
		return nil
	})
}

func replaceSharedRotation(base, out, generation, baseHash string, receipt *sharedInstallationReceipt, files map[string][]byte, eventNames []string, kind string, mutate func(stage string, stagedFiles map[string][]byte, next *sharedInstallationReceipt) error) error {
	if err := checkSharedInstallationEnvironment(base, receipt); err != nil {
		return err
	}
	stage := filepath.Join(out, ".pending")
	if err := os.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("shared staging exists; inspect preserved incomplete evidence")
	}
	for _, name := range []string{"platform", "platform/agent-secret", "edge", "public"} {
		if err := os.Mkdir(filepath.Join(stage, name), 0700); err != nil {
			return err
		}
	}
	leaves := sharedInstallationLeaves(receipt.DedicatedProfile != "")
	stagedFiles := map[string][]byte{}
	for _, name := range leaves {
		raw, ok := files[name]
		if !ok {
			return fmt.Errorf("shared source incomplete")
		}
		path := filepath.Join(stage, filepath.FromSlash(name))
		switch {
		case name == "public/host.json" && kind == sharedRotationKindModel:
			// Re-rendered exclusively below; never stage a 0600 placeholder that
			// would block the public renderer or leak into the published tree.
			continue
		case name == "public/host.json":
			if err := materialsCreateMode(path, raw, 0444); err != nil {
				return err
			}
		default:
			if err := materialsCreate(path, raw); err != nil {
				return err
			}
		}
		stagedFiles[name] = raw
	}
	next := *receipt
	if err := mutate(stage, stagedFiles, &next); err != nil {
		return err
	}
	digest, err := sharedSourceDigest(stagedFiles)
	if err != nil {
		return err
	}
	next.Files = digest
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(stage, "installation.receipt")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := materialsCreate(filepath.Join(stage, "installation.receipt"), raw); err != nil {
		return err
	}
	if fresh, err := verifyNASCollabBase(base); err != nil || fresh != baseHash {
		return fmt.Errorf("sealed base changed during shared rotation")
	}
	if err := runtimeSyncTree(stage); err != nil {
		return err
	}
	rotation := filepath.Join(out, sharedRotationDir)
	if err := os.Mkdir(rotation, 0700); err != nil {
		return fmt.Errorf("shared rotation exists; inspect preserved incomplete evidence")
	}
	meta, err := json.Marshal(sharedRotationMeta{Version: 1, Kind: kind})
	if err != nil {
		return err
	}
	if err := materialsCreate(filepath.Join(rotation, "rotation.json"), meta); err != nil {
		return err
	}
	if err := os.Rename(generation, filepath.Join(rotation, "generation")); err != nil {
		return err
	}
	for _, name := range eventNames {
		if err := os.Rename(filepath.Join(out, name), filepath.Join(rotation, name)); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, generation); err != nil {
		return err
	}
	hostPath := filepath.Join(generation, "public/host.json")
	if err := os.Chmod(hostPath, 0444); err != nil {
		return err
	}
	if _, err := runtimeReadLeaf(hostPath, 0444, 256<<10); err != nil {
		return fmt.Errorf("shared host mode lost after rotation publish: %w", err)
	}
	return runtimeSyncDir(out)
}

func restoreSharedRotation(out, generation string) error {
	rotation := filepath.Join(out, sharedRotationDir)
	if err := materialsPrivateDirectory(rotation); err != nil {
		return err
	}
	if _, err := readSharedRotationKind(out); err != nil {
		return err
	}
	previous := filepath.Join(rotation, "generation")
	if err := materialsPrivateDirectory(previous); err != nil {
		return err
	}
	entries, err := os.ReadDir(rotation)
	if err != nil {
		return err
	}
	var eventNames []string
	for _, entry := range entries {
		name := entry.Name()
		if name == "generation" || name == "rotation.json" {
			continue
		}
		if !regexp.MustCompile(`^e-[0-9]{20}\.json$`).MatchString(name) {
			return fmt.Errorf("unknown or interrupted shared rotation; preserve source for inspection")
		}
		eventNames = append(eventNames, name)
	}
	if err := os.RemoveAll(generation); err != nil {
		return err
	}
	if err := os.Rename(previous, generation); err != nil {
		return err
	}
	for _, name := range eventNames {
		if err := os.Rename(filepath.Join(rotation, name), filepath.Join(out, name)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(rotation); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}

func stageSharedInstallation(base, out, baseHash, adapter, adapterSHA, profile, profileSHA, dedicatedProfile, dedicatedSHA, model string, teamOnly bool) error {
	if err := sharedApprovedModelID(model); err != nil {
		return err
	}
	var adapterRaw []byte
	var err error
	if teamOnly {
		adapterRaw = []byte(sharedTeamOnlyAdapter)
		adapterSHA = sharedSHA(adapterRaw)
	} else if adapter == filepath.Join(base, "uac/adapter.json") && sharedCanonicalSHA(adapterSHA) {
		adapterRaw, err = sharedBaseLeaf(base, "uac/adapter.json")
		if err == nil && sharedSHA(adapterRaw) != adapterSHA {
			clear(adapterRaw)
			return fmt.Errorf("private base adapter differs from independent approval")
		}
	} else {
		adapterRaw, err = sharedApprovedFile(adapter, adapterSHA, true)
	}
	if err != nil {
		return err
	}
	defer clear(adapterRaw)
	if err := sharedPreserveUACSource(base, adapterRaw, teamOnly); err != nil {
		return err
	}
	policy, err := sharedEnterprisePolicy(adapterRaw)
	if err != nil {
		return err
	}
	profileRaw, err := sharedApprovedFile(profile, profileSHA, false)
	if err != nil {
		return err
	}
	if err := installation.ValidateSharedProfile(profileRaw); err != nil {
		return fmt.Errorf("approved shared profile violates production policy: %w", err)
	}
	var dedicatedRaw []byte
	if dedicatedProfile != "" {
		dedicatedRaw, err = sharedApprovedFile(dedicatedProfile, dedicatedSHA, false)
		if err != nil {
			return err
		}
		if err := installation.ValidateDedicatedProfile(dedicatedRaw); err != nil {
			return fmt.Errorf("approved dedicated profile violates production policy: %w", err)
		}
	}
	runtimeJSON, err := sharedCommittedRuntime(base)
	if err != nil {
		return err
	}
	node, ca, err := sharedNodeIdentity(base)
	if err != nil {
		return err
	}
	accessRaw, err := sharedBasePublic(base, "access.json")
	if err != nil {
		return err
	}
	access, err := deploymentconfig.Load(accessRaw)
	if err != nil {
		return err
	}
	legacy, err := sharedLegacyPublic(base)
	if err != nil {
		return err
	}
	files, err := generateSharedMaterials()
	if err != nil {
		return err
	}
	defer sharedClear(files)
	if err := verifySharedMaterials(files, legacy); err != nil {
		return err
	}
	files["platform/uac-adapter.json"] = adapterRaw
	files["public/enterprise-policy.json"] = policy
	files["public/profile.yml"] = profileRaw
	if dedicatedRaw != nil {
		files["public/dedicated-profile.yml"] = dedicatedRaw
	}
	files["public/initial-runtime.json"] = runtimeJSON
	stage := filepath.Join(out, ".pending")
	if err := os.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("shared staging exists; inspect preserved incomplete evidence")
	}
	// Preserve interrupted staging for explicit inspection; never rotate keys by
	// silently replacing a failed attempt or by touching the sealed base.
	for _, name := range []string{"platform", "platform/agent-secret", "edge", "public"} {
		if err := os.Mkdir(filepath.Join(stage, name), 0700); err != nil {
			return err
		}
	}
	for name, raw := range files {
		if err := materialsCreate(filepath.Join(stage, filepath.FromSlash(name)), raw); err != nil {
			return err
		}
	}
	if err := installation.RenderSharedHostConfig(installation.SharedHostInputs{IdentityKeysFile: filepath.Join(stage, "public/identity.jwks.json"), EnterpriseKeysFile: filepath.Join(stage, "public/enterprise.jwks.json"), ModelKeysFile: filepath.Join(stage, "public/model.jwks.json"), MessageKeysFile: filepath.Join(stage, "public/message.jwks.json"), EnterprisePolicyFile: filepath.Join(stage, "public/enterprise-policy.json"), Model: model, OutputFile: filepath.Join(stage, "public/host.json")}); err != nil {
		return err
	}
	host, err := runtimeReadLeaf(filepath.Join(stage, "public/host.json"), 0444, 256<<10)
	if err != nil {
		return err
	}
	files["public/host.json"] = host
	digest, err := sharedSourceDigest(files)
	if err != nil {
		return err
	}
	receipt := sharedInstallationReceipt{Version: 1, Base: baseHash, Tenant: access.TenantID(), Node: node, CA: ca, Adapter: adapterSHA, Profile: profileSHA, DedicatedProfile: dedicatedSHA, InitialRuntime: sharedSHA(runtimeJSON), Model: model, Files: digest, TeamOnly: teamOnly}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := materialsCreate(filepath.Join(stage, "installation.receipt"), raw); err != nil {
		return err
	}
	if fresh, err := verifyNASCollabBase(base); err != nil || fresh != baseHash {
		return fmt.Errorf("sealed base changed during shared staging")
	}
	if err := runtimeSyncTree(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, filepath.Join(out, sharedInstallationGeneration)); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}

func readSharedInstallation(base, generation, baseHash string) (*sharedInstallationReceipt, map[string][]byte, error) {
	raw, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
	if err != nil {
		return nil, nil, err
	}
	var receipt sharedInstallationReceipt
	if sharedUniqueJSON(raw) != nil || runtimeDecode(raw, &receipt) != nil || receipt.Version != 1 || receipt.DedicatedProfile != "" && !sharedCanonicalSHA(receipt.DedicatedProfile) {
		return nil, nil, fmt.Errorf("shared installation receipt refused")
	}
	leaves := sharedInstallationLeaves(receipt.DedicatedProfile != "")
	allowed := map[string]bool{".": true, "installation.receipt": true, "platform": true, "platform/agent-secret": true, "edge": true, "public": true}
	for _, name := range leaves {
		allowed[name] = true
	}
	if err := filepath.WalkDir(generation, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(generation, path)
		if err != nil || !allowed[filepath.ToSlash(relative)] {
			return fmt.Errorf("unknown shared source leaf")
		}
		if entry.IsDir() {
			return materialsPrivateDirectory(path)
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	files := map[string][]byte{}
	success := false
	defer func() {
		if !success {
			sharedClear(files)
		}
	}()
	for _, name := range leaves {
		mode := os.FileMode(0600)
		if name == "public/host.json" {
			mode = 0444
		}
		raw, err := runtimeReadLeaf(filepath.Join(generation, filepath.FromSlash(name)), mode, 4<<20)
		if err != nil {
			return nil, nil, err
		}
		files[name] = raw
	}
	digest, err := sharedSourceDigest(files)
	if err != nil || receipt.Version != 1 || receipt.Base != baseHash || receipt.Files != digest || receipt.Adapter != sharedSHA(files["platform/uac-adapter.json"]) || receipt.Profile != sharedSHA(files["public/profile.yml"]) || receipt.InitialRuntime != sharedSHA(files["public/initial-runtime.json"]) {
		return nil, nil, fmt.Errorf("shared source receipt or installation binding changed")
	}
	if _, err := sharedDedicatedProfile(files, receipt.DedicatedProfile); err != nil {
		return nil, nil, err
	}
	if receipt.TeamOnly && !bytes.Equal(files["platform/uac-adapter.json"], []byte(sharedTeamOnlyAdapter)) {
		return nil, nil, fmt.Errorf("team-only source cannot replace an existing identity adapter")
	}
	if err := sharedPreserveUACSource(base, files["platform/uac-adapter.json"], receipt.TeamOnly); err != nil {
		return nil, nil, err
	}
	legacy, err := sharedLegacyPublic(base)
	if err != nil {
		return nil, nil, err
	}
	materials := map[string][]byte{}
	for _, name := range sharedMaterialLeaves {
		materials[name] = files[name]
	}
	if err := verifySharedMaterials(materials, legacy); err != nil {
		return nil, nil, err
	}
	policy, err := sharedEnterprisePolicy(files["platform/uac-adapter.json"])
	if err != nil || !bytes.Equal(policy, files["public/enterprise-policy.json"]) {
		return nil, nil, fmt.Errorf("shared public policy differs from approved private adapter")
	}
	if err := installation.ValidateSharedProfile(files["public/profile.yml"]); err != nil {
		return nil, nil, fmt.Errorf("stored shared profile violates production policy: %w", err)
	}
	if err := sharedApprovedModelID(receipt.Model); err != nil {
		return nil, nil, err
	}
	var host struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(files["public/host.json"], &host) != nil || host.Model != receipt.Model {
		return nil, nil, fmt.Errorf("shared host model differs from installation receipt")
	}
	accessRaw, err := sharedBasePublic(base, "access.json")
	if err != nil {
		return nil, nil, err
	}
	access, err := deploymentconfig.Load(accessRaw)
	if err != nil || access.TenantID() != receipt.Tenant {
		return nil, nil, fmt.Errorf("shared tenant differs from sealed installation")
	}
	success = true
	return &receipt, files, nil
}

func checkSharedInstallationEnvironment(base string, receipt *sharedInstallationReceipt) error {
	node, ca, err := sharedNodeIdentity(base)
	if err != nil {
		return err
	}
	if node != receipt.Node || ca != receipt.CA {
		return fmt.Errorf("shared Node or CA changed; pool identity cannot be implicitly replaced")
	}
	_, err = sharedCommittedRuntime(base)
	return err
}
