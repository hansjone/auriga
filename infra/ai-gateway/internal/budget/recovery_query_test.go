package budget

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func TestRecoveryQueryValidation(t *testing.T) {
	l := new(Ledger) // Invalid inputs must fail before any database access.
	tenant, owner, requestID := id(t), id(t), id(t)
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for _, tc := range []struct {
		tenant string
		limit  int
		cursor string
	}{
		{"", 1, ""}, {"not-a-uuid", 1, ""}, {tenant, 0, ""}, {tenant, -1, ""}, {tenant, 201, ""},
		{tenant, 1, strings.Repeat("a", 513)}, {tenant, 1, "%bad"},
		{tenant, 1, encode("recovery-v1|"+tenant+"|"+requestID+"|"+owner) + "\n"},
		{tenant, 1, encode("recovery-v1|"+tenant+"|"+requestID+"|"+owner) + "="},
		{tenant, 1, encode("recovery-v1|" + id(t) + "|" + requestID + "|" + owner)},
		{tenant, 1, encode("recovery-v1|" + tenant + "|bad|" + owner)},
		{tenant, 1, encode("recovery-v1|" + tenant + "|" + requestID + "|bad")},
		{tenant, 1, encode("recovery-v1|" + tenant + "|" + requestID + "|" + owner + "|extra")},
		{tenant, 1, encode("recovery-v2|" + tenant + "|" + requestID + "|" + owner)},
		{tenant, 1, encode(tenant + "|" + owner + "|" + time.Now().UTC().Format(time.RFC3339Nano) + "|" + requestID)},
	} {
		page, err := l.ListRecoveryCandidates(t.Context(), tc.tenant, tc.limit, tc.cursor)
		wantCode(t, err, "common.validation_failed")
		if page != nil {
			t.Fatal("invalid input returned a page")
		}
	}
	for _, r := range []Ref{{}, {"bad", owner, requestID}, {tenant, "bad", requestID}, {tenant, owner, "bad"}} {
		got, err := l.GetRecoveryCandidate(t.Context(), r)
		wantCode(t, err, "common.validation_failed")
		if got != nil {
			t.Fatal("invalid Ref returned a candidate")
		}
	}
	// Keep the projection deliberately independent of Admission/Usage and raw DB data.
	typ := reflect.TypeFor[RecoveryCandidate]()
	want := []string{"Ref", "State", "Revision", "SlotHeld", "CreatedAt"}
	if typ.NumField() != len(want) {
		t.Fatal("recovery projection grew")
	}
	for i, name := range want {
		if typ.Field(i).Name != name {
			t.Fatalf("unexpected recovery field %s", typ.Field(i).Name)
		}
	}
}

