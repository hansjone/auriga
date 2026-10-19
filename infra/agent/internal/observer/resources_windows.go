//go:build windows

package observer

// WindowsCollector is a stub until Job Object stats land with the Windows runtime (A2/M4).
type WindowsCollector struct{}

func (WindowsCollector) CollectNode() (NodeResources, error) {
	return NodeResources{}, nil
}

func (WindowsCollector) CollectInstance(InstanceTarget) (InstanceResources, error) {
	return InstanceResources{}, nil
}

// DefaultCollector returns the platform collector.
func DefaultCollector() ResourceCollector { return WindowsCollector{} }
