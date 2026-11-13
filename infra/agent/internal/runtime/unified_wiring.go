package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"encoding/json"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// NewUnifiedManager reads only protected installation settings. Missing settings
// leave private instances usable and shared activation closed. ApplySpec never
// supplies a signing root or an origin.
func NewUnifiedManager(layout *storage.Layout, driver Driver, specs SpecSource, agentDirectory string) *UnifiedManager {
	started := time.Now()
	m := &UnifiedManager{Layout: layout, Driver: driver, Specs: specs, BootID: unifiedNonce(), Mono: func() int64 { return time.Since(started).Milliseconds() }, Intent: ReadUnifiedIntent}
	var settings struct {
		SchemaVersion int               `json:"schemaVersion"`
		Origin        string            `json:"origin"`
		Keys          []json.RawMessage `json:"keys"`
	}
	if err := unifiedRead(filepath.Join(agentDirectory, "resource-trust.json"), &settings); err != nil || settings.SchemaVersion != 1 {
		return m
	}
	purposes := map[string]string{}
	var keys []*resourcev1.ResourceTrustKey
	for _, raw := range settings.Keys {
		key := new(resourcev1.ResourceTrustKey)
		if err := protojson.Unmarshal(raw, key); err != nil {
			return m
		}
		if key.GetPurpose() != "release" && key.GetPurpose() != "revocation" && key.GetPurpose() != "downgrade" {
			return m
		}
		material := string(key.GetPublicKey())
		if prior, exists := purposes[material]; exists && prior != key.GetPurpose() {
			return m
		}
		purposes[material] = key.GetPurpose()
		keys = append(keys, key)
	}
	material, err := identity.Load(agentDirectory)
	if err != nil {
		return m
	}
	der, err := x509.MarshalPKCS8PrivateKey(material.PrivateKey)
	if err != nil {
		return m
	}
	certificate, err := tls.X509KeyPair(material.CertificatePEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		return m
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(material.CAPEM) {
		return m
	}
	m.Origin = settings.Origin
	m.Protocol.Keys = keys
	m.Client = &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}}, ResponseHeaderTimeout: 10 * time.Second}, Timeout: 30 * time.Second}
	return m
}

func (m *UnifiedManager) AuthorizeStart(ctx context.Context, id string) error {
	if m.Specs == nil {
		return fmt.Errorf("trusted desired source missing")
	}
	spec, err := m.Specs.GetDesired(id)
	if err != nil || spec == nil {
		return fmt.Errorf("trusted desired resource spec missing")
	}
	if err := m.Guard(ctx, spec); err != nil {
		return err
	}
	defer m.lock(id)()
	s, _, current, err := m.load(id)
	if err != nil {
		return err
	}
	// Existing executions that have never used shared resources have no D ledger
	// or observer row. Preserve their ordinary signed-runtime recovery path.
	if len(spec.GetUnifiedResources()) == 0 && !s.HasShared && s.Tenant == "" {
		return nil
	}
	if s.PendingActivation != "" || s.PendingIsolation != "" || s.State == "blocked" || s.State == "verifying" {
		return fmt.Errorf("resource recovery incomplete")
	}
	if s.State == "private_only" {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) != 0 {
			return fmt.Errorf("private-only view is not empty")
		}
	}
	paths, err := m.Layout.For(id)
	if err != nil {
		return err
	}
	file := filepath.Join(paths.Meta, "resource-session-policy.json")
	return m.publishSessionPolicy(file, s)
}

func (m *UnifiedManager) publishSessionPolicy(file string, s *unifiedNodeState) error {
	raw, err := json.Marshal(m.sessionPolicy(s))
	if err != nil {
		return err
	}
	return unifiedAtomicMode(file, raw, 0644)
}

func (m *UnifiedManager) sessionPolicy(s *unifiedNodeState) map[string]any {
	policy := map[string]any{"schemaVersion": 1, "newSessionRequired": s.HasShared && s.State == "private_only", "leaseExpiresAt": "", "trustedTime": ""}
	if s.State == "active" {
		policy["leaseExpiresAt"] = s.LeaseExpiresAt
		policy["trustedTime"] = s.Clock["maxObservedTime"]
	}
	return policy
}

func trustedImageCommand(image map[string]any) ([]string, error) {
	config, _ := image["Config"].(map[string]any)
	var result []string
	for _, field := range []string{"Entrypoint", "Cmd"} {
		switch value := config[field].(type) {
		case []any:
			for _, arg := range value {
				word, ok := arg.(string)
				if !ok {
					return nil, fmt.Errorf("invalid trusted image command")
				}
				result = append(result, word)
			}
		case []string:
			result = append(result, value...)
		case nil:
		default:
			return nil, fmt.Errorf("invalid trusted image command")
		}
	}
	if len(result) == 0 || result[0] == "" {
		return nil, fmt.Errorf("trusted image command missing")
	}
	return result, nil
}

