package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"sync"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/releaseproof"
	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// ReleaseActivation is installation-owned wiring shared by reconciler and Docker.
// Policy is read again at every authorization, never taken from a remote spec.
type ReleaseActivation struct {
	Artifacts *artifacts.Store
	Store     ReleaseState
	ProofsDir string
	Policy    func() policy.Policy
}

// ReleaseState is the local state boundary. Production always supplies the
// actual state.Store; tests can fail one persistence operation while retaining
// the real trust store, partition lock and bbolt high-water commit.
type ReleaseState interface {
	GetReleaseTrust() (releasetrust.Ring, error)
	WithReleaseExecution(context.Context, state.ReleaseCandidate, func(context.Context) error, func(context.Context, *state.ReleaseExecution) error) error
	GetExecution(string) (*state.ExecutionRecord, error)
	PutExecution(state.ExecutionRecord) error
	DeleteExecution(string, string) error
	CheckReleaseExecution(state.ReleaseCandidate) error
}

func (d *Docker) ReleaseCoordinator() *ReleaseActivation { return d.Activation }

type activationKey struct{}
type activationScope struct {
	coordinator *ReleaseActivation
	spec        *nodev1.InstanceSpec
	candidate   state.ReleaseCandidate
	image       releaseverify.VerifiedImage
	execution   *state.ReleaseExecution
	record      *state.ExecutionRecord
	mu          sync.Mutex
	active      bool
}

func (a *ReleaseActivation) verify(dsh *nodev1.DshRuntime) (releaseverify.VerifiedImage, state.ReleaseCandidate, error) {
	var zero releaseverify.VerifiedImage
	if a == nil || a.Store == nil || a.Policy == nil || a.ProofsDir == "" {
		return zero, state.ReleaseCandidate{}, fmt.Errorf("runtime activation is not configured")
	}
	ring, err := a.Store.GetReleaseTrust()
	if err != nil {
		return zero, state.ReleaseCandidate{}, err
	}
	p := a.Policy()
	v, err := releaseverify.Verify(dsh, ring, releaseverify.Target{OS: goruntime.GOOS, Arch: goruntime.GOARCH, Driver: "docker", AllowImage: p.AllowImage})
	if err != nil {
		return zero, state.ReleaseCandidate{}, err
	}
	m, c, err := releaseproof.Read(a.ProofsDir, v.ImageDigest())
	if err != nil {
		return zero, state.ReleaseCandidate{}, err
	}
	image, err := releaseverify.VerifyOCI(v, ring, m, c)
	if err != nil {
		return zero, state.ReleaseCandidate{}, err
	}
	part := v.Partition()
	return image, state.ReleaseCandidate{Partition: state.ReleasePartition{Purpose: part.Purpose, Name: part.Name, OS: part.OS, Arch: part.Arch}, Sequence: v.Sequence(), BodyDigest: v.BodyDigest()}, nil
}

// Activate fixes the entire desired snapshot and synchronously joins Ensure,
// its actual-record save and Start inside one continuous partition qualification.
func (a *ReleaseActivation) Activate(ctx context.Context, spec *nodev1.InstanceSpec, run func(context.Context) error) error {
	if spec == nil || run == nil {
		return fmt.Errorf("activation requires fixed spec and callback")
	}
	fixed := proto.Clone(spec).(*nodev1.InstanceSpec)
	return a.coordinate(ctx, fixed, nil, run)
}

// Recover authorizes the ORIGINAL actual release, not the latest desired. No
// execution record means no attempt to guess trust from container labels.
func (a *ReleaseActivation) Recover(ctx context.Context, id string, generation int64, run func(context.Context) error) error {
	if a == nil || a.Store == nil || run == nil {
		return fmt.Errorf("recovery is not configured")
	}
	record, err := a.Store.GetExecution(id)
	if err != nil {
		return err
	}
	if record == nil || record.Generation != generation {
		return fmt.Errorf("missing matching actual runtime execution")
	}
	spec := &nodev1.InstanceSpec{InstanceId: id, Generation: record.Generation, Dsh: record.DSH}
	return a.coordinate(ctx, spec, record, run)
}

