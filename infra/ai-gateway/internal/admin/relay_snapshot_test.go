package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

func relayFixture(t *testing.T) (fixture, *RelayReader, string, string) {
	t.Helper()
	f := setup(t)
	var p api.Provider
	w := f.call("POST", prefix+"providers", providerInput, "", "relay-provider")
	expect(t, w, 201, "")
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	var c api.Credential
	w = f.call("POST", prefix+"providers/"+p.Id+"/credentials", `{"secret":"test-only-secret"}`, "", "relay-key")
	expect(t, w, 201, "")
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	expect(t, f.call("POST", prefix+"providers/"+p.Id+"/credentials/"+c.Id+"/activate", `{"reason":"test"}`, etag(c.Revision), ""), 200, "")
	m := api.ModelInput{Id: "example", ProviderId: p.Id, UpstreamModel: "upstream-test", DisplayName: "Example", Enabled: true, ContextWindow: 4096, MaxOutputTokens: 1024, Price: api.Price{Currency: "USD", Version: "v1", EffectiveAt: time.Now().Add(-time.Hour), InputMicrosPerMillionTokens: 10, OutputMicrosPerMillionTokens: 20}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, f.call("POST", prefix+"models", string(b), "", "relay-model"), 201, "")
	expect(t, f.call("PUT", prefix+"models/example/grants", `{"items":[{"kind":"member","id":"`+testUUID+`"}]}`, grantsETag(f.tenant, "example", 1), ""), 200, "")
	r, err := NewRelayReader(f.s.store, "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return f, r, p.Id, c.Id
}

func TestRelaySnapshotIsolationAndStates(t *testing.T) {
	f, r, pid, cid := relayFixture(t)
	ctx := t.Context()
	snap, err := r.Capture(ctx, f.tenant, testUUID, "example")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Versions().CredentialID != cid || snap.Constraints().Price.Version != "v1" {
		t.Fatal("missing fixed versions")
	}
	bounds := snap.Constraints()
	bounds.Price.Version = "tampered"
	if snap.Constraints().Price.Version != "v1" {
		t.Fatal("mutable snapshot")
	}
	page, err := r.Catalog(ctx, f.tenant, testUUID)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("catalog %v %v", page, err)
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err = json.Unmarshal(b, &public); err != nil {
		t.Fatal(err)
	}
	if len(public) != 2 || len(public["data"].([]any)[0].(map[string]any)) != 4 {
		t.Fatalf("non-public catalog: %s", b)
	}
	foreign, _ := newID()
	if err = f.s.store.Transaction(ctx, foreign, func(tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ai.model WHERE tenant_id=$1", f.tenant).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatal("RLS exposed foreign rows despite explicit tenant predicate")
		}
		_, e := r.capture(ctx, tx, f.tenant, testUUID, "example")
		relayCode(t, e, "ai.model_not_allowed")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tenant, account string }{{foreign, testUUID}, {f.tenant, foreign}} {
		_, err = r.Capture(ctx, tc.tenant, tc.account, "example")
		relayCode(t, err, "ai.model_not_allowed")
	}
	tests := []struct{ name, sql, code string }{
		{"model-disabled", `UPDATE ai.model SET document=jsonb_set(document,'{enabled}','false'),revision=revision+1 WHERE tenant_id=$1`, "ai.model_not_allowed"},
		{"provider-disabled", `UPDATE ai.provider SET document=jsonb_set(document,'{enabled}','false'),revision=revision+1 WHERE tenant_id=$1`, "ai.model_not_allowed"},
		{"no-active", `UPDATE ai.provider SET document=jsonb_set(document,'{activeCredentialId}','null'),revision=revision+1 WHERE tenant_id=$1`, "ai.model_not_allowed"},
		{"revoked", `UPDATE ai.credential SET state='revoked',revision=revision+1 WHERE tenant_id=$1`, "ai.model_not_allowed"},
		{"group", `UPDATE ai.model SET grants='{"items":[{"kind":"member","id":"` + testUUID + `"},{"kind":"group","id":"` + testUUID + `"}]}',revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-currency", `UPDATE ai.model SET document=jsonb_set(document,'{price,currency}','"CNY"'),revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-future", `UPDATE ai.model SET document=jsonb_set(document,'{price,effectiveAt}','"2999-01-01T00:00:00Z"'),revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-version-null", `UPDATE ai.model SET document=jsonb_set(document,'{price,version}','null'),revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-input-null", `UPDATE ai.model SET document=jsonb_set(document,'{price,inputMicrosPerMillionTokens}','null'),revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-output-null", `UPDATE ai.model SET document=jsonb_set(document,'{price,outputMicrosPerMillionTokens}','null'),revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"price-missing", `UPDATE ai.model SET document=document-'price',revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"role", `UPDATE ai.model SET grants='{"items":[{"kind":"role","id":"` + testUUID + `"}]}',revision=revision+1 WHERE tenant_id=$1`, "ai.budget_unavailable"},
		{"reference-conflict", `UPDATE ai.model SET document=jsonb_set(document,'{providerId}','"` + foreign + `"'),revision=revision+1 WHERE tenant_id=$1`, "ai.model_not_allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
				if _, e := tx.Exec(ctx, tc.sql, f.tenant); e != nil {
					return e
				}
				_, e := r.capture(ctx, tx, f.tenant, testUUID, "example")
				relayCode(t, e, tc.code)
				return pgx.ErrTxClosed // Roll back the mutation.
			})
			if err != pgx.ErrTxClosed {
				t.Fatal(err)
			}
		})
	}
	if err = f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `UPDATE ai.model SET document=jsonb_set(jsonb_set(document,'{price,inputMicrosPerMillionTokens}','0'),'{price,outputMicrosPerMillionTokens}','0'),revision=revision+1 WHERE tenant_id=$1`, f.tenant); e != nil {
			return e
		}
		free, e := r.capture(ctx, tx, f.tenant, testUUID, "example")
		if e != nil {
			t.Fatal(e)
		}
		if free.Constraints().Price.InputMicrosPerMillionTokens != 0 || free.Constraints().Price.OutputMicrosPerMillionTokens != 0 {
			t.Fatal("explicit zero price was changed")
		}
		return pgx.ErrTxClosed
	}); err != pgx.ErrTxClosed {
		t.Fatal(err)
	}
	var got string
	var borrowed []byte
	err = r.WithCredential(ctx, snap, f.s.cipher, func(_ RelayTarget, secret []byte) error {
		got = string(secret)
		borrowed = secret
		return f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
			for _, table := range []string{"model", "provider", "credential"} {
				if _, e := tx.Exec(ctx, "SELECT id FROM ai."+table+" WHERE tenant_id=$1 FOR UPDATE NOWAIT", f.tenant); e != nil {
					return e
				}
			}
			return nil
		})
	})
	if err != nil || got != "test-only-secret" {
		t.Fatalf("AAD dispatch: %q %v (%s)", got, err, pid)
	}
	for _, b := range borrowed {
		if b != 0 {
			t.Fatal("plaintext retained after callback")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", snap, snap), "upstream") {
		t.Fatal("debug formatting exposed internal target")
	}
}