func (m *UnifiedManager) privateFallback(spec *nodev1.InstanceSpec) (string, error) {
	defer m.lock(spec.GetInstanceId())()
	s, root, current, err := m.load(spec.GetInstanceId())
	if err != nil {
		return "", err
	}
	if !s.HasShared || s.State != "private_only" || s.PendingActivation != "" || s.PendingIsolation != "" || s.Tenant != spec.GetTenantId() || s.Owner != spec.GetOwnerId() {
		return "", fmt.Errorf("private recovery not authorized")
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		return "", fmt.Errorf("private recovery view not empty")
	}
	if m.Specs != nil {
		latest, err := m.Specs.GetDesired(spec.GetInstanceId())
		if err != nil || !proto.Equal(latest, spec) {
			return "", fmt.Errorf("private recovery superseded")
		}
	}
	s.Generation, s.SelectionRevision = spec.GetGeneration(), spec.GetResourceSelectionRevision()
	if err := m.save(root, s); err != nil {
		return "", err
	}
	return current, nil
}

func (m *UnifiedManager) Status(id string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	defer m.lock(id)()
	s, _, _, err := m.load(id)
	if err != nil {
		return nil, nil, err
	}
	if s.Tenant == "" {
		return nil, nil, nil
	}
	state := &resourcev1.UnifiedGuardianState{TenantId: s.Tenant, OwnerId: s.Owner, HostId: s.Host, Generation: uint64(s.Generation), SelectionRevision: s.SelectionRevision, State: s.State, Reason: s.Reason, NewSessionRequired: s.HasShared && s.State == "private_only", ProcessStopped: s.State == "private_only", ViewCleared: s.State == "private_only"}
	for _, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if proto.Unmarshal(record.Ref, &ref) == nil && ref.GetLease().GetExpiresAt() == s.LeaseExpiresAt {
			state.Lease = ref.GetLease()
			break
		}
	}
	return nil, state, nil
}

// ResourceObservations supplies authenticated node evidence to the reconciler.
func (p *Preparing) ResourceObservations(ctx context.Context, id string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	if p.Unified == nil {
		return nil, nil, nil
	}
	return p.Unified.Observe(ctx, id)
}
func (p *Preparing) ResourceState(id string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	if p.Unified == nil {
		return nil, nil, nil
	}
	return p.Unified.Status(id)
}
func (p *Preparing) GuardResources(ctx context.Context, spec *nodev1.InstanceSpec) error {
	if p.Unified == nil {
		if len(spec.GetUnifiedResources()) > 0 {
			return fmt.Errorf("trusted unified guardian unavailable")
		}
		return nil
	}
	return p.Unified.Guard(ctx, spec)
}

// ResourceProcessStopped checks actual Engine state without requiring a still
// valid runtime release signature, so revoked runtime keys cannot prevent cleanup.
func (d *Docker) ResourceProcessStopped(ctx context.Context, id string) (bool, error) {
	name, err := d.cleanupContainer(ctx, id)
	if err != nil {
		return false, err
	}
	if name == "" {
		return true, nil
	}
	inspection, err := d.Client.ContainerInspect(ctx, name)
	if err != nil {
		return false, err
	}
	if inspection == nil {
		return true, nil
	}
	state, _ := inspection["State"].(map[string]any)
	running, ok := state["Running"].(bool)
	if !ok {
		return false, fmt.Errorf("unknown Engine process state")
	}
	return !running, nil
}

func (d *Docker) verifyUnifiedMount(inspection map[string]any, id string) error {
	expected := filepath.Join(d.DataRoot, "instances", id, "meta", "shared-resources", "skills-active", "current")
	mounts, _ := inspection["Mounts"].([]any)
	count := 0
	for _, value := range mounts {
		mount, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if mount["Destination"] != "/home/dsh/.dsh/auriga/skills-active/current" {
			continue
		}
		source, ok := mount["Source"].(string)
		if !ok || filepath.Clean(source) != filepath.Clean(expected) || mount["Type"] != "bind" || mount["RW"] != false {
			return fmt.Errorf("shared skill view is not the trusted read-only mount")
		}
		count++
	}
	if count != 1 {
		return fmt.Errorf("missing or duplicate shared skill mount")
	}
	config, _ := inspection["Config"].(map[string]any)
	entry, _ := json.Marshal(config["Entrypoint"])
	expectedEntry, _ := json.Marshal([]string{"node", renderer.ContainerResourceSupervisor})
	var args []string
	command, _ := json.Marshal(config["Cmd"])
	if err := json.Unmarshal(command, &args); err != nil || len(args) < 2 || args[0] != renderer.ContainerResourcePolicy || string(entry) != string(expectedEntry) {
		return fmt.Errorf("independent resource supervisor missing")
	}
	host, _ := inspection["HostConfig"].(map[string]any)
	restart, _ := host["RestartPolicy"].(map[string]any)
	if restart["Name"] != "no" {
		return fmt.Errorf("resource supervisor automatic restart denied")
	}
	return nil
}
