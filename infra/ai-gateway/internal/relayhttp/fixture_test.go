package relayhttp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const account = "01900000-0000-7000-8000-000000000002"
const device = "01900000-0000-7000-8000-000000000003"
const provider = "01900000-0000-7000-8000-000000000004"
const credential = "01900000-0000-7000-8000-000000000005"

// A real loopback gRPC control-plane fixture. The fake JWT has no valid IAM
// signature: these tests MUST NOT be described as real IAM signing end-to-end.
type control struct {
	iam.UnimplementedDeviceAccessServiceServer
	org.UnimplementedMemberServiceServer
	org.UnimplementedAuthorizationServiceServer
	org.UnimplementedQuotaServiceServer
	mu          sync.Mutex
	tenant, raw string
	owners      map[string]string
	active      bool
	quota       int64
	expires     time.Time
	checks      int
	fail        bool
	hook        func(int)
}

func (s *control) VerifyDeviceAiAccess(ctx context.Context, r *iam.VerifyDeviceAiAccessRequest) (*iam.VerifyDeviceAiAccessResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	owner := s.owners[r.AccessToken]
	if owner == "" || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != s.tenant || len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer fixture-service" {
		return nil, status.Error(codes.PermissionDenied, "fixture identity mismatch")
	}
	s.checks++
	if s.hook != nil {
		s.hook(s.checks)
	}
	if s.fail {
		return nil, status.Error(codes.Unavailable, "private dependency error")
	}
	return &iam.VerifyDeviceAiAccessResponse{Active: s.active, TenantId: s.tenant, AccountId: owner, DeviceId: device, ExpiresAt: timestamppb.New(s.expires)}, nil
}
func (s *control) GetMember(_ context.Context, r *org.GetMemberRequest) (*org.GetMemberResponse, error) {
	return &org.GetMemberResponse{Member: &org.Member{TenantId: s.tenant, AccountId: r.AccountId, Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER}}, nil
}
func (s *control) GetEffectivePermissions(context.Context, *org.GetEffectivePermissionsRequest) (*org.GetEffectivePermissionsResponse, error) {
	return &org.GetEffectivePermissionsResponse{Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER, Permissions: []string{"auriga.connect.models.invoke"}}, nil
}
func (s *control) GetEffectiveQuota(context.Context, *org.GetEffectiveQuotaRequest) (*org.GetEffectiveQuotaResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.quota
	return &org.GetEffectiveQuotaResponse{Quota: &org.Quota{Model: &org.ModelQuota{Currency: "USD", DailyTokens: &q, MonthlyBudgetMicros: &q}}}, nil
}

type fixture struct {
	db       *store.Store
	core     *relay.Core
	cfg      relay.Config
	http     Config
	raw      string
	observe  func([]byte)
	finished func()
	control  *control
	secret   []byte
}