// Deterministic request keys exercise the full PK, including equal request IDs
// across owners. Only the fixture changes IDs, immediately after real admission.
func recoveryAdmit(t *testing.T, l *Ledger, snap TrustedSnapshot, n int) *Reservation {
	t.Helper()
	r := admit(t, l, snap, fmt.Sprintf("recovery-%d", n))
	requestID := fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
	originalID := r.Admission.RequestId
	r.Admission.RequestId = requestID
	raw, err := proto.Marshal(r.Admission)
	if err != nil {
		t.Fatal(err)
	}
	err = l.store.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE ai.budget_request SET request_id=$4,admission=$5 WHERE tenant_id=$1 AND subject=$2 AND request_id=$3`, r.Admission.TenantId, r.Admission.AccountId, originalID, requestID, raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func recoverySnapshot(t *testing.T, s *store.Store, tenant string) string {
	t.Helper()
	var raw []byte
	err := s.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		// Every column, including key_hash/fingerprint/admission/price, is compared;
		// these fixture-only JSON snapshots never leave the test or read port.
		return tx.QueryRow(t.Context(), `SELECT jsonb_build_object(
		 'deployment',(SELECT jsonb_agg(to_jsonb(d) ORDER BY singleton) FROM ai.budget_deployment d),
		 'subject',(SELECT jsonb_agg(to_jsonb(s) ORDER BY subject) FROM ai.budget_subject s),
		 'period',(SELECT jsonb_agg(to_jsonb(p) ORDER BY subject,kind,start_at) FROM ai.budget_period p),
		 'request',(SELECT jsonb_agg(to_jsonb(r) ORDER BY subject,request_id) FROM ai.budget_request r),
		 'usage',(SELECT jsonb_agg(to_jsonb(u) ORDER BY subject,request_id) FROM ai.budget_usage u),
		 'outbox',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM ai.budget_outbox o))`).Scan(&raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRecoveryScanReadOnly(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	tenant := id(t)
	var candidates []RecoveryCandidate
	var all []*Reservation
	var usageOwner string
	for i, state := range []string{"reserved", "dispatching", "unknown-held", "unknown-released", "completed", "cancelled", "failed"} {
		snap := snapshot(t)
		snap.Admission.TenantId = tenant
		// The first two owners deliberately share a request_id.
		r := recoveryAdmit(t, l, snap, max(1, i))
		if state != "reserved" && state != "cancelled" {
			r = dispatch(t, l, r)
		}
		var err error
		switch state {
		case "unknown-held", "unknown-released":
			r, err = l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "unknown", ExecutionStopped: state == "unknown-released"})
		case "completed":
			r = finish(t, l, r, snap, 1, 1)
			usageOwner = snap.Admission.AccountId
			finish(t, l, dispatch(t, l, admit(t, l, snap, "second-usage")), snap, 1, 1)
		case "cancelled":
			r, err = l.Recover(t.Context(), ref(r), r.Revision, RecoveryEvidence{NeverSubmitted: true})
		case "failed":
			r, err = l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "failed", ExecutionStopped: true, Usage: &FinalUsage{InputTokens: 1, OutputTokens: 1, Price: snap.Price}})
		}
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
		if i < 3 {
			candidates = append(candidates, RecoveryCandidate{Ref: ref(r), State: r.State, Revision: r.Revision, SlotHeld: true})
		}
	}
	otherSnap := snapshot(t)
	foreign := recoveryAdmit(t, l, otherSnap, 1)
	before, foreignBefore := recoverySnapshot(t, s, tenant), recoverySnapshot(t, s, otherSnap.Admission.TenantId)
	slices.SortFunc(candidates, func(a, b RecoveryCandidate) int {
		if cmp := strings.Compare(a.Ref.RequestID, b.Ref.RequestID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Ref.AccountID, b.Ref.AccountID)
	})
	cursor := ""
	for i, want := range candidates {
		page, err := l.ListRecoveryCandidates(t.Context(), tenant, 1, cursor)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("page %d: %+v %v", i, page, err)
		}
		got := page.Items[0]
		if got.Ref != want.Ref || got.State != want.State || got.Revision != want.Revision || got.SlotHeld != want.SlotHeld || got.CreatedAt.IsZero() {
			t.Fatalf("candidate %d: %+v want %+v", i, got, want)
		}
		point, err := l.GetRecoveryCandidate(t.Context(), got.Ref)
		if err != nil || !reflect.DeepEqual(point, &got) {
			t.Fatalf("point differs: %+v %v", point, err)
		}
		if i < len(candidates)-1 {
			if page.NextCursor == nil {
				t.Fatal("missing next cursor")
			}
			cursor = *page.NextCursor
		} else if page.NextCursor != nil {
			t.Fatal("last page has next cursor")
		}
	}
	page, err := l.ListRecoveryCandidates(t.Context(), tenant, 200, "")
	if err != nil || len(page.Items) != len(candidates) || page.NextCursor != nil {
		t.Fatalf("limit 200: %+v %v", page, err)
	}
	// Point lookup also returns terminal rows and released unknown slots, so a
	// pending supervisor can distinguish them from absent rows without admission.
	for i, r := range all {
		got, err := l.GetRecoveryCandidate(t.Context(), ref(r))
		if err != nil || got.Ref != ref(r) || got.State != r.State || got.Revision != r.Revision || got.SlotHeld != (i < 3) {
			t.Fatalf("current point: %+v %v", got, err)
		}
	}
	for _, r := range []Ref{{tenant, id(t), id(t)}, {tenant, foreign.Admission.AccountId, foreign.Admission.RequestId}} {
		got, err := l.GetRecoveryCandidate(t.Context(), r)
		wantCode(t, err, "common.not_found")
		if got != nil {
			t.Fatal("missing point manufactured a candidate")
		}
	}
	_, err = l.ListRecoveryCandidates(t.Context(), otherSnap.Admission.TenantId, 1, cursor)
	wantCode(t, err, "common.validation_failed")
	usage, err := l.ListUsage(t.Context(), tenant, usageOwner, 1, "")
	if err != nil || usage.NextCursor == nil {
		t.Fatalf("usage cursor fixture: %+v %v", usage, err)
	}
	_, err = l.ListRecoveryCandidates(t.Context(), tenant, 1, *usage.NextCursor)
	wantCode(t, err, "common.validation_failed")
	_, err = l.ListUsage(t.Context(), tenant, usageOwner, 1, cursor)
	wantCode(t, err, "common.validation_failed")
	for _, c := range []string{"", base64.RawURLEncoding.EncodeToString([]byte("recovery-v1|" + tenant + "|ffffffff-ffff-7fff-bfff-ffffffffffff|ffffffff-ffff-7fff-bfff-ffffffffffff"))} {
		emptyTenant := tenant
		if c == "" {
			emptyTenant = id(t)
		}
		empty, err := l.ListRecoveryCandidates(t.Context(), emptyTenant, 1, c)
		if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
			t.Fatalf("empty page: %+v %v", empty, err)
		}
	}
	// Catalog inspection uses the same non-owner runtime role. No tenant table
	// scans by the migration/superuser role are involved.
	err = s.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		var def string
		if err := tx.QueryRow(t.Context(), `SELECT indexdef FROM pg_indexes WHERE schemaname='ai' AND indexname='budget_recovery_page'`).Scan(&def); err != nil {
			return err
		}
		for _, part := range []string{"tenant_id, request_id, subject", "reserved", "dispatching", "unknown", "slot_held", "WHERE"} {
			if !strings.Contains(def, part) {
				t.Fatalf("missing index component %s", part)
			}
		}
		var n int
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='ai' AND c.relname IN ('budget_subject','budget_period','budget_request','budget_usage','budget_outbox') AND c.relrowsecurity AND c.relforcerowsecurity`).Scan(&n); err != nil {
			return err
		}
		if n != 5 {
			t.Fatalf("old ledger tables/FORCE RLS changed: %d", n)
		}
		// No tenant WHERE: verify RLS independently of the port's predicate.
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE subject=$1`, foreign.Admission.AccountId).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("RLS leaked foreign requests")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(t.Context(), testpg.DSN(t)); err != nil {
		t.Fatal(err)
	}
	if recoverySnapshot(t, s, tenant) != before || recoverySnapshot(t, s, otherSnap.Admission.TenantId) != foreignBefore {
		t.Fatal("queries/migration changed budget, request/idempotency, usage or outbox rows")
	}
	encoded, err := json.Marshal(page.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil || len(fields) != 5 {
		t.Fatalf("minimal serialized projection: %s %v", encoded, err)
	}
}

func TestRecoveryScanNextCycle(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	first := recoveryAdmit(t, l, snap, 10)
	second := dispatch(t, l, recoveryAdmit(t, l, snap, 30))
	page, err := l.ListRecoveryCandidates(t.Context(), snap.Admission.TenantId, 1, "")
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil || page.Items[0].Ref != ref(first) {
		t.Fatalf("first page: %+v %v", page, err)
	}
	first = dispatch(t, l, first)
	finish(t, l, second, snap, 1, 1)
	newSnap := snapshot(t)
	newSnap.Admission.TenantId = snap.Admission.TenantId
	earlier := recoveryAdmit(t, l, newSnap, 5)
	before := recoverySnapshot(t, s, snap.Admission.TenantId)
	last, err := l.ListRecoveryCandidates(t.Context(), snap.Admission.TenantId, 1, *page.NextCursor)
	if err != nil || len(last.Items) != 0 || last.Items == nil || last.NextCursor != nil {
		t.Fatalf("transitioned row must disappear: %+v %v", last, err)
	}
	point, err := l.GetRecoveryCandidate(t.Context(), ref(first))
	if err != nil || point.State != "dispatching" || point.Revision != first.Revision || !point.SlotHeld {
		t.Fatalf("latest revision/slot: %+v %v", point, err)
	}
	next, err := l.ListRecoveryCandidates(t.Context(), snap.Admission.TenantId, 200, "")
	if err != nil || len(next.Items) != 2 || next.NextCursor != nil || next.Items[0].Ref != ref(earlier) || next.Items[1].Ref != ref(first) || next.Items[1].State != "dispatching" {
		t.Fatalf("next cycle must find earlier admission and current state: %+v %v", next, err)
	}
	if recoverySnapshot(t, s, snap.Admission.TenantId) != before {
		t.Fatal("scan/point lookup changed rows")
	}
}
