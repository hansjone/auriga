package sharedhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/bifrost"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	node "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type iamPeer struct {
	iam.UnimplementedSharedTaskServiceServer
	f            *fixture
	serviceToken atomic.Value
	checks       atomic.Int32
}

func (p *iamPeer) ResolveSharedTask(ctx context.Context, q *iam.ResolveSharedTaskRequest) (*iam.ResolveSharedTaskResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if tenant := md.Get("auriga-tenant-id"); len(tenant) != 1 || tenant[0] != p.f.tenant || q.TenantId != p.f.tenant {
		return nil, status.Error(codes.PermissionDenied, "common.forbidden")
	}
	if authorization := md.Get("authorization"); len(authorization) != 1 || authorization[0] != "Bearer "+p.serviceToken.Load().(string) {
		return nil, status.Error(codes.Unauthenticated, "common.unauthenticated")
	}
	// All invoked/rechecked JTI lookups must preserve the HTTP trace through
	// the real RPC transport. The fixture issuer has its own offline gate.
	if q.Jti != "" {
		if traces := md.Get("traceparent"); len(traces) != 1 || !observability.Valid(traces[0]) {
			return nil, status.Error(codes.InvalidArgument, "missing correlation")
		}
	}
	p.checks.Add(1)
	p.f.a.mu.Lock()
	defer p.f.a.mu.Unlock()
	b, ok := p.f.a.bindings[q.SessionId]
	if !ok || p.f.a.revoked {
		return &iam.ResolveSharedTaskResponse{}, nil
	}
	return &iam.ResolveSharedTaskResponse{Active: true, CredentialVersion: 1, Scopes: []string{"model.invoke"}, Binding: &node.SharedDshSessionBinding{TenantId: b.TenantID, MemberId: b.MemberID, PlacementId: b.PlacementID, PhysicalInstanceId: b.PhysicalInstanceID, Mode: node.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: b.Epoch, Revision: b.Revision, SessionId: b.SessionID, PlatformSessionId: b.PlatformSessionID, AuthGeneration: b.AuthGeneration}}, nil
}

func TestSharedModelHTTPRealIAMRPCAndMemberRelayComposition(t *testing.T) {
	f := newFixture(t, nil)
	dir := t.TempDir()
	password := uuid.NewString()
	passwordFile := filepath.Join(dir, "relay-password")
	if err := os.WriteFile(passwordFile, []byte(password), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "auriga-member" || pass != password {
			t.Error("bad installation relay credentials")
			w.WriteHeader(401)
			return
		}
		if !observability.Valid(r.Header.Get("traceparent")) {
			t.Error("HTTP trace did not reach existing relay")
		}
		if r.URL.Path == "/internal/members/ensure" {
			var input api.RelayMemberEnsure
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.TenantId != f.tenant {
				t.Error("wrong member tenant")
				w.WriteHeader(403)
				return
			}
			identity, _ := json.Marshal([]string{input.TenantId, input.AccountId})
			digest := sha256.Sum256(identity)
			if err := json.NewEncoder(w).Encode(api.RelayKeyIssued{Key: api.RelayKey{Id: input.AccountId, AccountId: input.AccountId, Name: "auriga-member:" + hex.EncodeToString(digest[:]), Source: "member", Active: true}, Secret: "sk-bf-" + input.AccountId}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(404)
			return
		}
		member := strings.TrimPrefix(r.Header.Get("X-Bf-Vk"), "sk-bf-")
		var input struct {
			User     string
			Metadata map[string]string
			Stream   bool
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.User != f.tenant+":"+member || input.Metadata["account_id"] != member || !input.Stream || !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "shared-") {
			t.Error("shared identity was not attributed by existing relay")
		}
		mu.Lock()
		seen[member]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	peer := &iamPeer{f: f}
	serviceToken := uuid.NewString()
	peer.serviceToken.Store(serviceToken)
	tokenFile := filepath.Join(dir, "service-token")
	if err := os.WriteFile(tokenFile, []byte(serviceToken), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	iam.RegisterSharedTaskServiceServer(grpcServer, peer)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	defer func() { _ = listener.Close() }()
	connection, err := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	gate, err := sharedauth.NewRPCGate(iam.NewSharedTaskServiceClient(connection), map[string]ed25519.PublicKey{"model": f.private.Public().(ed25519.PublicKey)}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := bifrost.NewSharedRelay(upstream.URL, passwordFile, bifrost.MemberPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	provider, err := sharedmodel.NewSharedProvider(gate, relay, f.l)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(f.tenant, []string{f.node}, provider)
	if err != nil {
		t.Fatal(err)
	}
	tlsFixture := newTLSFixture(t)
	serverTLS, err := TLSConfig(tlsFixture.files, []string{f.node})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(tlsFixture.caPEM)
	pair, _, _ := tlsFixture.issue(t, "auriga://node/"+f.node, false)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "example.com", Certificates: []tls.Certificate{pair}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for i := 0; i < 2; i++ {
		r := f.request(t, true)
		r.RequestURI = ""
		r.TLS = nil
		r.URL, err = url.Parse(server.URL + InvokePath)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = r.URL.Host
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "[DONE]") || !strings.Contains(string(raw), `"total_tokens":5`) {
			t.Fatal("composition did not complete", response.StatusCode, err, string(raw))
		}
		// Rotation is read by the existing authenticated RPC transport on every call.
		serviceToken = uuid.NewString()
		peer.serviceToken.Store(serviceToken)
		if err = os.WriteFile(tokenFile, []byte(serviceToken), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || f.l.finished.Load() != 2 || peer.checks.Load() < 6 {
		t.Fatal("missing per-member routing, online checks or settlement", seen, f.l.finished.Load(), peer.checks.Load())
	}
	for _, count := range seen {
		if count != 1 {
			t.Fatal("member dispatched more than once")
		}
	}
}