func fixtureNew(t *testing.T) *fixture {
	t.Helper()
	db := testpg.Open(t)
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&15 | 0x70
	b[8] = b[8]&63 | 0x80
	tenant := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"tid": tenant, "sub": account, "did": device, "exp": now.Add(2 * time.Minute).Unix()})
	s := &control{tenant: tenant, active: true, quota: 100000, expires: now.Add(time.Minute)}
	s.raw = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".ZmFrZQ"
	s.owners = map[string]string{s.raw: account}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	iam.RegisterDeviceAccessServiceServer(server, s)
	org.RegisterMemberServiceServer(server, s)
	org.RegisterAuthorizationServiceServer(server, s)
	org.RegisterQuotaServiceServer(server, s)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	tokenFile := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(tokenFile, []byte("fixture-service"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	a, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(conn), org.NewMemberServiceClient(conn), org.NewAuthorizationServiceClient(conn), org.NewQuotaServiceClient(conn), "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := admin.NewRelayReader(db, "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := modelgate.New(a, reader)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := budget.New(db, "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if err = os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	protectKey(t, keyPath)
	cipher, err := admin.LoadCipher(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(secret, admin.Binding{Tenant: tenant, Provider: provider, Credential: credential, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	p := api.Provider{Id: provider, Name: "Synthetic", Kind: "openai_compatible", BaseUrl: "https://upstream.example.com/v1", Enabled: true, ActiveCredentialId: *api.NewNullableString(new(credential)), ConnectTimeoutSeconds: 1, FirstByteTimeoutSeconds: 1, IdleTimeoutSeconds: 1, TotalTimeoutSeconds: 3, MaxConcurrency: 2, Revision: 1, Etag: `"1"`}
	m := api.Model{Id: "synthetic", ProviderId: provider, UpstreamModel: "fixed-synthetic-v1", DisplayName: "Synthetic", Enabled: true, ContextWindow: 4096, MaxOutputTokens: 512, Revision: 1, Etag: `"1"`, Price: api.Price{Currency: "USD", Version: "fixed-price", EffectiveAt: now.Add(-time.Hour), InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 2000000}}
	pb, _ := json.Marshal(p)
	mb, _ := json.Marshal(m)
	err = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(), `INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES($1,$2,$3,1)`, tenant, provider, pb); e != nil {
			return e
		}
		if _, e := tx.Exec(t.Context(), `INSERT INTO ai.credential(tenant_id,id,provider_id,state,ciphertext,cipher_version) VALUES($1,$2,$3,'active',$4,1)`, tenant, credential, provider, encrypted); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `INSERT INTO ai.model(tenant_id,id,provider_id,document,grants,revision) VALUES($1,'synthetic',$2,$3,$4,1)`, tenant, provider, mb, `{"items":[{"kind":"member","id":"`+account+`"}]}`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			for _, name := range []string{"budget_outbox", "budget_usage", "budget_request", "budget_period", "budget_subject", "model", "credential", "provider"} {
				if _, err := tx.Exec(ctx, "DELETE FROM ai."+name+" WHERE tenant_id=$1", tenant); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	f := &fixture{db: db, control: s, secret: secret, raw: s.raw}
	cfg := relay.Config{Gate: gate, Reader: reader, Cipher: cipher, Ledger: ledger, Policy: func(_ context.Context, tid, model string, v admin.RelayVersions) (modelprotocol.Policy, error) {
		if tid != tenant || model != "synthetic" || v.ModelRevision != 1 || v.ProviderRevision != 1 || v.CredentialID != credential {
			return modelprotocol.Policy{}, fault("ai.budget_unavailable")
		}
		return syntheticPolicy(), nil
	}, Transport: func(context.Context, relay.Submission, []byte) (relay.Exchange, error) {
		t.Error("unexpected transport")
		return relay.Exchange{}, nil
	},
		Limits: modelprotocol.Limits{MaxBytes: 65536, MaxEvents: 32, MaxEventBytes: 8192}, RecheckInterval: 20 * time.Millisecond, CheckTimeout: time.Second, StopTimeout: 100 * time.Millisecond, AccountingTimeout: time.Second}
	f.cfg = cfg
	f.http = Config{Gate: gate, Authority: a, Ledger: ledger, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, QueryTimeout: time.Second, MaxInFlight: 32, MaxResponseBytes: 1 << 20, ReserveRecovery: func() (func(*Pending), bool) { return func(*Pending) {}, true }}
	f.core, err = relay.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) exec(t *testing.T, sql string) {
	t.Helper()
	if err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error { _, err := tx.Exec(t.Context(), sql); return err }); err != nil {
		t.Fatal(err)
	}
}

// Revoke only this fixture's private runtime role. This causes a real 42501
// during recordUsage after accounting updates, proving transaction rollback
// preserves every slot/amount/period hold. Other test roles remain unaffected.
func (f *fixture) denyUsageWrites(t *testing.T) {
	t.Helper()
	var role string
	if err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error { return tx.QueryRow(t.Context(), "SELECT current_user").Scan(&role) }); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), testpg.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(ctx)
	})
	quoted := pgx.Identifier{role}.Sanitize()
	if err = usageCatalogACL(t.Context(), conn, "REVOKE INSERT ON ai.budget_usage FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := usageCatalogACL(ctx, conn, "GRANT INSERT ON ai.budget_usage TO "+quoted); err != nil {
			t.Error(err)
		}
	})
}

// Use the same cross-binary catalog lock as testpg fixture GRANT/DROP OWNED.
// Acquire it in a separate READ COMMITTED statement so ACL updates get a fresh
// snapshot after waiting. Never keep this transaction open during HTTP probes.
func usageCatalogACL(ctx context.Context, conn *pgx.Conn, statement string) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718245010)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, statement); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
