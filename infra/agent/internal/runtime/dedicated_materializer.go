package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

type dedicatedResource struct {
	ResourceID           string `json:"resourceId"`
	ManifestDigest       string `json:"manifestDigest"`
	ArtifactDigest       string `json:"artifactDigest"`
	ArtifactBytes        uint64 `json:"artifactBytes"`
	InstallationRevision uint64 `json:"installationRevision"`
}
type dedicatedIntent struct {
	SchemaVersion int                 `json:"schemaVersion"`
	View          any                 `json:"view"`
	Resources     []dedicatedResource `json:"resources"`
}

func (m *PoolMaterializer) dedicatedPaths(pool string) (cache, intent string, err error) {
	cache, err = m.Layout.Path(pool, "control", "dedicated-artifacts")
	if err != nil {
		return
	}
	var directory string
	directory, err = m.Layout.Path(pool, "control", "host-relay")
	if err != nil {
		return
	}
	intent, err = storage.SafeJoin(directory, "dedicated-resources.json")
	return
}

// Refresh consumes the existing W snapshot each placement cycle. The complete
// view must still pass Verify's original signatures, leases and high-water
// checks; unchanged W binding/digest is not an installation selection cache.
func (m *PoolMaterializer) Refresh(ctx context.Context, b *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error) {
	if m.Source == nil || !validPoolBinding(b) {
		return nil, fmt.Errorf("resource authority unavailable")
	}
	snapshot, err := m.Source.Snapshot(ctx, b)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.View == nil || !proto.Equal(snapshot.View.Binding, b) {
		return nil, fmt.Errorf("resource snapshot binding mismatch")
	}
	return proto.Clone(snapshot.View).(*nodev1.SharedDshResourceView), nil
}

func dedicatedLiteral(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	object, err := DecodeUnifiedJSON(raw)
	if err != nil {
		return nil, err
	}
	return unifiedCanonical(object)
}

func (m *PoolMaterializer) publishDedicatedIntent(ctx context.Context, pool, root string, before, next poolResourceState, view *nodev1.SharedDshResourceView, resources []dedicatedResource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validPoolBinding(view.Binding) || len(view.Resources) > 256 {
		return fmt.Errorf("invalid dedicated resource view")
	}
	publicView, err := sharednode.HostValue(view)
	if err != nil {
		return err
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ResourceID < resources[j].ResourceID })
	raw, err := dedicatedLiteral(dedicatedIntent{SchemaVersion: 1, View: publicView, Resources: resources})
	if err != nil {
		return err
	}
	if len(raw) > 1024*1024 {
		return fmt.Errorf("dedicated resource intent budget exceeded")
	}
	digest := udigest(raw)
	if before.Pending != "" && before.Pending != digest {
		return fmt.Errorf("dedicated resource transaction superseded while pending")
	}
	_, file, err := m.dedicatedPaths(pool)
	if err != nil {
		return err
	}
	if err = storage.CheckNoSymlink(m.Layout.Root, file); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	next.Pending = digest
	next.DedicatedIntent = digest
	if err = unifiedAtomic(filepath.Join(root, "state.json"), next); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unifiedAtomicMode(file, raw, 0644); err != nil {
		return err
	}
	confirmed, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(confirmed, raw) {
		return fmt.Errorf("dedicated resource intent readback mismatch")
	}
	next.Pending = ""
	return unifiedAtomic(filepath.Join(root, "state.json"), next)
}