func TestRelaySnapshotConcurrentRevocation(t *testing.T) {
	f, r, _, _ := relayFixture(t)
	snap, err := r.Capture(t.Context(), f.tenant, testUUID, "example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	locked, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		writerDone <- f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, "UPDATE ai.credential SET state='revoked',revision=revision+1 WHERE tenant_id=$1", f.tenant); e != nil {
				return e
			}
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case e := <-writerDone:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reading := make(chan error, 1)
	go func() { reading <- r.Recheck(ctx, snap, false) }()
	waitForRelayLock(t, ctx, f)
	close(release)
	if err = <-writerDone; err != nil {
		t.Fatal(err)
	}
	relayCode(t, <-reading, "ai.model_not_allowed")
}

func waitForRelayLock(t *testing.T, ctx context.Context, f fixture) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if e := f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%FOR SHARE OF m,p,c%')`).Scan(&waiting)
		}); e != nil {
			t.Fatal(e)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("reader never waited for concurrent writer", ctx.Err())
		}
	}
}

func TestRelaySnapshotConcurrentRotation(t *testing.T) {
	f, r, pid, cid := relayFixture(t)
	old, err := r.Capture(t.Context(), f.tenant, testUUID, "example")
	if err != nil {
		t.Fatal(err)
	}
	var next api.Credential
	w := f.call("POST", prefix+"providers/"+pid+"/credentials", `{"secret":"rotated"}`, "", "concurrent-next")
	expect(t, w, 201, "")
	if err = json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		done <- f.s.store.Transaction(ctx, f.tenant, func(tx pgx.Tx) error {
			p, e := loadProvider(ctx, tx, f.tenant, pid, true)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE ai.credential SET state='retiring',revision=revision+1 WHERE tenant_id=$1 AND id=$2", f.tenant, cid); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE ai.credential SET state='active',revision=revision+1 WHERE tenant_id=$1 AND id=$2", f.tenant, next.Id); e != nil {
				return e
			}
			p.ActiveCredentialId = *api.NewNullableString(&next.Id)
			p.Revision++
			if e = saveProvider(ctx, tx, f.tenant, p); e != nil {
				return e
			}
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case e := <-done:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	type captureResult struct {
		snapshot RelaySnapshot
		err      error
	}
	reading := make(chan captureResult, 1)
	go func() { s, e := r.Capture(ctx, f.tenant, testUUID, "example"); reading <- captureResult{s, e} }()
	waitForRelayLock(t, ctx, f)
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	result := <-reading
	if result.err != nil {
		relayCode(t, result.err, "ai.model_not_allowed")
	} else if result.snapshot.Versions().CredentialID != next.Id {
		t.Fatal("mixed/old snapshot survived concurrent rotation")
	}
	fresh, err := r.Capture(ctx, f.tenant, testUUID, "example")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Versions().CredentialID != next.Id || old.Versions().CredentialID != cid {
		t.Fatal("key selection was not immutable")
	}
	if err = r.Recheck(ctx, old, true); err != nil {
		t.Fatal(err)
	}
}

func relayCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error=%v want=%s", err, want)
	}
}

