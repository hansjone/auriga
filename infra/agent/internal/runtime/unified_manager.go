package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

var ErrUnifiedPrivateOnly = errors.New("shared skills isolated; a new private session is required")

type unifiedInstalled struct {
	Ref                  []byte `json:"ref"`
	InstallationRevision uint64 `json:"installationRevision"`
	DesiredState         string `json:"desiredState"`
	Name                 string `json:"name"`
	State                string `json:"state"`
	AuthorizationID      string `json:"authorizationId,omitempty"`
}
type unifiedNodeState struct {
	SchemaVersion         int `json:"schemaVersion"`
	Tenant, Owner, Host   string
	SelectionRevision     uint64
	Generation            int64
	Revision              uint64
	State, Reason         string
	Clock                 map[string]any
	LeaseExpiresAt        string
	Installations         []unifiedInstalled
	Watermarks            map[string]map[string]any
	RevocationRevision    uint64
	RevocationDigest      string
	PendingIsolation      string
	PendingActivation     string
	HasShared             bool
	Migrations            map[string]map[string]any
	UsedAuthorizations    []string
	InstallationRevisions map[string]uint64
	SelectionLocks        map[string]unifiedSelectionLock
	ObservedAt            string
}

// UnifiedManager is the cloud's only shared-skill writer. Its private ledger,
// cache and recovery journal stay outside the container's home/meta mounts. Only
// current is exposed to the instance, through an additional read-only mount.
// The agent's exclusively locked state database and this mutex serialize writers.
type UnifiedManager struct {
	Layout   *storage.Layout
	Driver   Driver
	Specs    SpecSource
	Protocol UnifiedProtocol
	Origin   string
	Client   *http.Client
	Now      func() time.Time
	Mono     func() int64
	BootID   string
	locks    sync.Map
}

