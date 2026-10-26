package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
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

const deviceAccount = "01900000-0000-7000-8000-000000000002"
const deviceID = "01900000-0000-7000-8000-000000000003"

// Protocol integration fixture only: the JWT has no real IAM signature.
type deviceControl struct {
	iam.UnimplementedDeviceAccessServiceServer
	org.UnimplementedMemberServiceServer
	org.UnimplementedAuthorizationServiceServer
	org.UnimplementedQuotaServiceServer
	tenant, raw                 string
	revoked, expired, orgFailed atomic.Bool
	wrongTenant                 atomic.Bool
	calls                       atomic.Int64
	memberHook                  func(context.Context) error
}

func (c *deviceControl) VerifyDeviceAiAccess(_ context.Context, r *iam.VerifyDeviceAiAccessRequest) (*iam.VerifyDeviceAiAccessResponse, error) {
	c.calls.Add(1)
	if r.AccessToken != c.raw {
		return &iam.VerifyDeviceAiAccessResponse{}, nil
	}
	exp := time.Now().Add(time.Minute)
	if c.expired.Load() {
		exp = time.Now().Add(-time.Second)
	}
	tenant := c.tenant
	if c.wrongTenant.Load() {
		tenant = deviceAccount
	}
	return &iam.VerifyDeviceAiAccessResponse{Active: !c.revoked.Load(), TenantId: tenant, AccountId: deviceAccount, DeviceId: deviceID, ExpiresAt: timestamppb.New(exp)}, nil
}
func (c *deviceControl) GetMember(ctx context.Context, _ *org.GetMemberRequest) (*org.GetMemberResponse, error) {
	if c.memberHook != nil {
		if e := c.memberHook(ctx); e != nil {
			return nil, e
		}
	}
	if c.orgFailed.Load() {
		return nil, status.Error(codes.Unavailable, "private error")
	}
	return &org.GetMemberResponse{Member: &org.Member{TenantId: c.tenant, AccountId: deviceAccount, Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER}}, nil
}
func (*deviceControl) GetEffectivePermissions(context.Context, *org.GetEffectivePermissionsRequest) (*org.GetEffectivePermissionsResponse, error) {
	return &org.GetEffectivePermissionsResponse{Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER, Permissions: []string{"auriga.connect.models.invoke"}}, nil
}
func (*deviceControl) GetEffectiveQuota(context.Context, *org.GetEffectiveQuotaRequest) (*org.GetEffectiveQuotaResponse, error) {
	n := int64(100000)
	return &org.GetEffectiveQuotaResponse{Quota: &org.Quota{Model: &org.ModelQuota{Currency: "USD", DailyTokens: &n, MonthlyBudgetMicros: &n}}}, nil
}

