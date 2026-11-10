package budget

import (
	"fmt"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
)

// Administrators read usage to see recent calls: pages run newest first and the
// cursor continues strictly older without gaps or duplicates.
func TestListUsageNewestFirst(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	want := []string{}
	for i := range 5 {
		r := admit(t, l, snap, fmt.Sprintf("order-%d", i))
		finish(t, l, dispatch(t, l, r), snap, 1, 1)
		want = append([]string{r.Admission.RequestId}, want...)
	}
	got := []string{}
	cursor := ""
	for range 4 {
		page, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			got = append(got, row.RequestId)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("usage order: got %v want %v", got, want)
	}
}
