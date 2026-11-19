// Package runtime defines the instance driver interface and implementations.
package runtime

import (
	"context"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"time"
)

// Caps describes driver capabilities.
type Caps struct {
	NetworkIsolation bool
	DiskQuota        bool
	ReadOnlyRoot     bool
}

// Spec is the subset of InstanceSpec the driver needs.
type Spec struct {
	// Full is the fixed snapshot supplied by the reconciler for this attempt.
	Full       *nodev1.InstanceSpec
	InstanceID string
	Generation int64
	Desired    string // running | stopped

	// Docker / process fields (filled by Preparing driver or tests).
	Image      string // must be repo@sha256:…
	CPUMillis  int64
	MemoryMB   int64
	PIDs       int64
	HostUID    int
	HomeBind   string // host path mounted at /home/dsh
	MetaBind   string // host meta path mounted read-only at /run/auriga
	SharedBind string // node-owned shared skill view, mounted read-only
	ListenPort uint32
	Env        map[string]string
	Labels     map[string]string
}

// Rendered is config render output.
type Rendered struct {
	Digest string
}

// Status is the observed runtime status of one instance.
type Status struct {
	InstanceID  string
	ContainerID string
	Running     bool
	Ready       bool
	Generation  int64
	Error       string
	// RecheckReady, when provided by a read-only inspection, checks the
	// inspected endpoint against current in-memory proof and clocks.
	RecheckReady func() bool
}

type readOnlyInspectionKey struct{}

// InspectReadOnly checks the current proof without withdrawing it. A queued
// message may refer to an older execution than the one being inspected.
func InspectReadOnly(ctx context.Context, driver Driver, id string) (Status, error) {
	return driver.Inspect(context.WithValue(ctx, readOnlyInspectionKey{}, struct{}{}), id)
}

// StillReady checks an inspection again after other work has delayed its use.
func (s Status) StillReady() bool {
	return s.Ready && (s.RecheckReady == nil || s.RecheckReady())
}

// HealthChecker revalidates the current execution, control network and public
// health endpoint without starting a process or replacing its configuration.
type HealthChecker interface {
	CheckHealth(ctx context.Context, id string) (Status, error)
}

// Usage is resource usage for one instance.
type Usage struct {
	CPUMillis int64
	MemoryMB  int64
	DiskMB    int64
	PIDs      int64
}

// Endpoint is the local HTTP address for ingress.
type Endpoint struct {
	URL string
}

// Driver is the unified runtime interface (node-agent.zh.md §5.1).
type Driver interface {
	Name() string
	Capabilities() Caps
	Ensure(ctx context.Context, s *Spec, r *Rendered) error
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, grace time.Duration) error
	Remove(ctx context.Context, id string) error
	Inspect(ctx context.Context, id string) (Status, error)
	Stats(ctx context.Context, id string) (Usage, error)
	Endpoint(id string) (Endpoint, error)
	List(ctx context.Context) ([]Status, error)
}
