package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	"golang.org/x/sys/unix"
)

const bifrostGeneration = "v-00000000000000000001"

var bifrostLeaves = []string{"bifrost/admin_password", "bifrost/member_password", "bifrost/encryption_key", "bifrost/pg_password", "bifrost/pg_migration_password", "bifrost-runtime.json"}

type materialsUpgradeReceipt struct {
	Version int    `json:"version"`
	Base    string `json:"base"`
	Collab  string `json:"collab"` // Historical staging snapshot, not an activation invariant.
	Files   string `json:"files"`
}

type materialsUpgradeEvent struct {
	Version    int    `json:"version"`
	Sequence   uint64 `json:"sequence"`
	Previous   string `json:"previous"`
	Generation string `json:"generation"`
	Action     string `json:"action"`
}

func nasMaterialsUpgrade(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("materials-upgrade", flag.ContinueOnError)
	base := f.String("base", "", "original sealed NAS source")
	out := f.String("out", "", "separate materials-upgrades directory")
	action := f.String("action", "status", "stage, commit, rollback, check, path, project or project-core")
	expected := f.String("expected-base-sha256", "", "independently checked original receipt digest")
	access := f.String("access-file", "", "unchanged deployment access input")
	version := f.Uint64("version", 1, "approved schema version (1)")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || os.Geteuid() != 0 || *version != 1 || !filepath.IsAbs(*base) || filepath.Clean(*base) != *base || *out != filepath.Join(filepath.Dir(*base), "materials-upgrades") {
		return fmt.Errorf("materials upgrade requires root, canonical sibling paths and version 1")
	}
	mutating := *action == "stage" || *action == "commit" || *action == "rollback"
	if !mutating && *action != "status" && *action != "path" && *action != "check" && *action != "project" && *action != "project-core" {
		return fmt.Errorf("unsupported materials operation")
	}
	baseHash, err := verifyNASCollabBase(*base)
	if err != nil {
		return err
	}
	if mutating && *expected != baseHash {
		return fmt.Errorf("original material receipt differs from approval")
	}
	if *access != "" {
		a, err := os.ReadFile(*access)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(*base, "access.json"))
		if err != nil || !bytes.Equal(a, b) {
			return fmt.Errorf("access input differs from original installation")
		}
	}
	if err := checkNASCollabParent(filepath.Dir(*out)); err != nil {
		return err
	}
	if _, err := os.Lstat(*out); os.IsNotExist(err) {
		if *action != "stage" {
			if *action == "status" {
				_, err = fmt.Fprintf(stdout, "base=%s state=original version=0\n", baseHash)
				return err
			}
			if *action == "path" {
				if _, e := os.Lstat(filepath.Join(*base, "bifrost-runtime.json")); e == nil {
					_, err = fmt.Fprint(stdout, *base)
				}
				return err
			}
			return fmt.Errorf("materials upgrade has not been staged")
		}
		if err := os.Mkdir(*out, 0700); err != nil {
			return err
		}
		if err := os.Chmod(*out, 0700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := materialsPrivateDirectory(*out); err != nil {
		return err
	}
	// Controllers mount original materials read-only. Observers never create or
	// repair a lock; the shared lock still excludes every activation/projection.
	flags, lock := unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, unix.LOCK_SH
	if *action != "status" && *action != "path" && *action != "check" {
		flags, lock = unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, unix.LOCK_EX
	}
	fd, err := unix.Open(filepath.Join(*out, ".lock"), flags, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0600 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fmt.Errorf("unsafe materials lock")
	}
	if err := unix.Flock(fd, lock); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	entries, err := os.ReadDir(*out)
	if err != nil {
		return err
	}
	generation := filepath.Join(*out, bifrostGeneration)
	var events []string
	hasGeneration := false
	for _, e := range entries {
		n := e.Name()
		if n == ".lock" {
			continue
		}
		if n == bifrostGeneration {
			hasGeneration = true
			continue
		}
		if len(n) != 27 || !strings.HasPrefix(n, "e-") || !strings.HasSuffix(n, ".json") {
			return fmt.Errorf("unknown or interrupted materials upgrade")
		}
		events = append(events, n)
	}
	if !hasGeneration && len(events) > 0 {
		return fmt.Errorf("materials generation missing; rollback refused")
	}
	if !hasGeneration {
		if *action != "stage" {
			return fmt.Errorf("materials generation absent")
		}
		if _, err := os.Lstat(filepath.Join(*base, "bifrost-runtime.json")); err == nil {
			return fmt.Errorf("bifrost already sealed in base; implicit secret rotation refused")
		}
		if err := stageBifrostMaterials(*base, *out, baseHash); err != nil {
			return err
		}
	}
	receiptRaw, err := runtimeReadLeaf(filepath.Join(generation, "upgrade.receipt"), 0600, 65536)
	if err != nil {
		return err
	}
	var receipt materialsUpgradeReceipt
	if err := runtimeDecode(receiptRaw, &receipt); err != nil {
		return err
	}
	files, err := bifrostSourceDigest(generation)
	// Collab may be activated after Bifrost. Its current source is independently
	// sealed and checked by render-collab; do not freeze its earlier receipt here.
	// Its optional receipt metadata must not couple message failure to core checks.
	if err != nil || receipt.Version != 1 || receipt.Base != baseHash || receipt.Files != files {
		return fmt.Errorf("materials generation or original binding changed")
	}
	genHash := fmt.Sprintf("%x", sha256.Sum256(receiptRaw))
	previous := genHash
	state := "staged"
	for i, name := range events {
		raw, err := runtimeReadLeaf(filepath.Join(*out, name), 0600, 65536)
		if err != nil {
			return err
		}
		var e materialsUpgradeEvent
		if err := runtimeDecode(raw, &e); err != nil {
			return err
		}
		if e.Version != 1 || e.Sequence != uint64(i+2) || name != fmt.Sprintf("e-%020d.json", e.Sequence) || e.Previous != previous || e.Generation != genHash {
			return fmt.Errorf("materials receipt chain rollback refused")
		}
		next, err := materialsNextState(state, e.Action)
		if err != nil {
			return err
		}
		state = next
		previous = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	if *action == "status" {
		_, err = fmt.Fprintf(stdout, "base=%s state=%s version=1 records=%d\n", baseHash, state, len(events)+1)
		return err
	}
	if *action == "check" || *action == "path" || *action == "project" || *action == "project-core" {
		if state == "rolled-back" {
			return fmt.Errorf("materials activation rolled back; explicit stage required")
		}
		if *action == "path" {
			_, err = fmt.Fprint(stdout, generation)
			return err
		}
		if *action == "project" || *action == "project-core" {
			return projectBifrostEdge(*base, generation, *action == "project-core")
		}
		return nil
	}
	eventAction := *action
	if eventAction == "stage" {
		if state == "staged" || state == "committed" {
			return nil
		}
		eventAction = "resume"
	}
	if eventAction == "commit" && state == "committed" || eventAction == "rollback" && state == "rolled-back" {
		return nil
	}
	if _, err := materialsNextState(state, eventAction); err != nil {
		return err
	}
	e := materialsUpgradeEvent{1, uint64(len(events) + 2), previous, genHash, eventAction}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if fresh, err := verifyNASCollabBase(*base); err != nil || fresh != baseHash {
		return fmt.Errorf("base changed before materials event")
	}
	return materialsAppend(*out, fmt.Sprintf("e-%020d.json", e.Sequence), raw)
}

func materialsNextState(state, action string) (string, error) {
	if state == "staged" && action == "commit" {
		return "committed", nil
	}
	if state == "staged" && action == "rollback" {
		return "rolled-back", nil
	}
	if state == "rolled-back" && action == "resume" {
		return "staged", nil
	}
	return "", fmt.Errorf("materials state transition or committed rollback refused")
}

func materialsPrivateDirectory(path string) error {
	if err := checkNASCollabParent(path); err != nil {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var v unix.Stat_t
	if unix.Fstat(fd, &v) != nil || v.Uid != 0 || v.Gid != 0 || v.Mode&07777 != 0700 {
		return fmt.Errorf("unsafe private materials directory")
	}
	return nil
}

func materialsCreate(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	return closed
}

func materialsAppend(out, name string, raw []byte) error {
	f, err := os.CreateTemp(out, ".event-")
	if err != nil {
		return err
	}
	p := f.Name()
	defer func() { _ = os.Remove(p) }()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	// link is exclusive and publishes a complete immutable file, never replacing history.
	if err = os.Link(p, filepath.Join(out, name)); err != nil {
		return err
	}
	if err = os.Remove(p); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}

func materialsCollabDigest(base string) (string, error) {
	path := filepath.Join(filepath.Dir(base), "collab-source", "activation.receipt")
	raw, err := runtimeReadLeaf(path, 0600, 65536)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func stageBifrostMaterials(base, out, baseHash string) error {
	stage, err := os.MkdirTemp(out, ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0700); err != nil {
		return err
	}
	if err := materials.EnsureBifrostSecrets(filepath.Join(stage, "bifrost")); err != nil {
		return err
	}
	raw, err := json.Marshal(bifrostRuntimeConfig())
	if err != nil {
		return err
	}
	if err := materialsCreate(filepath.Join(stage, "bifrost-runtime.json"), raw); err != nil {
		return err
	}
	digest, err := bifrostSourceDigest(stage)
	if err != nil {
		return err
	}
	collabHash, err := materialsCollabDigest(base)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(materialsUpgradeReceipt{1, baseHash, collabHash, digest})
	if err != nil {
		return err
	}
	if err := materialsCreate(filepath.Join(stage, "upgrade.receipt"), raw); err != nil {
		return err
	}
	if fresh, err := verifyNASCollabBase(base); err != nil || fresh != baseHash {
		return fmt.Errorf("base changed while staging materials")
	}
	if err := runtimeSyncTree(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, filepath.Join(out, bifrostGeneration)); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}

func bifrostSourceDigest(dir string) (string, error) {
	if err := materialsPrivateDirectory(dir); err != nil {
		return "", err
	}
	if err := materialsPrivateDirectory(filepath.Join(dir, "bifrost")); err != nil {
		return "", err
	}
	allowed := map[string]bool{"bifrost": true, "upgrade.receipt": true}
	for _, n := range bifrostLeaves {
		allowed[n] = true
	}
	if err := filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if !allowed[rel] {
			return fmt.Errorf("unknown materials source leaf")
		}
		return nil
	}); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, n := range bifrostLeaves {
		raw, err := runtimeReadLeaf(filepath.Join(dir, n), 0600, 1<<20)
		if err != nil {
			return "", err
		}
		_, err = fmt.Fprintf(h, "%s:%x\n", n, sha256.Sum256(raw))
		clear(raw)
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func projectBifrostEdge(base, generation string, coreOnly bool) error {
	config := filepath.Dir(base)
	source := filepath.Join(base, "edge.config.json")
	// Core render uses only the verified installation base. Message render calls
	// strict project only after independently checking/rebuilding its projection.
	if !coreOnly {
		if _, err := os.Lstat(filepath.Join(config, "collab-source")); err == nil {
			source = filepath.Join(config, "collab-runtime", "edge", "edge.config.json")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	// render-collab verifies its sealed source and projections before this step.
	raw, err := bifrostEdgeProjection(source)
	if err != nil {
		return err
	}
	out := filepath.Join(config, "bifrost")
	if err := materialsPrivateDirectory(out); err != nil {
		return err
	}
	for _, leaf := range []string{"edge.config.json", "bifrost-runtime.json"} {
		body := raw
		if leaf == "bifrost-runtime.json" {
			body, err = runtimeReadLeaf(filepath.Join(generation, leaf), 0600, 1<<20)
			if err != nil {
				return err
			}
		}
		if err := materialsProject(out, leaf, body); err != nil {
			return err
		}
	}
	return nil
}

// The renderer and read-only controller verification derive identical bytes.
func bifrostEdgeProjection(source string) ([]byte, error) {
	f, err := os.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closed := f.Close()
	if err != nil {
		return nil, err
	}
	if closed != nil {
		return nil, closed
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("edge projection too large")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("invalid edge projection")
	}
	delete(doc, "litellmOrigin")
	delete(doc, "litellmUI")
	doc["relay"] = json.RawMessage(`{"origin":"http://bifrost:8080","adminPasswordFile":"/run/secrets/bifrost_admin_password"}`)
	return json.Marshal(doc)
}

func materialsProject(out, leaf string, raw []byte) error {
	target := filepath.Join(out, leaf)
	if _, err := os.Lstat(target); err == nil {
		if _, err := runtimeReadLeaf(target, 0644, 1<<20); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(out, ".project-")
	if err != nil {
		return err
	}
	p := f.Name()
	defer func() { _ = os.Remove(p) }()
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	if err = os.Rename(p, target); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}
