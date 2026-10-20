package ingress

import (
	"net/url"
)

// Instance describes a local instance for ingress routing.
type Instance struct {
	ID       string
	Upstream *url.URL // HTTP base for /i/ reverse proxy
	RootDir  string   // filesystem root for /files/
	LogPath  string   // rolling log file for /logs/
}

// Registry resolves instance id → Instance.
type Registry interface {
	Lookup(instanceID string) (*Instance, bool)
}

// MapRegistry is a simple mutable registry for tests and early wiring.
type MapRegistry struct {
	m map[string]*Instance
}

func NewMapRegistry(items ...*Instance) *MapRegistry {
	r := &MapRegistry{m: make(map[string]*Instance, len(items))}
	for _, it := range items {
		if it != nil {
			r.m[it.ID] = it
		}
	}
	return r
}

func (r *MapRegistry) Lookup(id string) (*Instance, bool) {
	if r == nil {
		return nil, false
	}
	it, ok := r.m[id]
	return it, ok
}

func (r *MapRegistry) Put(it *Instance) {
	if r.m == nil {
		r.m = make(map[string]*Instance)
	}
	r.m[it.ID] = it
}
