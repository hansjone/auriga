package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

type PoolResourceSource interface {
	Snapshot(context.Context, *nodev1.SharedDshBinding) (*workspacev1.GetSharedDshResourceSnapshotResponse, error)
}
type PoolMaterializer struct {
	Layout      *storage.PoolLayout
	Source      PoolResourceSource
	Unified     *UnifiedManager
	mu          sync.Mutex
	clockMu     sync.Mutex
	trusted     time.Time
	trustedMono int64
}

// Seeded only by the authenticated NodeControl Welcome or artifact response.
// Its own small mutex never waits for quota, filesystem or resource RPC work.
func (m *PoolMaterializer) TrustServerTime(stamp time.Time) {
	m.clockMu.Lock()
	defer m.clockMu.Unlock()
	mono := m.Unified.mono()
	current := m.trusted
	if !current.IsZero() && mono >= m.trustedMono {
		current = current.Add(time.Duration(mono-m.trustedMono) * time.Millisecond)
	}
	if stamp.After(current) {
		m.trusted = stamp
		m.trustedMono = mono
	}
}
func (m *PoolMaterializer) Now() time.Time {
	m.clockMu.Lock()
	defer m.clockMu.Unlock()
	now := m.Unified.now()
	trusted := m.trusted
	mono := m.Unified.mono()
	if !trusted.IsZero() && mono >= m.trustedMono {
		trusted = trusted.Add(time.Duration(mono-m.trustedMono) * time.Millisecond)
	}
	if trusted.After(now) {
		now = trusted
	}
	return now
}

type poolResourceState struct {
	SchemaVersion                          int
	Tenant, Pool, Tree, Selection, Pending string
	ObservedAt                             int64
	Watermarks                             map[string]map[string]any
	Revisions                              map[string]uint64
	Intents                                map[string][]byte
	Present                                map[string]bool
	RevocationRevision                     uint64
	RevocationDigest                       string
	Dedicated                              *nodev1.SharedDshBinding
	DedicatedIntent                        string
}

