// Package runtime defines the instance driver interface and fake implementation.
package runtime

import (
	"context"
	"time"
)

// Caps describes driver capabilities.
type Caps struct {
	NetworkIsolation bool
	DiskQuota        bool
	ReadOnlyRoot     bool
}

// Spec is the subset of InstanceSpec the driver needs (A1: opaque enough for fake).
type Spec struct {
	InstanceID string
	Generation int64
	Desired    string // running | stopped
}

// Rendered is a placeholder for config render output (filled in A2).
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
