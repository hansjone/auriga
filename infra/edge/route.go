package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// InstanceRoute mirrors contracts/kv/route.schema.json (consumer-side).
type InstanceRoute struct {
	InstanceID string    `json:"instanceId"`
	TenantID   string    `json:"tenantId"`
	OwnerID    string    `json:"ownerId"`
	NodeID     string    `json:"nodeId"`
	Host       *string   `json:"host"`
	Ingress    string    `json:"ingress"`
	State      string    `json:"state"`
	Generation int       `json:"generation"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// RouteStore looks up instance routes by instance id.
type RouteStore interface {
	Get(ctx context.Context, instanceID string) (*InstanceRoute, error)
}

var ErrRouteNotFound = fmt.Errorf("route not found")
var ErrRouteUnavailable = fmt.Errorf("route service unavailable")

// MemoryRouteStore for tests and local skeleton.
type MemoryRouteStore struct {
	mu   sync.RWMutex
	byID map[string]*InstanceRoute
}

func NewMemoryRouteStore() *MemoryRouteStore {
	return &MemoryRouteStore{byID: map[string]*InstanceRoute{}}
}

func (m *MemoryRouteStore) Put(r *InstanceRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *r
	m.byID[r.InstanceID] = &cp
}

func (m *MemoryRouteStore) Get(_ context.Context, instanceID string) (*InstanceRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byID[instanceID]
	if !ok || r == nil {
		return nil, ErrRouteNotFound
	}
	cp := *r
	return &cp, nil
}

// OwnerChecker verifies the portal session account owns the instance.
type OwnerChecker interface {
	Owns(ctx context.Context, accountID, instanceID string) (bool, error)
}

// RouteOwnerChecker uses RouteStore.ownerId.
type RouteOwnerChecker struct {
	Routes RouteStore
}

func (c RouteOwnerChecker) Owns(ctx context.Context, accountID, instanceID string) (bool, error) {
	r, err := c.Routes.Get(ctx, instanceID)
	if err != nil {
		return false, err
	}
	return r.OwnerID == accountID, nil
}

func DecodeRouteJSON(raw []byte) (*InstanceRoute, error) {
	var r InstanceRoute
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.InstanceID == "" || r.OwnerID == "" || r.Ingress == "" {
		return nil, fmt.Errorf("invalid route record")
	}
	return &r, nil
}
