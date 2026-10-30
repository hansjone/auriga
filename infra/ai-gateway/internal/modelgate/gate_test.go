package modelgate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func gateFixture(t *testing.T) (*control, *Gate, *store.Store) {
	t.Helper()
	db := testpg.Open(t)
	t.Cleanup(func() {
		if err := db.Transaction(context.Background(), tenant, func(tx pgx.Tx) error {
			for _, table := range []string{"model", "credential", "provider"} {
				if _, e := tx.Exec(context.Background(), "DELETE FROM ai."+table+" WHERE tenant_id=$1", tenant); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	s, a, _ := controlFixture(t)
	p := api.Provider{Id: device, Name: "Example", Kind: "openai_compatible", BaseUrl: "https://upstream.example.com/v1", Enabled: true, ActiveCredentialId: *api.NewNullableString(new(account)), ConnectTimeoutSeconds: 1, FirstByteTimeoutSeconds: 1, IdleTimeoutSeconds: 1, TotalTimeoutSeconds: 1, MaxConcurrency: 2, Revision: 1, Etag: `"1"`}
	m := api.Model{Id: "example", ProviderId: device, UpstreamModel: "hidden-upstream", DisplayName: "Example", Enabled: true, ContextWindow: 4096, MaxOutputTokens: 512, Revision: 1, Etag: `"1"`, Price: api.Price{Currency: "USD", Version: "v1", EffectiveAt: s.now.Add(-time.Hour), InputMicrosPerMillionTokens: 10, OutputMicrosPerMillionTokens: 20}}
	pb, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	mb, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES($1,$2,$3,1)", tenant, device, pb); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), "INSERT INTO ai.credential(tenant_id,id,provider_id,state,ciphertext,cipher_version) VALUES($1,$2,$3,'active',$4,1)", tenant, account, device, []byte("opaque-fixture-ciphertext")); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), "INSERT INTO ai.model(tenant_id,id,provider_id,document,grants,revision) VALUES($1,'example',$2,$3,$4,1)", tenant, device, mb, `{"items":[{"kind":"member","id":"`+account+`"}]}`)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	reader, e := admin.NewRelayReader(db, "USD", a.now)
	if e != nil {
		t.Fatal(e)
	}
	g, e := New(a, reader)
	if e != nil {
		t.Fatal(e)
	}
	return s, g, db
}

func TestGatePreparationAndRemoteRecheckWithoutTransaction(t *testing.T) {
	s, g, db := gateFixture(t)
	// Executed on the real gRPC server for every IAM/org method. Any retained
	// tenant transaction, including one that no longer holds row locks, fails.
	s.hook = func(_ string) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		e := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid() AND state LIKE 'idle in transaction%'`).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				return fmt.Errorf("remote work retained %d DB transactions", n)
			}
			for _, table := range []string{"model", "provider", "credential"} {
				if _, err := tx.Exec(ctx, "SELECT id FROM ai."+table+" WHERE tenant_id=$1 FOR UPDATE NOWAIT", tenant); err != nil {
					return err
				}
			}
			return nil
		})
		if e != nil {
			t.Error(e)
		}
	}
	call, e := g.Prepare(t.Context(), s.raw, "example")
	if e != nil {
		t.Fatal(e)
	}
	old := call.Deadline()
	if e = g.Recheck(t.Context(), call, false); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	count := len(s.calls)
	s.verify.ExpiresAt = timestamppb.New(old.Add(time.Second))
	s.mu.Unlock()
	if e = g.Recheck(t.Context(), call, true); e != nil {
		t.Fatal(e)
	}
	if call.Deadline() != old || count != 8 {
		t.Fatal("recheck cached approval or extended original deadline")
	}
	snapshot := call.Snapshot()
	if snapshot.ModelID() != "example" {
		t.Fatal("missing private snapshot")
	}
	snapshot = admin.RelaySnapshot{}
	if call.Snapshot().ModelID() != "example" || snapshot.ModelID() != "" {
		t.Fatal("caller replaced private snapshot")
	}
	if text := fmt.Sprintf("%v %+v %#v", *call, call, call); strings.Contains(text, s.raw) {
		t.Fatal("raw token leaked in diagnostic formatting")
	}
	page, e := g.Catalog(t.Context(), s.raw)
	if e != nil || len(page.Data) != 1 {
		t.Fatalf("catalog=%v %v", page, e)
	}
	s.mu.Lock()
	zero := int64(0)
	s.quota.Quota.Model.DailyTokens = &zero
	s.mu.Unlock()
	// Directory visibility is distinct from payment admission. A lowered quota
	// never deletes an existing stream's pinned reservation (D3).
	if _, e = g.Catalog(t.Context(), s.raw); e != nil {
		t.Fatal(e)
	}
	_, e = g.Prepare(t.Context(), s.raw, "example")
	wantCode(t, e, "ai.budget_exceeded")
	wantCode(t, g.Recheck(t.Context(), call, false), "ai.budget_exceeded")
	if e = g.Recheck(t.Context(), call, true); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.verify.Active = false
	s.mu.Unlock()
	wantCode(t, g.Recheck(t.Context(), call, true), "common.unauthenticated")
}

func TestGateRevocationDuringRemoteRecheck(t *testing.T) {
	s, g, db := gateFixture(t)
	call, e := g.Prepare(t.Context(), s.raw, "example")
	if e != nil {
		t.Fatal(e)
	}
	// A committed ACL/model change made DURING remote calls must invalidate the
	// prepared snapshot when control-plane verification finishes.
	s.hook = func(method string) {
		if method == "quota" {
			if e := db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
				_, err := tx.Exec(t.Context(), `UPDATE ai.model SET grants='{"items":[]}',revision=revision+1 WHERE tenant_id=$1`, tenant)
				return err
			}); e != nil {
				t.Error(e)
			}
		}
	}
	wantCode(t, g.Recheck(t.Context(), call, false), "ai.model_not_allowed")
}

func TestGateModelAndPriceRevisionChanges(t *testing.T) {
	s, g, db := gateFixture(t)
	// The snapshot stays fixed even if the price version changes in storage.
	call, e := g.Prepare(t.Context(), s.raw, "example")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE ai.model SET document=jsonb_set(document,'{price,version}','"v2"'),revision=revision+1 WHERE tenant_id=$1`, tenant)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if call.Snapshot().Constraints().Price.Version != "v1" {
		t.Fatal("mutable price snapshot")
	}
	wantCode(t, g.Recheck(t.Context(), call, false), "ai.model_not_allowed")
}

func TestGateExpiryAndQuotaChange(t *testing.T) {
	s, g, _ := gateFixture(t)
	call, e := g.Prepare(t.Context(), s.raw, "example")
	if e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	finite := int64(100)
	s.quota.Quota.Model.DailyTokens = &finite
	s.mu.Unlock()
	wantCode(t, g.Recheck(t.Context(), call, false), "ai.budget_unavailable")
	s.mu.Lock()
	s.now = call.Deadline()
	before := len(s.calls)
	s.mu.Unlock()
	wantCode(t, g.Recheck(t.Context(), call, true), "common.unauthenticated")
	if len(s.calls) != before {
		t.Fatal("expired call contacted dependencies")
	}
}

func TestGateSingleDeploymentCurrency(t *testing.T) {
	_, g, db := gateFixture(t)
	reader, e := admin.NewRelayReader(db, "CNY", time.Now)
	if e != nil {
		t.Fatal(e)
	}
	_, e = New(g.authority, reader)
	wantCode(t, e, "common.validation_failed")
}
