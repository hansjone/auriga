package main

import (
	"net/http/httptest"
	"testing"
)

func TestPlacementMaintenanceBrowserRouteBoundary(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	const base = "/api/workspace/v1/admin/"
	for _, sample := range []struct {
		method, path string
		want         bool
	}{
		{"GET", base + "members/" + id + "/placement", true},
		{"PUT", base + "members/" + id + "/dedicated-entitlement", true},
		{"GET", base + "members/" + id + "/placement-switches/lookup", true},
		{"POST", base + "placement-rebuild-plans", true},
		{"GET", base + "placement-rebuild-plans/" + id, true},
		{"PUT", base + "placement-rebuild-plans/" + id, true},
		{"POST", base + "placement-rebuild-plans/" + id + "/start", true},
		{"GET", base + "placement-rebuild-plans/" + id + "/progress", true},
		{"GET", base + "placement-capacity", true},
		{"DELETE", base + "placement-rebuild-plans/" + id, false},
		{"POST", base + "placement-rebuild-plans/" + id, false},
		{"GET", base + "members/" + id + "/dedicated-entitlement", false},
		{"PUT", base + "members/" + id + "/placement", false},
		{"GET", base + "placement-rebuild-plans/not-a-uuid", false},
		{"GET", base + "placement-rebuild-plans/" + id + "/progress/", false},
		{"GET", base + "placement-capacity?other=value", false}, {"GET", base + "placement-capacity?", false},
		{"POST", base + "placement-rebuild-plans%2f" + id + "/start", false},
		{"GET", "/api/org/v1/admin/placement-capacity", false},
		{"GET", "/api/workspace/v1/me", false},
	} {
		t.Run(sample.method+" "+sample.path, func(t *testing.T) {
			r := httptest.NewRequest(sample.method, "https://admin.example.test"+sample.path, nil)
			if got := placementBrowserOperation(r); got != sample.want {
				t.Fatalf("browser context=%v, want %v", got, sample.want)
			}
		})
	}
}
