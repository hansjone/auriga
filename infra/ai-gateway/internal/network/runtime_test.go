package network

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/dynamicpb"
)

func startIAMRPC(t *testing.T, f *fakeIAM) team.RPCConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	token := randHex(t, 32)
	tokenFile := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if strings.Join(md.Get("authorization"), "") != "Bearer "+token || strings.Join(md.Get("auriga-tenant-id"), "") != testTenant {
			t.Error("missing existing internal service identity or delegated tenant")
		}
		return handler(ctx, req)
	}))
	svc := iamFile.Services().ByName("NetworkGatewayService")
	methods := []grpc.MethodDesc{}
	for i := 0; i < svc.Methods().Len(); i++ {
		method := svc.Methods().Get(i)
		full := "/" + string(svc.FullName()) + "/" + string(method.Name())
		methods = append(methods, grpc.MethodDesc{MethodName: string(method.Name()), Handler: func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			req := dynamicpb.NewMessage(method.Input())
			resp := dynamicpb.NewMessage(method.Output())
			if err := decode(req); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, _ any) (any, error) { err := f.Invoke(ctx, full, req, resp); return resp, err }
			if interceptor != nil {
				return interceptor(ctx, req, &grpc.UnaryServerInfo{FullMethod: full}, handler)
			}
			return handler(ctx, req)
		}})
	}
	srv.RegisterService(&grpc.ServiceDesc{ServiceName: string(svc.FullName()), HandlerType: (*interface{})(nil), Methods: methods}, f)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(listener) }()
	t.Cleanup(func() { srv.Stop(); _ = listener.Close(); <-done })
	return team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile}
}

func runtimeFixture(t *testing.T) (*harness, RuntimeConfig, *fakeIAM) {
	t.Helper()
	o := defaultBrokerOpts()
	o.noCallout = true
	h := newHarness(t, o)
	f := &fakeIAM{active: true}
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	issuer, _ := h.issuer.Seed()
	xkey, _ := h.xkey.Seed()
	cfg := RuntimeConfig{BrokerConfigFile: h.configFile, ServerAudience: "server-a", ClientAccount: clientAccount, IssuerSeedFile: write("issuer.seed", issuer), XKeySeedFile: write("xkey.seed", xkey), IAM: startIAMRPC(t, f), RedeemTimeoutMillis: 500, ActiveIntervalMillis: 10, RequestTimeoutMillis: 100, MaxKickAttempts: 3, MaxConnections: 100}
	cfg.Auth = NATSConfig{URL: h.tcpURL(), Username: h.authUser, PasswordFile: write("auth.password", []byte(h.authPass))}
	cfg.Ops = NATSConfig{URL: h.tcpURL(), Username: h.opsUser, PasswordFile: write("ops.password", []byte(h.opsPass))}
	return h, cfg, f
}

func TestProductionRuntimeRedeemsAndKicksAfterDeviceRevocation(t *testing.T) {
	h, cfg, f := runtimeFixture(t)
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	ad := h.startAdapter()
	ingress := ingressForTicket("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if _, err = nc.SubscribeSync(DownSubject(ingress)); err != nil {
		t.Fatal(err)
	}
	if err = nc.Publish(UpSubject(ingress), []byte("runtime")); err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := ad.expectUp(t); got.ingress != ingress {
		t.Fatalf("private ingress=%q", got.ingress)
	}
	if _, err = nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect()); err == nil {
		t.Fatal("ticket replay authorized")
	}
	f.mu.Lock()
	f.active = false
	f.mu.Unlock()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !nc.IsClosed() {
		select {
		case <-deadline:
			t.Fatal("production runtime did not kick revoked device")
		case <-ticker.C:
		}
	}
}

func TestProductionRuntimeRejectsMissingOrMismatchedConfiguration(t *testing.T) {
	_, cfg, _ := runtimeFixture(t)
	for name, change := range map[string]func(*RuntimeConfig){
		"missing audience":   func(c *RuntimeConfig) { c.ServerAudience = "" },
		"missing broker":     func(c *RuntimeConfig) { c.BrokerConfigFile = "" },
		"missing issuer":     func(c *RuntimeConfig) { c.IssuerSeedFile = "" },
		"missing xkey":       func(c *RuntimeConfig) { c.XKeySeedFile = "" },
		"missing JWT":        func(c *RuntimeConfig) { c.IAM.ServiceTokenFile = "" },
		"timeout":            func(c *RuntimeConfig) { c.RedeemTimeoutMillis = 2000 },
		"unbounded retries":  func(c *RuntimeConfig) { c.MaxKickAttempts = 0 },
		"unbounded capacity": func(c *RuntimeConfig) { c.MaxConnections = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cfg
			change(&copy)
			runtime, err := StartRuntime(context.Background(), copy)
			if err == nil {
				runtime.Close()
				t.Fatal("invalid config started")
			}
		})
	}
}

func TestIAMRPCUnreachableFailsClosed(t *testing.T) {
	cfg := startIAMRPC(t, &fakeIAM{})
	cfg.Target = "127.0.0.1:0"
	conn, err := team.DialRPC(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	r, err := NewIAMRedeemer(conn, IAMConfig{ServerID: "server-id", ServerAudience: "server-a", Account: clientAccount, RedeemTimeout: 30 * time.Millisecond, AuthTimeout: time.Second, MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	if grant, err := r.Redeem(context.Background(), testAttempt()); err == nil || grant.Ingress != "" {
		t.Fatalf("unreachable IAM authorized: %+v %v", grant, err)
	}
}

func TestProductionRuntimeRequiresExplicitSharedBrokerTimeout(t *testing.T) {
	_, cfg, _ := runtimeFixture(t)
	raw, err := os.ReadFile(cfg.BrokerConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "missing-timeout.conf")
	raw = []byte(strings.Replace(string(raw), "timeout: 2", "", 1))
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.BrokerConfigFile = path
	if runtime, err := StartRuntime(context.Background(), cfg); err == nil {
		runtime.Close()
		t.Fatal("missing authorization.timeout started runtime")
	}
}

func TestProductionRuntimeRequiresBrokerEnforcedAccountLimits(t *testing.T) {
	_, cfg, _ := runtimeFixture(t)
	raw, err := os.ReadFile(cfg.BrokerConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "missing-limits.conf")
	raw = []byte(strings.Replace(string(raw), "limits { max_payload: 4096, max_subscriptions: 8 }", "", 1))
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.BrokerConfigFile = path
	if runtime, err := StartRuntime(context.Background(), cfg); err == nil {
		runtime.Close()
		t.Fatal("missing enforced account limits started runtime")
	}
}

func TestProductionRuntimeFailsClosedOnIAMOutageAndReclaimsClosedConnections(t *testing.T) {
	h, cfg, f := runtimeFixture(t)
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	f.mu.Lock()
	f.failure = context.DeadlineExceeded
	f.mu.Unlock()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(3 * time.Second)
	for !nc.IsClosed() {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("IAM outage left an established connection open")
		}
	}
	for len(runtime.redeemer.snapshot()) > 0 {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("closed connection was not reclaimed")
		}
	}
}
