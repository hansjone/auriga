package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlacementMaintenanceBrowserRouteBoundary(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	const base = "/api/workspace/v1/admin/"
	longQ := strings.Repeat("a", 257)
	maxQ := strings.Repeat("张", 256)
	for _, sample := range []struct {
		method, path string
		want         bool
	}{
		{"GET", base + "members/" + id + "/placement", true},
		{"PUT", base + "members/" + id + "/dedicated-entitlement", true},
		{"GET", base + "members/" + id + "/placement-switches/lookup", true},
		{"GET", base + "placement-migration-candidates", true},
		{"GET", base + "placement-migration-candidates?q=", true},
		{"GET", base + "placement-migration-candidates?q=alice&limit=50&offset=0", true},
		{"GET", base + "placement-migration-candidates?q=%E5%BC%A0%E4%B8%89&limit=200&offset=100&memberId=" + id, true},
		{"GET", base + "placement-migration-candidates?q=" + maxQ, true},
		{"GET", base + "placement-migration-candidates?memberId=" + id, true},
		{"GET", base + "placement-rebuild-plans", true},
		{"POST", base + "placement-rebuild-plans", true},
		{"POST", base + "placement-rebuild-plans/preflight", true},
		{"GET", base + "placement-rebuild-plans/" + id, true},
		{"PUT", base + "placement-rebuild-plans/" + id, true},
		{"POST", base + "placement-rebuild-plans/" + id + "/start", true},
		{"GET", base + "placement-rebuild-plans/" + id + "/progress", true},
		{"GET", base + "placement-capacity", true},
		{"POST", base + "placement-migration-candidates", false},
		{"GET", base + "placement-migration-candidates?", false},
		{"GET", base + "placement-migration-candidates?unknown=1", false},
		{"GET", base + "placement-migration-candidates?limit=10&limit=20", false},
		{"GET", base + "placement-migration-candidates?q=a;limit=10", false},
		{"GET", base + "placement-migration-candidates?q=a&&limit=10", false},
		{"GET", base + "placement-migration-candidates?&q=a", false},
		{"GET", base + "placement-migration-candidates?q=a&", false},
		{"GET", base + "placement-migration-candidates?q", false},
		{"GET", base + "placement-migration-candidates?%71=alice", false},
		{"GET", base + "placement-migration-candidates?limit=0", false},
		{"GET", base + "placement-migration-candidates?limit=201", false},
		{"GET", base + "placement-migration-candidates?limit=-1", false},
		{"GET", base + "placement-migration-candidates?limit=+10", false},
		{"GET", base + "placement-migration-candidates?limit=abc", false},
		{"GET", base + "placement-migration-candidates?offset=-1", false},
		{"GET", base + "placement-migration-candidates?offset=9999999999999", false},
		{"GET", base + "placement-migration-candidates?memberId=not-a-uuid", false},
		{"GET", base + "placement-migration-candidates?memberId=", false},
		{"GET", base + "placement-migration-candidates?memberId=11111111-2222-3333-4444-55555555555A", false},
		{"GET", base + "placement-migration-candidates?q=%00", false},
		{"GET", base + "placement-migration-candidates?q=a%0ab", false},
		{"GET", base + "placement-migration-candidates?q=%zz", false},
		{"GET", base + "placement-migration-candidates?q=%ff", false},
		{"GET", base + "placement-migration-candidates?q=" + longQ, false},
		{"GET", base + "placement-rebuild-plans?", false},
		{"GET", base + "placement-rebuild-plans?limit=10", false},
		{"GET", base + "placement-rebuild-plans/preflight", false},
		{"POST", base + "placement-rebuild-plans/preflight?x=1", false},
		{"POST", base + "placement-rebuild-plans/preflight?", false},
		{"POST", base + "placement-rebuild-plans%2fpreflight", false},
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
