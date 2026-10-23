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
	InstanceID string
	Running    bool
	Ready      bool
	Generation int64
	Error      string
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