func (m *PoolMaterializer) paths(pool string) (root, active string, err error) {
	root, err = m.Layout.Path(pool, "control", "signed-resources")
	if err != nil {
		return
	}
	active, err = m.Layout.Path(pool, "host", "platform-skills", "active")
	return
}
func (m *PoolMaterializer) Verify(ctx context.Context, pool string, views []*nodev1.SharedDshResourceView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Source == nil || m.Unified == nil || len(m.Unified.Protocol.Keys) == 0 || len(views) == 0 {
		return fmt.Errorf("signed pool resource installation unavailable")
	}
	if !uUUID.MatchString(m.Unified.BootID) || m.Unified.mono() < 0 {
		return fmt.Errorf("pool resource host clock unavailable")
	}
	dedicated := len(views) == 1 && views[0].GetBinding().GetMode() == nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	if dedicated && len(views[0].Resources) > 256 {
		return fmt.Errorf("dedicated resource count exceeded")
	}
	for _, view := range views {
		if view.GetBinding().GetMode() != nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED && !dedicated {
			return fmt.Errorf("dedicated requires one exact current binding")
		}
	}
	root, active, err := m.paths(pool)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	state := poolResourceState{SchemaVersion: 1, Pool: pool, Watermarks: map[string]map[string]any{}, Revisions: map[string]uint64{}}
	err = unifiedRead(filepath.Join(root, "state.json"), &state)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if state.SchemaVersion != 1 || state.Pool != pool || (state.Pending != "" && !dedicated) || state.Watermarks == nil || state.Revisions == nil {
		return fmt.Errorf("incomplete pool resource transaction")
	}
	if state.Dedicated != nil {
		if !dedicated || !proto.Equal(state.Dedicated, views[0].Binding) {
			return fmt.Errorf("dedicated resource binding changed; rebuild required")
		}
	} else if dedicated && (state.Tree != "" || len(state.Revisions) > 0) {
		return fmt.Errorf("shared resource state cannot become dedicated")
	}
	if len(state.Revisions) > 0 && (state.Intents == nil || state.Present == nil) {
		return fmt.Errorf("pool resource installation locks require explicit maintenance")
	}
	if state.Tree != "" {
		tree, e := poolSkillTree(active)
		if e != nil || tree != state.Tree {
			return fmt.Errorf("%w: materialized pool skill view changed", observer.ErrPoolResourceRevoked)
		}
	}
	now := m.Now()
	if now.Unix()+120 < state.ObservedAt {
		return fmt.Errorf("pool resource clock rollback")
	}
	next := state
	next.Intents = map[string][]byte{}
	for k, v := range state.Intents {
		next.Intents[k] = v
	}
	next.Present = map[string]bool{}
	for k := range state.Revisions {
		next.Present[k] = true
	}
	for k, v := range state.Present {
		next.Present[k] = v
	}
	next.RevocationRevision, next.RevocationDigest = 0, ""
	next.Watermarks = map[string]map[string]any{}
	for k, v := range state.Watermarks {
		next.Watermarks[k] = v
	}
	next.Revisions = map[string]uint64{}
	for k, v := range state.Revisions {
		next.Revisions[k] = v
	}
	payloads := map[string][]byte{}
	selection := map[string]string{}
	names := map[string]string{}
	var expectedSet string
	dedicatedResources := []dedicatedResource{}
	for _, view := range views {
		if view == nil || view.Binding == nil || view.Binding.PhysicalInstanceId != pool {
			return fmt.Errorf("invalid pool resource binding")
		}
		if state.Tenant != "" && state.Tenant != view.Binding.TenantId {
			return fmt.Errorf("pool resource tenant changed")
		}
		next.Tenant = view.Binding.TenantId
		snapshot, e := m.Source.Snapshot(ctx, view.Binding)
		if e != nil {
			return e
		}
		if snapshot == nil || !proto.Equal(snapshot.View, view) || snapshot.Revocations == nil || len(snapshot.Resources) != len(view.Resources) {
			return fmt.Errorf("pool resource snapshot mismatch")
		}
		rev, e := unifiedProto(snapshot.Revocations)
		if e != nil {
			return e
		}
		if e = m.Unified.Protocol.Evaluate("revocation_list", map[string]any{"list": rev, "tenantId": view.Binding.TenantId, "now": now.Format(time.RFC3339Nano)}); e != nil {
			return e
		}
		delete(rev, "signature")
		revBytes, e := unifiedCanonical(rev)
		if e != nil {
			return e
		}
		revDigest := udigest(revBytes)
		revision := snapshot.Revocations.Revision
		if revision < state.RevocationRevision || revision == state.RevocationRevision && state.RevocationDigest != "" && state.RevocationDigest != revDigest {
			return fmt.Errorf("pool revocation rollback/conflict")
		}
		if next.RevocationRevision != 0 && (next.RevocationRevision != revision || next.RevocationDigest != revDigest) {
			return fmt.Errorf("members have inconsistent revocation snapshots")
		}
		next.RevocationRevision, next.RevocationDigest = revision, revDigest
		refs := map[string]*nodev1.SharedDshResource{}
		for _, r := range view.Resources {
			if refs[r.ResourceId] != nil {
				return fmt.Errorf("duplicate view resource")
			}
			refs[r.ResourceId] = r
		}
		memberSet := map[string]string{}
		for key := range next.Present {
			if strings.HasPrefix(key, view.Binding.PlacementId+"/") {
				next.Present[key] = false
			}
		}
		for _, ref := range snapshot.Resources {
			id := ref.GetIdentity()
			descriptor := refs[id.GetPackageId()]
			if descriptor == nil || id.GetTenantId() != view.Binding.TenantId || (id.GetKind() != "skill" && (!dedicated || id.GetKind() != "plugin")) || descriptor.Digest != id.GetManifestDigest() || len(ref.Artifacts) != 1 || !uDigest.MatchString(id.GetManifestDigest()) || !uUUID.MatchString(id.GetPackageId()) || !uUUID.MatchString(id.GetVersionId()) {
				return fmt.Errorf("pool signed reference mismatch")
			}
			delete(refs, id.PackageId)
			installation, stateName, e := ReadUnifiedIntent(ref)
			if e != nil || stateName != "ENABLED" || (!dedicated && ref.InstallationMode == "optional") {
				return fmt.Errorf("invalid pool resource distribution")
			}
			key := view.Binding.PlacementId + "/" + id.PackageId
			if installation < state.Revisions[key] {
				return fmt.Errorf("pool installation rollback")
			}
			intent, e := unifiedSelectionBytes(ref)
			if e != nil {
				return e
			}
			if installation == state.Revisions[key] {
				if previous := state.Intents[key]; previous != nil && !bytes.Equal(previous, intent) {
					return fmt.Errorf("pool installation intent changed under the same revision")
				}
				if present, known := state.Present[key]; known && !present {
					return fmt.Errorf("collected pool installation replay")
				}
			}
			next.Intents[key], next.Present[key] = intent, true
			next.Revisions[key] = installation
			end, e := time.Parse(time.RFC3339Nano, ref.GetLease().GetExpiresAt())
			if e != nil || end.Unix() != descriptor.ExpiresAt || !end.After(now) {
				return fmt.Errorf("pool lease expired/mismatched")
			}
			base := "/internal/resource/v1/catalog/downloads/" + descriptor.LeaseId
			if ref.ManifestDownloadPath != base+"/manifest" || ref.Artifacts[0].DownloadPath != base+"/artifacts/"+ref.Artifacts[0].ArtifactId {
				return fmt.Errorf("pool locator does not belong to the member lease")
			}
			manifest, online, e := m.cachedArtifact(ctx, root, ref.ManifestDownloadPath, id.ManifestDigest, "manifest.json", 256*1024)
			if e != nil {
				return e
			}
			if udigest(manifest) != id.ManifestDigest {
				return fmt.Errorf("pool manifest digest mismatch")
			}
			if online.After(now) {
				now = online
			}
			artifact := ref.Artifacts[0]
			if !uDigest.MatchString(artifact.Digest) {
				return fmt.Errorf("invalid pool payload digest")
			}
			maximum := int64(5 * 1024 * 1024)
			if dedicated && id.Kind == "plugin" {
				maximum = 64 * 1024 * 1024
			}
			payload, online, e := m.cachedArtifact(ctx, root, artifact.DownloadPath, artifact.Digest, "payload.tgz", maximum)
			if e != nil {
				return e
			}
			if online.After(now) {
				now = online
			}
			if udigest(payload) != artifact.Digest || uint64(len(payload)) != artifact.SizeBytes {
				return fmt.Errorf("pool payload digest mismatch")
			}
			if e = unifiedAtomicBytes(filepath.Join(root, "cache", strings.TrimPrefix(id.ManifestDigest, "sha256:"), "manifest.json"), manifest); e != nil {
				return e
			}
			if e = unifiedAtomicBytes(filepath.Join(root, "cache", strings.TrimPrefix(artifact.Digest, "sha256:"), "payload.tgz"), payload); e != nil {
				return e
			}
			input, e := m.Unified.validate(root, ref, snapshot.Revocations, now)
			if e != nil {
				if e.Error() == "revoked" {
					return fmt.Errorf("%w: signed resource revoked", observer.ErrPoolResourceRevoked)
				}
				return e
			}
			if mark := state.Watermarks[id.PackageId]; mark != nil {
				if e = m.Unified.Protocol.Evaluate("watermark", map[string]any{"release": input, "watermark": mark}); e != nil {
					return e
				}
			}
			next.Watermarks[id.PackageId] = map[string]any{"tenantId": id.TenantId, "packageId": id.PackageId, "versionId": id.VersionId, "releaseSequence": json.Number(fmt.Sprint(ref.ReleaseSequence)), "verifiedDigestAliases": []any{id.ManifestDigest}}
			name := ref.GetEnvelope().GetStatement().GetName()
			definition, e := DecodeUnifiedJSON(manifest)
			if e != nil {
				return e
			}
			description, _ := definition["description"].(string)
			if dedicated && id.Kind == "plugin" {
				plugin, _ := definition["plugin"].(map[string]any)
				name, _ = plugin["packageName"].(string)
				if !unifiedPluginName(name) {
					return fmt.Errorf("protected or invalid dedicated plugin package")
				}
			} else if !uName.MatchString(name) || len(name) > 64 {
				return fmt.Errorf("invalid pool skill name")
			}
			if prior := names[name]; prior != "" && prior != id.PackageId {
				return fmt.Errorf("pool skill name conflict")
			}
			names[name] = id.PackageId
			if dedicated {
				if e = m.exposeDedicatedResource(pool, root, id.Kind, name, description, id.ManifestDigest, artifact.Digest, manifest, payload); e != nil {
					return e
				}
				dedicatedResources = append(dedicatedResources, dedicatedResource{ResourceID: id.PackageId, ManifestDigest: id.ManifestDigest, ArtifactDigest: artifact.Digest, ArtifactBytes: uint64(len(payload)), InstallationRevision: installation})
			}
			// Common materialization compares the immutable selection projection,
			// excluding only the independent member installation revision. Never
			// change the original ref or manufacture a shared revision for dispatch.
			var common resourcev1.UnifiedResourceRef
			if e = proto.Unmarshal(intent, &common); e != nil {
				return e
			}
			common.InstallationRevision = 0
			commonBytes, e := proto.MarshalOptions{Deterministic: true}.Marshal(&common)
			if e != nil {
				return e
			}
			selected := udigest(commonBytes)
			if prior := selection[id.PackageId]; prior != "" && prior != selected {
				return fmt.Errorf("members have different resource releases")
			}
			selection[id.PackageId], memberSet[id.PackageId] = selected, selected
			if !dedicated {
				payloads[name] = payload
			}
		}
		bytes, _ := json.Marshal(memberSet)
		set := udigest(bytes)
		if expectedSet != "" && expectedSet != set {
			return fmt.Errorf("member authorization resource sets differ")
		}
		expectedSet = set
	}
	bytes, _ := json.Marshal(selection)
	next.Selection = udigest(bytes)
	next.ObservedAt = now.Unix()
	m.TrustServerTime(now)
	if dedicated {
		next.Dedicated = proto.Clone(views[0].Binding).(*nodev1.SharedDshBinding)
		return m.publishDedicatedIntent(ctx, pool, root, state, next, views[0], dedicatedResources)
	}
	if state.Tree != "" {
		tree, e := poolSkillTree(active)
		if e != nil || tree != state.Tree {
			return fmt.Errorf("materialized pool skill view changed")
		}
		if state.Selection != next.Selection {
			return fmt.Errorf("pool resource release change requires stopped maintenance")
		}
		next.Tree = state.Tree
		return unifiedAtomic(filepath.Join(root, "state.json"), next)
	}
	if entries, e := os.ReadDir(active); e == nil && len(entries) > 0 {
		return fmt.Errorf("unowned pool resource view")
	}
	stage := filepath.Join(filepath.Dir(active), "prepared-"+unifiedNonce())
	if err = os.MkdirAll(stage, 0755); err != nil {
		return err
	}
	for name, payload := range payloads {
		directory := filepath.Join(stage, name)
		if err = ExtractUnifiedSkill(payload, directory); err != nil {
			return err
		}
		entry, e := os.ReadFile(filepath.Join(directory, "SKILL.md"))
		if e != nil {
			return e
		}
		text := strings.ReplaceAll(string(entry), "\r\n", "\n")
		end := strings.Index(text[4:], "\n---")
		var metadata struct{ Name, Description string }
		if end < 0 || yaml.Unmarshal([]byte(text[4:4+end]), &metadata) != nil || metadata.Name != name || metadata.Description == "" {
			return fmt.Errorf("pool skill entry identity mismatch")
		}
	}
	next.Tree, err = poolSkillTree(stage)
	if err != nil {
		return err
	}
	next.Pending = stage
	if err = unifiedAtomic(filepath.Join(root, "state.json"), next); err != nil {
		return err
	}
	if err = os.Remove(active); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = unifiedRename(stage, active); err != nil {
		return err
	}
	next.Pending = ""
	return unifiedAtomic(filepath.Join(root, "state.json"), next)
}

