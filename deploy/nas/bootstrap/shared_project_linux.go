package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sharedProjectionReceipt struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	Files   string `json:"files"`
}

func sharedRoleDigest(files map[string]sharedProjectedFile) string {
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var body strings.Builder
	for _, name := range names {
		file := files[name]
		fmt.Fprintf(&body, "%s:%d:%04o:%s\n", name, file.UID, file.Mode, sharedSHA(file.Body))
	}
	return sharedSHA([]byte(body.String()))
}

func projectSharedInstallation(base string, receipt *sharedInstallationReceipt, source map[string][]byte) (string, error) {
	node, ca, err := sharedNodeIdentity(base)
	if err != nil {
		return "", err
	}
	if node != receipt.Node || ca != receipt.CA {
		return "", fmt.Errorf("shared Node or CA changed; implicit pool replacement refused")
	}
	runtimeJSON, err := sharedRuntimeSnapshot(base, true)
	if err != nil {
		return "", err
	}
	caPEM, err := sharedBasePublic(base, "pki/ca.crt")
	if err != nil {
		return "", err
	}
	config := filepath.Dir(base)
	var bifrostPath bytes.Buffer
	if err := nasMaterialsUpgrade([]string{"-base", base, "-out", filepath.Join(config, "materials-upgrades"), "-action", "path"}, &bifrostPath); err != nil {
		return "", err
	}
	if bifrostPath.Len() == 0 {
		return "", fmt.Errorf("standard Bifrost materials must be installed before shared projection")
	}
	var bifrostRaw, bifrostPG, bifrostMigPG []byte
	if bifrostPath.String() == base {
		bifrostRaw, err = sharedBasePublic(base, "bifrost-runtime.json")
		if err == nil {
			bifrostPG, err = sharedBaseLeaf(base, "bifrost/pg_password")
		}
		if err == nil {
			bifrostMigPG, err = sharedBaseLeaf(base, "bifrost/pg_migration_password")
		}
	} else {
		bifrostRaw, err = runtimeReadLeaf(filepath.Join(bifrostPath.String(), "bifrost-runtime.json"), 0600, 1<<20)
		if err == nil {
			bifrostPG, err = runtimeReadLeaf(filepath.Join(bifrostPath.String(), "bifrost/pg_password"), 0600, 1024)
		}
		if err == nil {
			bifrostMigPG, err = runtimeReadLeaf(filepath.Join(bifrostPath.String(), "bifrost/pg_migration_password"), 0600, 1024)
		}
	}
	defer clear(bifrostPG)
	defer clear(bifrostMigPG)
	if err != nil {
		return "", err
	}
	edgeSource := filepath.Join(base, "edge.config.json")
	if _, err := os.Lstat(filepath.Join(config, "collab-source")); err == nil {
		if _, err := checkNASCollabSource(filepath.Join(config, "collab-source"), receipt.Base); err == nil {
			edgeSource = filepath.Join(config, "collab-source/edge.config.json")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Reuse the existing verified Edge/Bifrost adapter rather than replacing its
	// resource/login/relay settings with a handwritten shared-only configuration.
	edgeRaw, err := bifrostEdgeProjection(edgeSource)
	if err != nil {
		return "", err
	}
	natsPath := filepath.Join(config, "nats/nats.conf")
	if err := checkNASCollabParent(filepath.Dir(natsPath)); err != nil {
		return "", err
	}
	natsRaw, err := sharedRoleReadLeaf(natsPath, 65532, 0600, 256<<10)
	if err != nil {
		return "", err
	}
	defer clear(natsRaw)
	files, err := sharedServiceProjection(sharedProjectionInputs{
		Receipt:                    receipt,
		Source:                     source,
		RuntimeJSON:                runtimeJSON,
		InternalCA:                 caPEM,
		EdgeConfig:                 edgeRaw,
		BifrostConfig:              bifrostRaw,
		BifrostPGPassword:          bifrostPG,
		BifrostPGMigrationPassword: bifrostMigPG,
		NATSConfig:                 natsRaw,
	})
	if err != nil {
		return "", err
	}
	defer func() {
		for _, file := range files {
			clear(file.Body)
		}
	}()
	out := filepath.Join(config, "shared-runtime")
	if _, err := os.Lstat(out); os.IsNotExist(err) {
		if err := os.Mkdir(out, 0755); err != nil {
			return "", err
		}
		if err := os.Chmod(out, 0755); err != nil {
			return "", err
		}
		if err := runtimePublicDirectory(out); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := sharedRoleDirectory(out, 0, 0755); err != nil {
		return "", err
	}
	if err := checkNASCollabParent(out); err != nil {
		return "", err
	}
	digest := sharedRoleDigest(files)
	generation := filepath.Join(out, "g-"+digest)
	projectionReceipt := sharedProjectionReceipt{Version: 1, Source: receipt.Files, Files: digest}
	if _, err := os.Lstat(generation); err == nil {
		if err := checkSharedRoleProjection(generation, projectionReceipt, files); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else {
		stage := filepath.Join(out, ".pending")
		if err := os.Mkdir(stage, 0700); err != nil {
			return "", fmt.Errorf("shared projection staging exists; inspect preserved incomplete evidence")
		}
		roles := map[string]uint32{"platform": 10001, "edge": 65532, "gateway": 10001, "agent": 0, "nats": 65532}
		for role, uid := range roles {
			path := filepath.Join(stage, role)
			if err := os.Mkdir(path, 0700); err != nil {
				return "", err
			}
			if err := os.Chown(path, int(uid), int(uid)); err != nil {
				return "", err
			}
		}
		secretDir := filepath.Join(stage, "platform/agent-secret")
		if err := os.Mkdir(secretDir, 0700); err != nil {
			return "", err
		}
		if err := os.Chown(secretDir, 10001, 10001); err != nil {
			return "", err
		}
		for name, file := range files {
			if err := sharedRoleCreate(filepath.Join(stage, filepath.FromSlash(name)), file); err != nil {
				return "", err
			}
		}
		raw, err := json.Marshal(projectionReceipt)
		if err != nil {
			return "", err
		}
		if err := materialsCreate(filepath.Join(stage, "projection.receipt"), raw); err != nil {
			return "", err
		}
		if err := runtimeSyncTree(stage); err != nil {
			return "", err
		}
		if err := os.Chmod(stage, 0755); err != nil {
			return "", err
		}
		if err := os.Rename(stage, generation); err != nil {
			return "", err
		}
		if err := runtimeSyncDir(out); err != nil {
			return "", err
		}
		if err := checkSharedRoleProjection(generation, projectionReceipt, files); err != nil {
			return "", err
		}
	}
	// The publisher owns only this dedicated POSIX0700 output directory. Its
	// atomically replaced 0600 JWT is mounted by directory, never by old inode.
	data := filepath.Dir(config)
	for _, path := range []string{filepath.Join(data, "agent-tokens"), filepath.Join(data, "agent-tokens/agent")} {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if err := os.Mkdir(path, 0700); err != nil {
				return "", err
			}
			if err := os.Chown(path, 10001, 10001); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		if err := sharedRoleDirectory(path, 10001, 0700); err != nil {
			return "", err
		}
	}
	if err := ensureSharedAgentResourceTrust(data); err != nil {
		return "", err
	}
	if fresh, err := verifyNASCollabBase(base); err != nil || fresh != receipt.Base {
		return "", fmt.Errorf("sealed base changed during shared projection")
	}
	return generation, nil
}

func checkSharedRoleProjection(dir string, receipt sharedProjectionReceipt, files map[string]sharedProjectedFile) error {
	allowed := map[string]bool{".": true, "projection.receipt": true}
	roles := map[string]uint32{"platform": 10001, "platform/agent-secret": 10001, "edge": 65532, "gateway": 10001, "agent": 0, "nats": 65532}
	for role := range roles {
		allowed[role] = true
	}
	for name := range files {
		allowed[name] = true
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if !allowed[name] {
			return fmt.Errorf("unknown immutable shared projection leaf")
		}
		if entry.IsDir() {
			if name == "." {
				return sharedRoleDirectory(path, 0, 0755)
			}
			return sharedRoleDirectory(path, roles[name], 0700)
		}
		return nil
	}); err != nil {
		return err
	}
	raw, err := runtimeReadLeaf(filepath.Join(dir, "projection.receipt"), 0600, 65536)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(raw, expected) {
		return fmt.Errorf("shared projection receipt changed")
	}
	for name, file := range files {
		actual, err := sharedRoleReadLeaf(filepath.Join(dir, filepath.FromSlash(name)), file.UID, file.Mode, 4<<20)
		if err != nil {
			return err
		}
		same := bytes.Equal(actual, file.Body)
		clear(actual)
		if !same {
			return fmt.Errorf("immutable shared role projection changed")
		}
	}
	return nil
}
