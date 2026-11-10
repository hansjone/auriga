package bifrostbackend

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// The admin pool listing must honour limit/cursor like providers and keys.
// Ignoring them made the console re-request the same page forever and fail with
// "Repeated relay cursor" once a tenant exceeded one page of pools.
func TestAdminPoolListingHonoursPagination(t *testing.T) {
	b, admin, _, tenant := reviewBackend(t)
	provider, e := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: "p", Kind: "openai-compatible", BaseUrl: "https://example.com", Enabled: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	price := api.Price{Currency: "USD", Version: "v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}
	for i := range 5 {
		pool := api.RelayPool{Id: fmt.Sprintf("pool-%d", i), Enabled: true, Routes: []api.RelayRoute{{ProviderId: provider.Id, Model: "m", Weight: 1}}, ContextWindow: 16, MaxOutputTokens: 8, Price: price}
		if e = b.SavePool(t.Context(), tenant, pool); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		path := "/api/pools?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := reviewRequest(b, "GET", path, "", "auriga", admin, tenant, "")
		var page struct {
			Items      []struct{ Id string }
			NextCursor string
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) > 2 {
			t.Fatalf("invalid pool page %d %s", w.Code, w.Body.String())
		}
		for _, item := range page.Items {
			if seen[item.Id] {
				t.Fatalf("duplicate pool %s", item.Id)
			}
			seen[item.Id] = true
		}
		pages++
		if cursor = page.NextCursor; cursor == "" {
			break
		}
		if pages > 5 {
			t.Fatal("pool cursor did not advance")
		}
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("pools %d pages %d", len(seen), pages)
	}
}
