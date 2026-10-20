package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSRouteStore never serves cached authorization after a failed freshness check.
// KV revisions invalidate decoded records on migration; delete/purge clears them.
// Only the producer creates/writes the bucket. This client needs read permissions.
type NATSRouteStore struct {
	nc    *nats.Conn
	js    jetstream.JetStream
	mu    sync.Mutex
	cache map[string]cachedRoute
}
type cachedRoute struct {
	revision uint64
	route    InstanceRoute
}

func NewNATSRouteStore(rawURL, creds string) (*NATSRouteStore, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("natsUrl is required for instance routes")
	}
	opts := []nats.Option{nats.Name("auriga-edge-routes"), nats.MaxReconnects(-1), nats.Timeout(5 * time.Second)}
	if creds != "" {
		opts = append(opts, nats.UserCredentials(creds))
	}
	nc, err := nats.Connect(rawURL, opts...)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &NATSRouteStore{nc: nc, js: js, cache: make(map[string]cachedRoute)}, nil
}

func (s *NATSRouteStore) Get(ctx context.Context, id string) (*InstanceRoute, error) {
	if !validRouteID(id) {
		return nil, ErrRouteNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	kv, err := s.js.KeyValue(ctx, "auriga-routes")
	if err != nil {
		return nil, ErrRouteUnavailable
	}
	entry, err := kv.Get(ctx, id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		delete(s.cache, id)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return nil, ErrRouteNotFound
		}
		return nil, ErrRouteUnavailable
	}
	if cached, ok := s.cache[id]; ok && cached.revision == entry.Revision() {
		cp := cached.route
		return &cp, nil
	}
	route, err := DecodeRouteJSON(entry.Value())
	if err != nil || route.InstanceID != id || !validRouteID(route.TenantID) || !validRouteID(route.OwnerID) || !validRouteID(route.NodeID) {
		delete(s.cache, id)
		return nil, ErrRouteNotFound
	}
	if _, err := parseIngress(route.Ingress); err != nil {
		delete(s.cache, id)
		return nil, ErrRouteNotFound
	}
	if len(s.cache) >= 4096 {
		clear(s.cache)
	}
	s.cache[id] = cachedRoute{entry.Revision(), *route}
	return route, nil
}

func (s *NATSRouteStore) Close() { s.nc.Close() }