func (m *PoolMaterializer) cachedArtifact(ctx context.Context, root, locator, digest, name string, maximum int64) ([]byte, time.Time, error) {
	path := filepath.Join(root, "cache", strings.TrimPrefix(digest, "sha256:"), name)
	if info, e := os.Lstat(path); e == nil && info.Mode().IsRegular() && info.Size() <= maximum {
		if raw, e := os.ReadFile(path); e == nil && udigest(raw) == digest {
			return raw, m.Now(), nil
		}
	}
	raw, trusted, e := m.Unified.download(ctx, locator, maximum)
	if e == nil {
		m.TrustServerTime(trusted)
	}
	return raw, trusted, e
}
func (m *PoolMaterializer) Cleanup(ctx context.Context, pool string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, active, err := m.paths(pool)
	if err != nil {
		return err
	}
	if err = storage.CheckNoSymlink(m.Layout.Root, active); err != nil {
		return err
	}
	if _, err = os.Lstat(active); err == nil {
		target := filepath.Join(filepath.Dir(active), "quarantine-"+unifiedNonce())
		if err = unifiedRename(active, target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(active, 0755); err != nil {
		return err
	}
	var state poolResourceState
	if err = unifiedRead(filepath.Join(root, "state.json"), &state); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	state.Tree, state.Selection, state.Pending = "", "", ""
	if state.Dedicated != nil {
		state.DedicatedIntent = ""
	}
	return unifiedAtomic(filepath.Join(root, "state.json"), state)
}
func poolSkillTree(root string) (string, error) {
	paths := []string{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in pool skill view")
		}
		if !entry.IsDir() {
			info, e := entry.Info()
			if e != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("invalid pool skill file")
			}
			total += info.Size()
			if total > 100*1024*1024 {
				return fmt.Errorf("pool skill budget exceeded")
			}
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, _ := filepath.Rel(root, path)
		_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00")
		file, e := os.Open(path)
		if e != nil {
			return "", e
		}
		_, e = io.Copy(hash, file)
		_ = file.Close()
		if e != nil {
			return "", e
		}
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
