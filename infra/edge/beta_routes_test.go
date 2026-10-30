package main

import (
	"context"
	"errors"
	"testing"
)

func TestInstanceRouteModeRequiresExplicitMemory(t *testing.T) {
	for _, mode := range []string{"", "nats"} {
		if _, _, err := openInstanceRoutes(defaultConfig(), mode); err == nil {
			t.Fatal("default startup must still require NATS")
		}
	}
	if _, _, err := openInstanceRoutes(defaultConfig(), "typo"); err == nil {
		t.Fatal("unknown route mode accepted")
	}
	routes, closeRoutes, err := openInstanceRoutes(defaultConfig(), "memory")
	if err != nil {
		t.Fatal(err)
	}
	defer closeRoutes()
	if _, err = routes.Get(context.Background(), "00000000-0000-7000-8000-000000000001"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("empty local routes must deny instance access: %v", err)
	}
}
