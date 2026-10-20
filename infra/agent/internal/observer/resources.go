package observer

// InstanceResources is a single sample of instance resource use.
type InstanceResources struct {
	CPUMillis  int64
	MemoryMB   int64
	DiskMB     int64
	PIDs       int64
	NetRxBytes int64
	NetTxBytes int64
}

// NodeResources is host-level usage.
type NodeResources struct {
	CPUMillisUsed int64
	MemoryMBUsed  int64
	DiskGBUsed    int64
	DiskGBFree    int64
}

// ResourceCollector gathers node and instance resource stats.
type ResourceCollector interface {
	CollectNode() (NodeResources, error)
	CollectInstance(t InstanceTarget) (InstanceResources, error)
}

// NopResources is used on unsupported platforms / tests.
type NopResources struct{}

func (NopResources) CollectNode() (NodeResources, error) { return NodeResources{}, nil }
func (NopResources) CollectInstance(InstanceTarget) (InstanceResources, error) {
	return InstanceResources{}, nil
}