// DedicatedIntent returns only a fully committed current Node intent. Host
// ready must match its full binding, view and this canonical digest exactly.
func (m *PoolMaterializer) DedicatedIntent(pool string, view *nodev1.SharedDshResourceView) (cache, digest string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, _, err := m.paths(pool)
	if err != nil {
		return "", "", err
	}
	var state poolResourceState
	if err = unifiedRead(filepath.Join(root, "state.json"), &state); err != nil {
		return "", "", err
	}
	if view == nil || !proto.Equal(state.Dedicated, view.Binding) || state.Pending != "" || !uDigest.MatchString(state.DedicatedIntent) {
		return "", "", fmt.Errorf("dedicated intent is not current")
	}
	cache, file, err := m.dedicatedPaths(pool)
	if err != nil {
		return "", "", err
	}
	if err = storage.CheckNoSymlink(m.Layout.Root, file); err != nil {
		return "", "", err
	}
	var intent dedicatedIntent
	if err = unifiedRead(file, &intent); err != nil {
		return "", "", err
	}
	raw, err := dedicatedLiteral(intent)
	if err != nil || udigest(raw) != state.DedicatedIntent {
		return "", "", fmt.Errorf("dedicated intent changed outside its transaction")
	}
	expected, err := sharednode.HostValue(view)
	if err != nil {
		return "", "", err
	}
	a, err := dedicatedLiteral(map[string]any{"view": expected})
	if err != nil {
		return "", "", err
	}
	b, err := dedicatedLiteral(map[string]any{"view": intent.View})
	if err != nil || !bytes.Equal(a, b) {
		return "", "", fmt.Errorf("dedicated intent view mismatch")
	}
	return cache, state.DedicatedIntent, nil
}

func (m *PoolMaterializer) exposeDedicatedResource(pool, root, kind, name, description, manifestDigest, artifactDigest string, manifest, payload []byte) error {
	cache, _, err := m.dedicatedPaths(pool)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		digest, name string
		raw          []byte
	}{{manifestDigest, "manifest.json", manifest}, {artifactDigest, "payload.tgz", payload}} {
		dir := filepath.Join(cache, strings.TrimPrefix(entry.digest, "sha256:"))
		file := filepath.Join(dir, entry.name)
		if err = storage.CheckNoSymlink(m.Layout.Root, file); err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		if old, readErr := os.ReadFile(file); readErr == nil {
			if !bytes.Equal(old, entry.raw) {
				return fmt.Errorf("immutable dedicated artifact changed")
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		} else if err = unifiedAtomicMode(file, entry.raw, 0644); err != nil {
			return err
		}
	}
	if kind != "skill" {
		return nil
	}
	staging := filepath.Join(root, "staging")
	if err = storage.CheckNoSymlink(m.Layout.Root, staging); err != nil {
		return err
	}
	if err = os.MkdirAll(staging, 0700); err != nil {
		return err
	}
	stageRoot, err := os.MkdirTemp(staging, "dedicated-skill-")
	if err != nil {
		return err
	}
	if err = storage.AssertUnderRoot(root, stageRoot); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()
	stage := filepath.Join(stageRoot, "skill")
	if err = ExtractUnifiedSkill(payload, stage); err != nil {
		return err
	}
	entry, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(entry), "\r\n", "\n")
	if len(text) < 4 {
		return fmt.Errorf("invalid dedicated skill entry")
	}
	end := strings.Index(text[4:], "\n---")
	var metadata struct{ Name, Description string }
	if end < 0 || yaml.Unmarshal([]byte(text[4:4+end]), &metadata) != nil || metadata.Name != name || metadata.Description == "" || metadata.Description != description {
		return fmt.Errorf("dedicated skill entry identity mismatch")
	}
	expected, err := poolSkillTree(stage)
	if err != nil {
		return err
	}
	if err = filepath.WalkDir(stage, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if entry.IsDir() {
			mode = 0755
		}
		return os.Chmod(path, mode)
	}); err != nil {
		return err
	}
	target := filepath.Join(cache, strings.TrimPrefix(artifactDigest, "sha256:"), "skill")
	if err = storage.CheckNoSymlink(m.Layout.Root, target); err != nil {
		return err
	}
	if _, err = os.Lstat(target); err == nil {
		actual, err := poolSkillTree(target)
		if err != nil || actual != expected {
			return fmt.Errorf("immutable dedicated skill tree changed")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return unifiedRename(stage, target)
}