func deviceRPC(t *testing.T, c *deviceControl, isIAM bool) team.RPCConfig {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "service.jwt")
	token := rand.Text()
	if e = os.WriteFile(path, []byte(token), 0600); e != nil {
		t.Fatal(e)
	}
	s := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, r any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if strings.Join(md.Get("authorization"), ",") != "Bearer "+token || strings.Join(md.Get("auriga-tenant-id"), ",") != c.tenant {
			return nil, status.Error(codes.PermissionDenied, "fixture auth mismatch")
		}
		return h(ctx, r)
	}))
	if isIAM {
		iam.RegisterDeviceAccessServiceServer(s, c)
	} else {
		org.RegisterMemberServiceServer(s, c)
		org.RegisterAuthorizationServiceServer(s, c)
		org.RegisterQuotaServiceServer(s, c)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	t.Cleanup(func() {
		s.Stop()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	return team.RPCConfig{Target: l.Addr().String(), ServiceTokenFile: path}
}

type deviceFixture struct {
	cfg     deviceConfig
	db      *store.Store
	control *deviceControl
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	t.Helper()
	dsn := testpg.DSN(t)
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	bootstrap, e := pgx.Connect(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	var exists bool
	if e = bootstrap.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='auriga_d7')").Scan(&exists); e != nil {
		t.Fatal(e)
	}
	if !exists {
		if _, e = bootstrap.Exec(t.Context(), "CREATE DATABASE auriga_d7"); e != nil {
			t.Fatal(e)
		}
	}
	_ = bootstrap.Close(t.Context())
	u.Path = "/auriga_d7"
	dsn = u.String()
	m := new(store.Store)
	if e = m.Migrate(t.Context(), dsn); e != nil {
		t.Fatal(e)
	}
	adminConn, e := pgx.Connect(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = adminConn.Close(context.Background()) })
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	role := "d7_test_" + hex.EncodeToString(b[:8])
	password := hex.EncodeToString(b[:])
	quoted := pgx.Identifier{role}.Sanitize()
	// Same cross-binary ACL lock as #204; grant/drop never race other fixtures.
	catalog := func(ctx context.Context, sql string) error {
		tx, e := adminConn.Begin(ctx)
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718245010)"); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, sql); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if e = catalog(t.Context(), "CREATE ROLE "+quoted+" LOGIN PASSWORD '"+password+"'; GRANT USAGE ON SCHEMA ai TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA ai TO "+quoted); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if e := catalog(ctx, "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); e != nil {
			t.Error(e)
		}
	})
	u.User = url.UserPassword(role, password)
	cfg := validDeviceConfig()
	cfg.Store = store.Config{DSN: u.String(), MigrationDSN: dsn}
	cfg.MaxConnections = 16
	cfg.MaxInFlight = 8
	cfg.Capacity = 4
	cfg.ShutdownMillis = 300
	b[6] = b[6]&15 | 0x70
	b[8] = b[8]&63 | 0x80
	cfg.SyntheticTenant = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	c := &deviceControl{tenant: cfg.SyntheticTenant}
	claims, _ := json.Marshal(map[string]any{"tid": c.tenant, "sub": deviceAccount, "did": deviceID, "exp": time.Now().Add(2 * time.Minute).Unix()})
	c.raw = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".ZmFrZQ"
	cfg.IAM = deviceRPCConfig(deviceRPC(t, c, true))
	cfg.Org = deviceRPCConfig(deviceRPC(t, c, false))
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	cfg.MasterKeyFile = filepath.Join(t.TempDir(), "master.key")
	if e = os.WriteFile(cfg.MasterKeyFile, key, 0600); e != nil {
		t.Fatal(e)
	}
	protectDeviceKey(t, cfg.MasterKeyFile)
	cipher, e := admin.LoadCipher(cfg.MasterKeyFile)
	if e != nil {
		t.Fatal(e)
	}
	encrypted, e := cipher.Encrypt(key, admin.Binding{Tenant: c.tenant, Provider: syntheticProvider, Credential: syntheticCredential, Version: 1})
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(t.Context(), cfg.Store)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	p := api.Provider{Id: syntheticProvider, Name: "D7 synthetic", Kind: "openai_compatible", BaseUrl: syntheticURL, Enabled: true, ActiveCredentialId: *api.NewNullableString(new(syntheticCredential)), ConnectTimeoutSeconds: 1, FirstByteTimeoutSeconds: 1, IdleTimeoutSeconds: 1, TotalTimeoutSeconds: 3, MaxConcurrency: 2, Revision: 1, Etag: `"1"`}
	model := api.Model{Id: syntheticModel, ProviderId: syntheticProvider, UpstreamModel: syntheticUpstream, DisplayName: "D7 synthetic", Enabled: true, ContextWindow: 64, MaxOutputTokens: 2, Price: syntheticPrice(), Revision: 1, Etag: `"1"`}
	pb, _ := json.Marshal(p)
	mb, _ := json.Marshal(model)
	if e = db.Transaction(t.Context(), c.tenant, func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(), `INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES($1,$2,$3,1)`, c.tenant, syntheticProvider, pb); e != nil {
			return e
		}
		if _, e := tx.Exec(t.Context(), `INSERT INTO ai.credential(tenant_id,id,provider_id,state,ciphertext,cipher_version) VALUES($1,$2,$3,'active',$4,1)`, c.tenant, syntheticCredential, syntheticProvider, encrypted); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `INSERT INTO ai.model(tenant_id,id,provider_id,document,grants,revision) VALUES($1,$2,$3,$4,$5,1)`, c.tenant, syntheticModel, syntheticProvider, mb, `{"items":[{"kind":"member","id":"`+deviceAccount+`"}]}`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return &deviceFixture{cfg: cfg, db: db, control: c}
}