func (a *ReleaseActivation) coordinate(ctx context.Context, spec *nodev1.InstanceSpec, record *state.ExecutionRecord, run func(context.Context) error) error {
	_, candidate, err := a.verify(spec.Dsh)
	if err != nil {
		return err
	}
	scope := &activationScope{coordinator: a, spec: spec, candidate: candidate, record: record, active: true}
	defer func() { scope.mu.Lock(); scope.active = false; scope.mu.Unlock() }()
	return a.Store.WithReleaseExecution(ctx, candidate, func(ctx context.Context) error {
		if err := scope.recheck(); err != nil {
			return err
		}
		if spec.Dsh.Digest != "" {
			if a.Artifacts == nil {
				return fmt.Errorf("runtime archive store is required")
			}
			if _, err := a.Artifacts.Ensure(ctx, spec.InstanceId, spec.Dsh.Url, spec.Dsh.Digest, 0); err != nil {
				return err
			}
		}
		return scope.recheck()
	}, func(ctx context.Context, e *state.ReleaseExecution) error {
		scope.execution = e
		return run(context.WithValue(ctx, activationKey{}, scope))
	})
}

func (s *activationScope) recheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return fmt.Errorf("expired runtime activation scope")
	}
	image, candidate, err := s.coordinator.verify(s.spec.Dsh)
	if err != nil {
		return err
	}
	if candidate != s.candidate {
		return fmt.Errorf("runtime release changed during activation")
	}
	if s.record != nil && (s.record.Candidate != candidate || s.record.ProofDigest != image.ManifestDigest() || s.record.ConfigID != image.ConfigID()) {
		return fmt.Errorf("actual execution does not match signed evidence")
	}
	s.image = image
	return nil
}

func requireActivation(ctx context.Context, id string) (*activationScope, error) {
	s, ok := ctx.Value(activationKey{}).(*activationScope)
	if !ok || s == nil || s.spec.InstanceId != id || s.execution == nil {
		return nil, fmt.Errorf("docker requires coordinated runtime release activation")
	}
	if err := s.recheck(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetEndpointRevoker wires the installation's ingress/observer publication.
// Revocation runs outside the driver mutex and never stops a process.
func (d *Docker) SetEndpointRevoker(revoke func(string)) {
	d.mu.Lock()
	d.endpointRevoker = revoke
	d.mu.Unlock()
}

func (d *Docker) revoke(id string) {
	d.mu.Lock()
	delete(d.endpoints, id)
	revoke := d.endpointRevoker
	d.mu.Unlock()
	if revoke != nil {
		revoke(id)
	}
}

func (d *Docker) actualRecord(id string) (*state.ExecutionRecord, error) {
	if d.Activation == nil || d.Activation.Store == nil {
		return nil, fmt.Errorf("docker activation is not configured")
	}
	r, err := d.Activation.Store.GetExecution(id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("missing actual runtime execution")
	}
	return r, nil
}

func (d *Docker) observationRecord(id string) (*state.ExecutionRecord, error) {
	r, err := d.actualRecord(id)
	if err != nil {
		d.revoke(id)
		return nil, err
	}
	image, candidate, err := d.Activation.verify(r.DSH)
	if err == nil && (candidate != r.Candidate || image.ConfigID() != r.ConfigID || image.ManifestDigest() != r.ProofDigest) {
		err = fmt.Errorf("actual execution evidence mismatch")
	}
	if err == nil {
		err = d.Activation.Store.CheckReleaseExecution(candidate)
	}
	if err != nil {
		d.revoke(id)
		return nil, err
	}
	return r, nil
}

func (d *Docker) verifyContainer(ctx context.Context, r *state.ExecutionRecord) (map[string]any, error) {
	insp, err := d.Client.ContainerInspect(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	cfg, _ := insp["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	if insp == nil || insp["Id"] != r.ContainerID || insp["Image"] != r.ConfigID || labels[labelManaged] != "true" || labels[labelInstance] != r.InstanceID || labels[labelGeneration] != fmt.Sprint(r.Generation) {
		return nil, fmt.Errorf("docker actual container identity mismatch")
	}
	return insp, nil
}

// Cleanup does not depend on release signature validity; it still binds the
// actual object. An unrecorded old object is usable only for owned cleanup.
func (d *Docker) cleanupContainer(ctx context.Context, id string) (string, error) {
	if d.Activation != nil && d.Activation.Store != nil {
		r, err := d.Activation.Store.GetExecution(id)
		if err != nil {
			return "", err
		}
		if r != nil {
			insp, err := d.Client.ContainerInspect(ctx, r.ContainerID)
			if err != nil {
				return "", err
			}
			if insp == nil {
				return "", nil
			}
			if _, err := d.verifyContainer(ctx, r); err != nil {
				return "", err
			}
			return r.ContainerID, nil
		}
	}
	insp, err := d.Client.ContainerInspect(ctx, containerName(id))
	if err != nil || insp == nil {
		return "", err
	}
	cfg, _ := insp["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	actual, _ := insp["Id"].(string)
	if !containerIDPattern.MatchString(actual) || labels[labelManaged] != "true" || labels[labelInstance] != id {
		return "", fmt.Errorf("docker cleanup container ownership mismatch")
	}
	return actual, nil
}