func TestRelaySnapshotRotationAndRevoke(t *testing.T) {
	f, r, pid, cid := relayFixture(t)
	now := time.Now()
	r.now = func() time.Time { return now }
	snap, err := r.Capture(t.Context(), f.tenant, testUUID, "example")
	if err != nil {
		t.Fatal(err)
	}
	var next api.Credential
	w := f.call("POST", prefix+"providers/"+pid+"/credentials", `{"secret":"replacement"}`, "", "relay-next")
	expect(t, w, 201, "")
	if err = json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	expect(t, f.call("POST", prefix+"providers/"+pid+"/credentials/"+next.Id+"/activate", `{"reason":"rotation"}`, etag(next.Revision), ""), 200, "")
	relayCode(t, r.Recheck(t.Context(), snap, false), "ai.model_not_allowed")
	if err = r.Recheck(t.Context(), snap, true); err != nil {
		t.Fatal(err)
	}
	if snap.Versions().CredentialID != cid {
		t.Fatal("rotation replaced fixed key")
	}
	now = now.Add(61 * time.Second)
	relayCode(t, r.Recheck(t.Context(), snap, true), "ai.model_not_allowed")
	now = now.Add(-61 * time.Second)
	expect(t, f.call("POST", prefix+"providers/"+pid+"/credentials/"+cid+"/revoke", `{"reason":"emergency"}`, etag(3), ""), 200, "")
	relayCode(t, r.Recheck(context.Background(), snap, true), "ai.model_not_allowed")
}