func (m *UnifiedManager) lock(id string) func() {
	value, _ := m.locks.LoadOrStore(id, new(sync.Mutex))
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func unifiedNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	s := hex.EncodeToString(raw[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// ReadUnifiedIntent validates generated K2 fields. This consumer requires an
// explicit desired state; legacy enabled alone never authorizes a tombstone.
func ReadUnifiedIntent(ref *resourcev1.UnifiedResourceRef) (uint64, string, error) {
	r := ref.GetInstallationRevision()
	if ref == nil || r < 1 || r > 9007199254740991 || ref.DesiredState == nil {
		return 0, "", fmt.Errorf("invalid unified intent")
	}
	state := ""
	switch ref.GetDesiredState() {
	case resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_ENABLED:
		state = "ENABLED"
	case resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED:
		state = "DISABLED"
	case resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED:
		state = "REMOVED"
	default:
		return 0, "", fmt.Errorf("invalid unified desired state")
	}
	if ref.GetEnabled() != (state == "ENABLED") {
		return 0, "", fmt.Errorf("inconsistent unified intent")
	}
	return r, state, nil
}

func unifiedProto(message proto.Message) (map[string]any, error) {
	raw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		return nil, err
	}
	result, err := DecodeUnifiedJSON(raw)
	if err != nil {
		return nil, err
	}
	var convert func(any)
	convert = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				switch key {
				case "schemaVersion", "releaseSequence", "revision", "firstReleaseSequence", "fromReleaseSequence", "toReleaseSequence", "sizeBytes", "installationRevision":
					if text, ok := child.(string); ok {
						v[key] = json.Number(text)
					}
				}
				convert(child)
			}
		case []any:
			for _, child := range v {
				convert(child)
			}
		}
	}
	convert(result)
	return result, nil
}
func (m *UnifiedManager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m *UnifiedManager) mono() int64 {
	if m.Mono != nil {
		return m.Mono()
	}
	return 0
}
func (m *UnifiedManager) paths(id string) (string, string, error) {
	paths, err := m.Layout.For(id)
	if err != nil {
		return "", "", err
	}
	root, err := storage.SafeJoin(paths.Root, "resource-state")
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", "", err
	}
	if err := unifiedLocalFS(root); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("unsafe managed resource root")
	}
	return root, filepath.Join(root, "skills-active", "current"), nil
}
func (m *UnifiedManager) load(id string) (*unifiedNodeState, string, string, error) {
	root, current, err := m.paths(id)
	if err != nil {
		return nil, "", "", err
	}
	s := &unifiedNodeState{SchemaVersion: 1, Host: id, State: "verifying", Watermarks: map[string]map[string]any{}, Installations: []unifiedInstalled{}}
	err = unifiedRead(filepath.Join(root, "node-state.json"), s)
	if os.IsNotExist(err) {
		if entries, readErr := os.ReadDir(current); readErr == nil && len(entries) > 0 {
			return nil, "", "", fmt.Errorf("unowned shared view")
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, "", "", err
	}
	if s.SchemaVersion != 1 || s.Host != id || s.Watermarks == nil {
		return nil, "", "", fmt.Errorf("invalid managed resource ledger")
	}
	if s.InstallationRevisions == nil {
		s.InstallationRevisions = map[string]uint64{}
	}
	switch s.State {
	case "verifying", "active", "stopping", "isolating", "private_only", "blocked":
	default:
		return nil, "", "", fmt.Errorf("unknown guardian state")
	}
	return s, root, current, nil
}
func (m *UnifiedManager) save(root string, s *unifiedNodeState) error {
	s.Revision++
	return unifiedAtomic(filepath.Join(root, "node-state.json"), s)
}

// Download uses an exact same-origin locator, mTLS client and no redirects.
// A locator is never treated as an authorization token and no credentials go in it.
func (m *UnifiedManager) download(ctx context.Context, locator string, maximum int64) ([]byte, time.Time, error) {
	u, err := url.Parse(locator)
	if err != nil || !strings.HasPrefix(locator, "/internal/resource/v1/catalog/downloads/") || strings.HasPrefix(locator, "//") || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(locator, "\\%") || strings.Contains(locator, "//") {
		return nil, time.Time{}, fmt.Errorf("unsafe resource locator")
	}
	for _, part := range strings.Split(locator, "/") {
		if part == "." || part == ".." {
			return nil, time.Time{}, fmt.Errorf("unsafe resource locator")
		}
	}
	parts := strings.Split(strings.TrimPrefix(locator, "/internal/resource/v1/catalog/downloads/"), "/")
	manifestRoute := len(parts) == 2 && parts[1] == "manifest"
	artifactRoute := len(parts) == 3 && parts[1] == "artifacts" && uName.MatchString(parts[2])
	if len(parts) < 2 || !uUUID.MatchString(parts[0]) || !manifestRoute && !artifactRoute {
		return nil, time.Time{}, fmt.Errorf("invalid resource download locator")
	}
	base, err := url.Parse(m.Origin)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" && base.Path != "/" {
		return nil, time.Time{}, fmt.Errorf("trusted resource mTLS origin required")
	}
	target := base.ResolveReference(u)
	if m.Client == nil {
		return nil, time.Time{}, fmt.Errorf("resource mTLS client unavailable")
	}
	client := *m.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 {
		client.Timeout = 30 * time.Second
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || response.Header.Get("Content-Range") != "" {
		return nil, time.Time{}, fmt.Errorf("resource download denied")
	}
	trusted, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("authenticated resource time missing")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if int64(len(raw)) > maximum {
		return nil, time.Time{}, fmt.Errorf("resource download budget exceeded")
	}
	return raw, trusted.UTC(), nil
}

func (m *UnifiedManager) releaseInput(root string, ref *resourcev1.UnifiedResourceRef, now time.Time) (map[string]any, error) {
	id := ref.GetIdentity()
	if !uDigest.MatchString(id.GetManifestDigest()) {
		return nil, fmt.Errorf("invalid manifest digest")
	}
	manifestPath := filepath.Join(root, "cache", strings.TrimPrefix(id.GetManifestDigest(), "sha256:"), "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	envelope, err := unifiedProto(ref.GetEnvelope())
	if err != nil {
		return nil, err
	}
	artifacts := map[string]any{}
	for _, artifact := range ref.GetArtifacts() {
		if !uDigest.MatchString(artifact.GetDigest()) {
			return nil, fmt.Errorf("invalid payload digest")
		}
		bytes, err := os.ReadFile(filepath.Join(root, "cache", strings.TrimPrefix(artifact.GetDigest(), "sha256:"), "payload.tgz"))
		if err != nil {
			return nil, err
		}
		artifacts[artifact.GetArtifactId()] = base64.StdEncoding.EncodeToString(bytes)
	}
	return map[string]any{"protocol": json.Number("2"), "envelope": envelope, "rawManifest": string(raw), "artifactBytes": artifacts, "expected": map[string]any{"tenantId": id.GetTenantId(), "packageId": id.GetPackageId(), "versionId": id.GetVersionId(), "kind": id.GetKind()}, "now": now.Format(time.RFC3339Nano)}, nil
}

func (m *UnifiedManager) validate(root string, ref *resourcev1.UnifiedResourceRef, list *resourcev1.ResourceRevocationList, now time.Time) (map[string]any, error) {
	input, err := m.releaseInput(root, ref, now)
	if err != nil {
		return nil, err
	}
	revocations, err := unifiedProto(list)
	if err != nil {
		return nil, err
	}
	if err := m.Protocol.Evaluate("revocation", map[string]any{"release": input, "list": revocations}); err != nil {
		return nil, err
	}
	envelope := ref.GetEnvelope()
	id := ref.GetIdentity()
	if id.GetKind() != "skill" || ref.GetReleaseSequence() != envelope.GetReleaseSequence() {
		return nil, fmt.Errorf("unsupported resource activation")
	}
	manifest, err := DecodeUnifiedJSON([]byte(us(input["rawManifest"])))
	if err != nil {
		return nil, err
	}
	if len(ua(manifest["artifacts"])) != 1 || len(ua(manifest["runtimes"])) != 0 || len(ref.GetArtifacts()) != 1 {
		return nil, fmt.Errorf("skill payload profile mismatch")
	}
	a := um(ua(manifest["artifacts"])[0])
	download := ref.GetArtifacts()[0]
	if a["role"] != "payload" || a["os"] != "any" || a["arch"] != "any" || a["id"] != download.GetArtifactId() || a["digest"] != download.GetDigest() || un(a["sizeBytes"]) != int64(download.GetSizeBytes()) {
		return nil, fmt.Errorf("skill payload locator mismatch")
	}
	end, e1 := time.Parse(time.RFC3339Nano, envelope.GetNotAfter())
	revEnd, e2 := time.Parse(time.RFC3339Nano, list.GetExpiresAt())
	lease := ref.GetLease()
	cutoff, e3 := time.Parse(time.RFC3339Nano, lease.GetExpiresAt())
	if revEnd.Before(end) {
		end = revEnd
	}
	if e1 != nil || e2 != nil || e3 != nil || !end.Equal(cutoff) || lease.GetEnvelopeNotAfter() != envelope.GetNotAfter() || lease.GetRevocationsExpiresAt() != list.GetExpiresAt() || lease.GetRevocationRevision() != list.GetRevision() {
		return nil, fmt.Errorf("resource lease binding mismatch")
	}
	return input, nil
}

func (m *UnifiedManager) stopped(ctx context.Context, id string) error {
	if err := m.Driver.Stop(ctx, id, 5*time.Second); err != nil {
		return err
	}
	if checker, ok := m.Driver.(interface {
		ResourceProcessStopped(context.Context, string) (bool, error)
	}); ok {
		stopped, err := checker.ResourceProcessStopped(ctx, id)
		if err != nil {
			return err
		}
		if !stopped {
			return fmt.Errorf("resource process stop not confirmed")
		}
		return nil
	}
	actual, err := m.Driver.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if actual.Running {
		return fmt.Errorf("resource process stop not confirmed")
	}
	return nil
}
func (m *UnifiedManager) isolate(ctx context.Context, s *unifiedNodeState, root, current, reason string) error {
	s.State, s.Reason = "stopping", reason
	if s.PendingIsolation == "" {
		s.PendingIsolation = unifiedNonce()
	}
	journalError := m.save(root, s)
	if err := m.stopped(ctx, s.Host); err != nil {
		s.State = "blocked"
		_ = m.save(root, s)
		return err
	}
	if journalError != nil {
		return journalError
	}
	s.State = "isolating"
	if err := m.save(root, s); err != nil {
		return err
	}
	quarantine := filepath.Join(root, "skills-active", "quarantine", s.PendingIsolation)
	if err := os.MkdirAll(filepath.Dir(quarantine), 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(current); err == nil {
		if err := storage.CheckNoSymlink(root, current); err != nil {
			return err
		}
		_, quarantined := os.Lstat(quarantine)
		if quarantined == nil {
			entries, err := os.ReadDir(current)
			if err != nil || len(entries) != 0 {
				return fmt.Errorf("ambiguous resource isolation recovery")
			}
		} else if !os.IsNotExist(quarantined) {
			return quarantined
		} else if err := unifiedRename(current, quarantine); err != nil {
			s.State = "blocked"
			_ = m.save(root, s)
			return err
		}
		if err := unifiedSyncDirectory(filepath.Dir(current)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(current, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("shared view is not empty")
	}
	receipt := map[string]any{"schemaVersion": 1, "tenantId": s.Tenant, "ownerId": s.Owner, "hostId": s.Host, "receiptId": s.PendingIsolation, "reason": reason, "processStopped": true, "viewCleared": true, "newSessionRequired": true, "recordedAt": m.now().Format(time.RFC3339Nano)}
	if err := unifiedAtomic(filepath.Join(root, "disable-receipts", s.PendingIsolation+".json"), receipt); err != nil {
		return err
	}
	s.State = "private_only"
	s.ObservedAt = ""
	s.PendingIsolation = ""
	s.PendingActivation = ""
	if err := m.save(root, s); err != nil {
		_ = m.Driver.Stop(ctx, s.Host, 5*time.Second)
		return err
	}
	paths, err := m.Layout.For(s.Host)
	if err != nil {
		return err
	}
	return m.publishSessionPolicy(filepath.Join(paths.Meta, "resource-session-policy.json"), s)
}

// Guard is called on every apply and health tick, independently of the plugin.
// Expiry, restart, identity changes and revocations stop the process before the
// fixed view is quarantined. Any uncertain recovery remains blocked.
func (m *UnifiedManager) Guard(ctx context.Context, spec *nodev1.InstanceSpec) (failure error) {
	defer func() {
		if failure != nil && m.Driver != nil {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = m.Driver.Stop(stopCtx, spec.GetInstanceId(), 5*time.Second)
		}
	}()
	defer m.lock(spec.GetInstanceId())()
	s, root, current, err := m.load(spec.GetInstanceId())
	if err != nil {
		return err
	}
	if s.State == "stopping" || s.State == "isolating" || s.State == "blocked" || s.PendingIsolation != "" || s.PendingActivation != "" {
		if err := m.isolate(ctx, s, root, current, "verification_failed"); err != nil {
			return err
		}
		return ErrUnifiedPrivateOnly
	}
	if s.State != "active" {
		return nil
	}
	reason := ""
	if s.Tenant != spec.GetTenantId() || s.Owner != spec.GetOwnerId() {
		reason = "identity_changed"
	} else if err := m.Protocol.Evaluate("clock", map[string]any{"clock": s.Clock, "bootId": m.BootID, "monotonicNowMillis": json.Number(fmt.Sprint(m.mono())), "wallNow": m.now().Format(time.RFC3339Nano), "leaseExpiresAt": s.LeaseExpiresAt}); err != nil {
		reason = err.Error()
		if reason != "lease_expired" && reason != "clock_rollback" {
			reason = "verification_failed"
		}
	}
	if reason == "" && spec.GetResourceSelectionRevision() > s.SelectionRevision {
		incoming := map[string]*resourcev1.UnifiedResourceRef{}
		for _, ref := range spec.GetUnifiedResources() {
			incoming[ref.GetIdentity().GetPackageId()] = ref
		}
		for _, record := range s.Installations {
			if record.DesiredState != "ENABLED" {
				continue
			}
			var prior resourcev1.UnifiedResourceRef
			if proto.Unmarshal(record.Ref, &prior) != nil {
				reason = "verification_failed"
				break
			}
			next := incoming[prior.GetIdentity().GetPackageId()]
			if next == nil {
				reason = "disabled"
				break
			}
			_, desired, err := ReadUnifiedIntent(next)
			if err != nil {
				reason = "verification_failed"
				break
			}
			if desired != "ENABLED" {
				reason = "disabled"
				break
			}
		}
	}
	effective := m.now()
	if reason == "" {
		advance := ut(s.Clock["maxObservedTime"]).Add(time.Duration(m.mono()-un(s.Clock["lastMonotonicMillis"])) * time.Millisecond)
		if advance.After(effective) {
			effective = advance
		}
		if ut(s.Clock["lastTrustedTime"]).After(effective) {
			effective = ut(s.Clock["lastTrustedTime"])
		}
		list := unifiedTenantRevocations(spec)
		document, listError := unifiedProto(list)
		if listError != nil || m.Protocol.Evaluate("revocation_list", map[string]any{"list": document, "tenantId": spec.GetTenantId(), "now": effective.Format(time.RFC3339Nano)}) != nil {
			reason = "verification_failed"
		} else {
			body := map[string]any{}
			for key, value := range document {
				if key != "signature" {
					body[key] = value
				}
			}
			canonical, err := unifiedCanonical(body)
			if err != nil {
				return err
			}
			digest := udigest(canonical)
			if list.GetRevision() < s.RevocationRevision || list.GetRevision() == s.RevocationRevision && s.RevocationDigest != "" && s.RevocationDigest != digest {
				reason = "verification_failed"
			} else {
				if s.RevocationRevision != list.GetRevision() || s.RevocationDigest != digest {
					s.RevocationRevision, s.RevocationDigest = list.GetRevision(), digest
					if err := m.save(root, s); err != nil {
						return err
					}
				}
			}
		}
		for _, installed := range s.Installations {
			if reason != "" {
				break
			}
			if installed.DesiredState != "ENABLED" {
				continue
			}
			var ref resourcev1.UnifiedResourceRef
			if err := proto.Unmarshal(installed.Ref, &ref); err != nil {
				reason = "verification_failed"
				break
			}
			input, inputErr := m.releaseInput(root, &ref, effective)
			revocations, listErr := unifiedProto(list)
			if inputErr != nil || listErr != nil {
				reason = "verification_failed"
				break
			}
			if err := m.Protocol.Evaluate("revocation", map[string]any{"release": input, "list": revocations}); err != nil {
				reason = "verification_failed"
				if err.Error() == "revoked" {
					reason = "revoked"
				}
				break
			}
		}
		if reason == "" {
			if err := m.renewCached(spec, s, root, list, effective); err != nil {
				reason = "verification_failed"
			}
		}
	}
	if reason != "" {
		if err := m.isolate(ctx, s, root, current, reason); err != nil {
			return err
		}
		return ErrUnifiedPrivateOnly
	}
	s.Clock["maxObservedTime"] = effective.Format(time.RFC3339Nano)
	s.Clock["lastMonotonicMillis"] = json.Number(fmt.Sprint(m.mono()))
	if err := m.save(root, s); err != nil {
		return err
	}
	paths, err := m.Layout.For(spec.GetInstanceId())
	if err != nil {
		return err
	}
	return m.publishSessionPolicy(filepath.Join(paths.Meta, "resource-session-policy.json"), s)
}

func unifiedTenantRevocations(spec *nodev1.InstanceSpec) *resourcev1.ResourceRevocationList {
	var found *resourcev1.ResourceRevocationList
	for _, list := range spec.GetResourceRevocations() {
		if list.GetTenantId() == spec.GetTenantId() && (found == nil || list.GetRevision() > found.GetRevision()) {
			found = list
		}
	}
	return found
}

// Prepare performs all long downloads outside the manager lock; the final
// critical section rechecks the immutable selection and durably journals the
// complete set before changing current. A restarted manager never reuses a grant.
func (m *UnifiedManager) Prepare(ctx context.Context, spec *nodev1.InstanceSpec) (binding string, failure error) {
	if len(spec.GetUnifiedResources()) > 128 {
		return "", fmt.Errorf("resource selection budget exceeded")
	}
	defer func() {
		if failure != nil {
			// A rejected new intent must never leave the old shared view usable.
			if err := m.Isolate(ctx, spec.GetInstanceId(), "verification_failed"); err != nil {
				if m.Driver != nil {
					stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					defer cancel()
					_ = m.Driver.Stop(stopCtx, spec.GetInstanceId(), 5*time.Second)
				}
				failure = errors.Join(failure, err)
				return
			}
			if current, err := m.privateFallback(spec); err == nil && !errors.Is(failure, errUnifiedSelection) {
				binding, failure = current, nil
			}
		}
	}()
	root, current, err := m.paths(spec.GetInstanceId())
	if err != nil {
		return "", err
	}
	var staging []string
	defer func() {
		for _, stage := range staging {
			if storage.CheckNoSymlink(root, stage) == nil {
				_ = os.RemoveAll(stage)
			}
		}
	}()
	type prepared struct {
		ref           *resourcev1.UnifiedResourceRef
		revision      uint64
		state         string
		input         map[string]any
		name          string
		directory     string
		authorization string
	}
	var ready []prepared
	trustedTime := time.Time{}
	deadline := time.Time{}
	seen := map[string]bool{}
	names := map[string]bool{}
	list := unifiedTenantRevocations(spec)
	for _, ref := range spec.GetUnifiedResources() {
		revision, state, err := ReadUnifiedIntent(ref)
		if err != nil {
			return "", err
		}
		id := ref.GetIdentity()
		if id.GetTenantId() != spec.GetTenantId() || !uUUID.MatchString(id.GetPackageId()) || !uUUID.MatchString(id.GetVersionId()) || id.GetKind() != "skill" || seen[id.GetPackageId()] {
			return "", fmt.Errorf("invalid resource selection")
		}
		seen[id.GetPackageId()] = true
		item := prepared{ref: ref, revision: revision, state: state, name: ref.GetEnvelope().GetStatement().GetName()}
		if !uName.MatchString(item.name) || len(item.name) > 64 || names[item.name] {
			return "", fmt.Errorf("invalid or duplicate skill name")
		}
		names[item.name] = true
		if state == "ENABLED" {
			raw, online, err := m.download(ctx, ref.GetManifestDownloadPath(), 256*1024)
			if err != nil {
				return "", err
			}
			if udigest(raw) != id.GetManifestDigest() {
				return "", fmt.Errorf("manifest raw byte digest mismatch")
			}
			manifestCache := filepath.Join(root, "cache", strings.TrimPrefix(id.GetManifestDigest(), "sha256:"), "manifest.json")
			if err := unifiedAtomicBytes(manifestCache, raw); err != nil {
				return "", err
			}
			if len(ref.GetArtifacts()) != 1 {
				return "", fmt.Errorf("one skill payload required")
			}
			artifact := ref.GetArtifacts()[0]
			payload, artifactTime, err := m.download(ctx, artifact.GetDownloadPath(), 5*1024*1024)
			if err != nil {
				return "", err
			}
			if !uDigest.MatchString(artifact.GetDigest()) || udigest(payload) != artifact.GetDigest() || uint64(len(payload)) != artifact.GetSizeBytes() {
				return "", fmt.Errorf("payload raw byte digest mismatch")
			}
			if artifactTime.After(online) {
				online = artifactTime
			}
			if online.After(trustedTime) {
				trustedTime = online
			}
			if err := unifiedAtomicBytes(filepath.Join(root, "cache", strings.TrimPrefix(artifact.GetDigest(), "sha256:"), "payload.tgz"), payload); err != nil {
				return "", err
			}
			checkTime := m.now()
			if online.After(checkTime) {
				checkTime = online
			}
			input, err := m.validate(root, ref, list, checkTime)
			if err != nil {
				return "", err
			}
			item.input = input
			leaseEnd, err := time.Parse(time.RFC3339Nano, ref.GetLease().GetExpiresAt())
			if err != nil {
				return "", err
			}
			if deadline.IsZero() || leaseEnd.Before(deadline) {
				deadline = leaseEnd
			}
			stage := filepath.Join(root, "staging", unifiedNonce())
			staging = append(staging, stage)
			if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
				return "", err
			}
			if err := ExtractUnifiedSkill(payload, stage); err != nil {
				return "", err
			}
			entry, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
			if err != nil {
				return "", err
			}
			text := strings.ReplaceAll(string(entry), "\r\n", "\n")
			end := strings.Index(text[4:], "\n---")
			var metadata struct{ Name, Description string }
			if end < 0 {
				return "", fmt.Errorf("invalid skill metadata")
			}
			if err := yaml.Unmarshal([]byte(text[4:4+end]), &metadata); err != nil || metadata.Name != item.name || metadata.Description == "" {
				return "", fmt.Errorf("skill entry identity mismatch")
			}
			item.directory = stage
		}
		ready = append(ready, item)
	}
	defer m.lock(spec.GetInstanceId())()
	s, _, _, err := m.load(spec.GetInstanceId())
	if err != nil {
		return "", err
	}
	if m.Specs != nil {
		latest, err := m.Specs.GetDesired(spec.GetInstanceId())
		if err != nil {
			return "", err
		}
		if !proto.Equal(latest, spec) {
			return "", fmt.Errorf("resource selection superseded")
		}
	}
	if err := m.lockSelection(spec, s); err != nil {
		return "", err
	}
	if s.PendingIsolation != "" || s.PendingActivation != "" || s.State == "stopping" || s.State == "isolating" || s.State == "blocked" {
		if err := m.isolate(ctx, s, root, current, "verification_failed"); err != nil {
			return "", err
		}
	}
	// Refuse rollback/replay even after uninstall or identity changes. Never delete watermarks.
	old := map[string]unifiedInstalled{}
	for _, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if proto.Unmarshal(record.Ref, &ref) != nil {
			return "", fmt.Errorf("invalid installation ledger")
		}
		if s.Tenant == spec.GetTenantId() && s.Owner == spec.GetOwnerId() {
			old[ref.GetIdentity().GetPackageId()] = record
		}
	}
	for itemIndex := range ready {
		item := &ready[itemIndex]
		key := spec.GetTenantId() + "/" + item.ref.GetIdentity().GetPackageId()
		installationKey := spec.GetTenantId() + "/" + spec.GetOwnerId() + "/" + item.ref.GetIdentity().GetPackageId()
		highRevision := s.InstallationRevisions[installationKey]
		if item.revision < highRevision {
			return "", fmt.Errorf("%w: installation revision rollback after collection", errUnifiedSelection)
		}
		if _, exists := old[item.ref.GetIdentity().GetPackageId()]; !exists && highRevision > 0 && item.revision == highRevision {
			return "", fmt.Errorf("%w: collected installation revision replay", errUnifiedSelection)
		}
		if previous, ok := old[item.ref.GetIdentity().GetPackageId()]; ok {
			if item.revision < previous.InstallationRevision {
				return "", fmt.Errorf("%w: installation revision rollback", errUnifiedSelection)
			}
			if item.revision == previous.InstallationRevision {
				var prior resourcev1.UnifiedResourceRef
				if proto.Unmarshal(previous.Ref, &prior) != nil || !proto.Equal(prior.GetIdentity(), item.ref.GetIdentity()) || prior.GetReleaseSequence() != item.ref.GetReleaseSequence() || previous.DesiredState != item.state {
					return "", fmt.Errorf("%w: installation revision conflict", errUnifiedSelection)
				}
			}
		}
		if item.state == "ENABLED" {
			if watermark, ok := s.Watermarks[key]; ok {
				if item.ref.GetReleaseSequence() < uint64(un(watermark["releaseSequence"])) {
					id, err := m.authorizeDowngrade(spec, s, item.ref, item.revision, old[item.ref.GetIdentity().GetPackageId()])
					if err != nil {
						return "", errors.Join(errUnifiedSelection, err)
					}
					item.authorization = id
				} else if err := m.Protocol.Evaluate("watermark", map[string]any{"release": item.input, "watermark": watermark}); err != nil {
					return "", err
				}
			}
		}
	}
	if s.Generation == spec.GetGeneration() && s.SelectionRevision == spec.GetResourceSelectionRevision() && s.Tenant == spec.GetTenantId() && s.Owner == spec.GetOwnerId() && s.State == "active" && len(s.Installations) == len(ready) {
		same := true
		for i, item := range ready {
			wire, err := proto.Marshal(item.ref)
			if err != nil {
				return "", err
			}
			if !bytes.Equal(s.Installations[i].Ref, wire) || s.Installations[i].InstallationRevision != item.revision || s.Installations[i].DesiredState != item.state {
				same = false
			}
		}
		if !same {
			return "", fmt.Errorf("same resource generation changed bytes")
		}
		return current, nil
	}
	if list != nil {
		doc, err := unifiedProto(list)
		if err != nil {
			return "", err
		}
		delete(doc, "signature")
		body, err := unifiedCanonical(doc)
		if err != nil {
			return "", err
		}
		digest := udigest(body)
		if list.GetRevision() < s.RevocationRevision || list.GetRevision() == s.RevocationRevision && s.RevocationDigest != "" && s.RevocationDigest != digest {
			return "", fmt.Errorf("revocation list rollback/conflict")
		}
		s.RevocationRevision, s.RevocationDigest = list.GetRevision(), digest
	}
	if err := m.stopped(ctx, spec.GetInstanceId()); err != nil {
		return "", err
	}
	if err := m.cleanupQuarantines(root, s.Host); err != nil {
		return "", err
	}
	if s.State == "active" || s.Tenant != "" && s.Tenant != spec.GetTenantId() || s.Owner != "" && s.Owner != spec.GetOwnerId() {
		if err := m.isolate(ctx, s, root, current, "identity_changed"); err != nil {
			return "", err
		}
	}
	if err := m.cleanupQuarantines(root, s.Host); err != nil {
		return "", err
	}
	transaction := unifiedNonce()
	next := filepath.Join(root, "skills-active", "prepared-"+transaction)
	if err := os.MkdirAll(next, 0755); err != nil {
		return "", err
	}
	installations := []unifiedInstalled{}
	hasEnabled := false
	for _, item := range ready {
		wire, err := proto.Marshal(item.ref)
		if err != nil {
			return "", err
		}
		record := unifiedInstalled{Ref: wire, InstallationRevision: item.revision, DesiredState: item.state, Name: item.name, State: strings.ToLower(item.state)}
		record.AuthorizationID = item.authorization
		if item.state == "ENABLED" {
			if err := unifiedRename(item.directory, filepath.Join(next, item.name)); err != nil {
				return "", err
			}
			record.State = "applying"
			s.HasShared = true
			hasEnabled = true
			key := spec.GetTenantId() + "/" + item.ref.GetIdentity().GetPackageId()
			prior := s.Watermarks[key]
			if prior == nil || un(prior["releaseSequence"]) < int64(item.ref.GetReleaseSequence()) {
				s.Watermarks[key] = map[string]any{"tenantId": spec.GetTenantId(), "packageId": item.ref.GetIdentity().GetPackageId(), "releaseSequence": json.Number(fmt.Sprint(item.ref.GetReleaseSequence())), "versionId": item.ref.GetIdentity().GetVersionId(), "verifiedDigestAliases": []any{item.ref.GetIdentity().GetManifestDigest()}}
			}
			if item.authorization != "" {
				found := false
				for _, id := range s.UsedAuthorizations {
					found = found || id == item.authorization
				}
				if !found {
					s.UsedAuthorizations = append(s.UsedAuthorizations, item.authorization)
				}
			}
		}
		installations = append(installations, record)
		s.InstallationRevisions[spec.GetTenantId()+"/"+spec.GetOwnerId()+"/"+item.ref.GetIdentity().GetPackageId()] = item.revision
	}
	s.Tenant, s.Owner, s.Host = spec.GetTenantId(), spec.GetOwnerId(), spec.GetInstanceId()
	s.SelectionRevision, s.Generation = spec.GetResourceSelectionRevision(), spec.GetGeneration()
	s.Installations = installations
	s.HasShared = hasEnabled
	s.ObservedAt = ""
	s.PendingActivation = transaction
	s.State = "verifying"
	if err := m.save(root, s); err != nil {
		return "", err
	}
	if _, err := os.Lstat(current); err == nil {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) != 0 {
			return "", fmt.Errorf("activation requires an empty fixed view")
		}
		if err := os.Remove(current); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := unifiedRename(next, current); err != nil {
		return "", err
	}
	if err := unifiedSyncDirectory(filepath.Dir(current)); err != nil {
		return "", err
	}
	s.PendingActivation = ""
	s.State = "private_only"
	s.Reason = "startup"
	if !deadline.IsZero() {
		effective := m.now()
		if trustedTime.After(effective) {
			effective = trustedTime
		}
		if s.Clock != nil {
			if prior, err := time.Parse(time.RFC3339Nano, fmt.Sprint(s.Clock["maxObservedTime"])); err == nil && prior.After(effective) {
				effective = prior
			}
		}
		if !deadline.After(effective) {
			return "", fmt.Errorf("resource expired before activation")
		}
		s.LeaseExpiresAt = deadline.Format(time.RFC3339Nano)
		s.Clock = map[string]any{"bootId": m.BootID, "maxObservedTime": effective.Format(time.RFC3339Nano), "lastTrustedTime": trustedTime.Format(time.RFC3339Nano), "lastTrustedTimeSource": "authenticated_server", "lastMonotonicMillis": json.Number(fmt.Sprint(m.mono())), "leaseDeadlineMonotonicMillis": json.Number(fmt.Sprint(m.mono() + deadline.Sub(effective).Milliseconds()))}
		s.State = "active"
		s.Reason = "startup"
	}
	if err := m.save(root, s); err != nil {
		return "", err
	}
	if err := m.writeInstallationReceipts(root, s); err != nil {
		return "", err
	}
	if err := m.collectResourceCache(root, s); err != nil {
		return "", err
	}
	paths, err := m.Layout.For(spec.GetInstanceId())
	if err != nil {
		return "", err
	}
	if err := m.publishSessionPolicy(filepath.Join(paths.Meta, "resource-session-policy.json"), s); err != nil {
		return "", err
	}
	return current, nil
}

// Isolate withdraws shared content before returning any failed observation or intent.
func (m *UnifiedManager) Isolate(ctx context.Context, id, reason string) error {
	defer m.lock(id)()
	s, root, current, err := m.load(id)
	if err != nil {
		return err
	}
	if s.Tenant == "" && len(s.Installations) == 0 {
		return nil
	}
	if s.State == "private_only" && s.PendingIsolation == "" && s.PendingActivation == "" {
		return nil
	}
	return m.isolate(ctx, s, root, current, reason)
}

func (m *UnifiedManager) Observe(ctx context.Context, id string) (reports []*resourcev1.UnifiedInstallationReport, guardian *resourcev1.UnifiedGuardianState, failure error) {
	defer m.lock(id)()
	defer func() {
		if failure != nil && m.Driver != nil {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := m.Driver.Stop(stopCtx, id, 5*time.Second); err != nil {
				failure = errors.Join(failure, err)
			}
		}
	}()
	s, root, current, err := m.load(id)
	if err != nil {
		return nil, nil, err
	}
	guardian = unifiedGuardian(s)
	if len(s.Installations) == 0 && !s.HasShared {
		return reports, guardian, nil
	}
	defer func() {
		if failure != nil {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := m.isolate(stopCtx, s, root, current, "observation_mismatch"); err != nil {
				failure = errors.Join(failure, err)
			}
			reports = nil
			guardian = unifiedGuardian(s)
		}
	}()
	if verifier, ok := m.Driver.(interface {
		VerifyResourceView(context.Context, string) error
	}); ok {
		if err := verifier.VerifyResourceView(ctx, id); err != nil {
			return nil, guardian, err
		}
	}
	endpoint, err := m.Driver.Endpoint(id)
	if err != nil {
		return nil, guardian, err
	}
	paths, err := m.Layout.For(id)
	if err != nil {
		return nil, guardian, err
	}
	var policy map[string]any
	policyError := unifiedRead(filepath.Join(paths.Meta, "resource-session-policy.json"), &policy)
	actualPolicy, _ := json.Marshal(policy)
	expectedPolicy, _ := json.Marshal(m.sessionPolicy(s))
	if policyError != nil || !bytes.Equal(actualPolicy, expectedPolicy) {
		return nil, guardian, fmt.Errorf("node session policy does not match guardian")
	}
	if err := storage.CheckNoSymlink(root, current); err != nil {
		return nil, guardian, err
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return nil, guardian, err
	}
	expectedNames := map[string]bool{}
	if s.State == "active" {
		for _, record := range s.Installations {
			if record.DesiredState == "ENABLED" {
				expectedNames[record.Name] = true
			}
		}
	}
	if len(entries) != len(expectedNames) {
		return nil, guardian, fmt.Errorf("node shared view does not match selected content")
	}
	for _, entry := range entries {
		if !entry.IsDir() || !expectedNames[entry.Name()] {
			return nil, guardian, fmt.Errorf("unexpected node shared content")
		}
	}
	token, err := os.ReadFile(filepath.Join(paths.Meta, "instance.token"))
	if err != nil {
		return nil, guardian, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint.URL, "/")+"/auriga-node-resource-catalog", nil)
	if err != nil {
		return nil, guardian, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, guardian, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, guardian, fmt.Errorf("native skill catalog unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, guardian, err
	}
	var catalog struct {
		PrivateOnly bool                                                     `json:"privateOnly"`
		Skills      []struct{ Name, Provider, Path, Content, Preset string } `json:"skills"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, guardian, err
	}
	if catalog.PrivateOnly != (s.HasShared && s.State == "private_only") {
		return nil, guardian, fmt.Errorf("private-only session gate unavailable")
	}
	for _, skill := range catalog.Skills {
		if skill.Provider == "auriga-shared" && !expectedNames[skill.Name] {
			return nil, guardian, fmt.Errorf("instance reported unselected shared content")
		}
	}
	changed := s.ObservedAt == ""
	for i, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if err := proto.Unmarshal(record.Ref, &ref); err != nil {
			return nil, guardian, err
		}
		revision, desired, err := ReadUnifiedIntent(&ref)
		if err != nil || revision != record.InstallationRevision || desired != record.DesiredState {
			return nil, guardian, fmt.Errorf("installation ledger does not match generated intent")
		}
		state := "overridden"
		if record.DesiredState == "ENABLED" && s.State == "private_only" {
			state = "expired"
			if s.Reason == "revoked" {
				state = "revoked"
			}
		}
		foundShared := false
		confirmed := map[string]bool{}
		for _, skill := range catalog.Skills {
			if skill.Name != record.Name {
				continue
			}
			expected := filepath.ToSlash(filepath.Join("/home/dsh/.dsh/auriga/skills-active/current", record.Name, "SKILL.md"))
			if skill.Provider == "auriga-shared" || strings.ReplaceAll(skill.Path, "\\", "/") == expected {
				foundShared = true
			}
			if record.DesiredState == "ENABLED" && skill.Provider == "auriga-shared" && strings.ReplaceAll(skill.Path, "\\", "/") == expected && s.State == "active" {
				content, err := os.ReadFile(filepath.Join(root, "skills-active", "current", record.Name, "SKILL.md"))
				if err == nil && skill.Content == unifiedSkillBody(content) {
					confirmed[skill.Preset] = true
				}
			}
		}
		if confirmed["cordis"] && confirmed["standard"] && confirmed["ptc"] {
			state = "applied"
		}
		if record.DesiredState == "ENABLED" && s.State == "active" && state != "applied" {
			return nil, guardian, fmt.Errorf("native catalog disagrees with node shared view")
		}
		if s.State == "private_only" && foundShared {
			return nil, guardian, fmt.Errorf("isolated shared content still visible")
		}
		if record.DesiredState != "ENABLED" && !foundShared {
			if _, err := os.Lstat(filepath.Join(root, "skills-active", "current", record.Name)); os.IsNotExist(err) {
				state = strings.ToLower(record.DesiredState)
			}
		}
		changed = changed || s.Installations[i].State != state
		s.Installations[i].State = state
		reports = append(reports, &resourcev1.UnifiedInstallationReport{ClientId: id, Identity: ref.GetIdentity(), InstallationRevision: record.InstallationRevision, SelectionRevision: s.SelectionRevision, Generation: uint64(s.Generation), ObservedGeneration: uint64(s.Generation), State: state, ReportSource: "node", ObservedAt: m.now().Format(time.RFC3339Nano)})
	}
	if changed {
		s.ObservedAt = m.now().Format(time.RFC3339Nano)
		if err := m.save(root, s); err != nil {
			return nil, guardian, err
		}
		if err := m.writeInstallationReceipts(root, s); err != nil {
			return nil, guardian, err
		}
	}
	return reports, guardian, nil
}

func unifiedGuardian(s *unifiedNodeState) *resourcev1.UnifiedGuardianState {
	return &resourcev1.UnifiedGuardianState{TenantId: s.Tenant, OwnerId: s.Owner, HostId: s.Host, SelectionRevision: s.SelectionRevision, Generation: uint64(s.Generation), State: s.State, Reason: s.Reason, NewSessionRequired: s.HasShared && s.State == "private_only", ViewCleared: s.State == "private_only"}
}
