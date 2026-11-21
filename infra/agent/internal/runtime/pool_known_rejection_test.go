package runtime_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type signedAdmissionAuthority struct {
	*admissionAuthority
	key ed25519.PrivateKey
}

func (a *signedAdmissionAuthority) BeginSharedTask(ctx context.Context, in *iamv1.BeginSharedTaskRequest) (*iamv1.BeginSharedTaskResponse, error) {
	out, err := a.admissionAuthority.BeginSharedTask(ctx, in)
	if err != nil {
		return nil, err
	}
	claims, err := sharednode.HostValue(out.Capability)
	if err != nil {
		return nil, err
	}
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "fixture", "typ": "auriga-shared-capability+jwt"})
	body, _ := json.Marshal(claims)
	text := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	out.Token = text + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(a.key, []byte(text)))
	return out, nil
}

// The public Host production ports, Relay, durable gate, Engine sampling and
// actual same-connection mTLS NodeControl fixture all participate. Only the
// authorities and Engine are synthetic; no DSH/container process is started.
func TestKnownRejectionActualHostCatchAndNextTurn(t *testing.T) {
	m, engine, _ := persistentFixture(t)
	a := &admissionAuthority{t: t, bindings: map[string]*nodev1.SharedDshSessionBinding{}}
	bindings := make([]any, 0, 5)
	for i := 0; i < 5; i++ {
		command := persistentIntent(1)
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 700+i)
		command.OperationId, command.Binding.MemberId, command.Binding.PlacementId = id, id, id
		if err := m.View(t.Context(), persistentView(command)); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Prepare(t.Context(), command); err != nil {
			t.Fatal(err)
		}
		b := command.Binding
		session := &nodev1.SharedDshSessionBinding{TenantId: b.TenantId, MemberId: b.MemberId, PlacementId: b.PlacementId, PhysicalInstanceId: b.PhysicalInstanceId,
			Mode: b.Mode, Epoch: b.Epoch, Revision: b.Revision, SessionId: id, PlatformSessionId: id, AuthGeneration: 1}
		a.bindings[id] = session
		value, err := sharednode.HostValue(session)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, value)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	workspacev1.RegisterSharedDshServiceServer(server, a)
	iamv1.RegisterSharedTaskServiceServer(server, &signedAdmissionAuthority{a, private})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///known-rejection", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tokenFile := filepath.Join(t.TempDir(), "service-token")
	if err = os.WriteFile(tokenFile, []byte("synthetic-admission-token"), 0600); err != nil {
		t.Fatal(err)
	}
	b := a.bindings["00000000-0000-4000-8000-000000000700"]
	client := &sharednode.Client{Settings: sharednode.Settings{TenantID: b.TenantId, ServiceTokenFile: tokenFile}, Workspace: conn, IAM: conn}
	gate := &agentruntime.PoolTaskAdmission{Pools: m.Pools, Driver: m.Driver, TenantID: b.TenantId}
	control := startPoolTaskControl(t, m, gate, a)
	relay := sharednode.NewRelay(client, b.PhysicalInstanceId)
	relay.Admission = gate
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/fixture/memory" {
			engine.mu.Lock()
			defer engine.mu.Unlock()
			if r.URL.RawQuery == "closed" {
				const limit = int64(512 * 1024 * 1024)
				engine.memoryStats = map[string]any{"read": time.Now().UTC().Format(time.RFC3339Nano), "memory_stats": map[string]any{"usage": limit - limit/5, "limit": limit}}
			} else {
				engine.memoryStats = nil
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		relay.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"bindings": bindings, "publicKey": base64.StdEncoding.EncodeToString(der)})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "testdata/known-rejection-host.mjs", endpoint.URL, string(raw))
	out, err := command.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal("actual Host catch/Finish/next turn failed", err)
	}
	// Even a proven local rejection needs both authorities. Old empty IAM or
	// W success and unknown results cannot become an exact Host ACK.
	engine.mu.Lock()
	const limit = int64(512 * 1024 * 1024)
	engine.memoryStats = map[string]any{"read": time.Now().UTC().Format(time.RFC3339Nano), "memory_stats": map[string]any{"usage": limit - limit/5, "limit": limit}}
	engine.mu.Unlock()
	const rejectedID = "00000000-0000-4000-8000-000000000900"
	input := map[string]any{"binding": admissionBinding(t, b), "taskId": rejectedID, "scopes": []string{"task.execute"}, "audience": "auriga.shared.enterprise"}
	a.emptyFinish.Store(true)
	if code, _ := admissionCall(t, endpoint.URL, "beginTask", input); code == 200 {
		t.Fatal("closed memory gate admitted")
	}
	finish := map[string]any{"binding": admissionBinding(t, b), "taskId": rejectedID}
	if code, _ := admissionCall(t, endpoint.URL, "finishTask", finish); code == 200 {
		t.Fatal("empty IAM Finish proved a rejection terminal")
	}
	a.emptyFinish.Store(false)
	control.releaseMode.Store(1)
	relay.RetryFinishes()
	if code, _ := admissionCall(t, endpoint.URL, "finishTask", finish); code == 200 {
		t.Fatal("empty W Release proved a rejection terminal")
	}
	control.releaseMode.Store(0)
	relay.RetryFinishes()
	if code, value := admissionCall(t, endpoint.URL, "finishTask", finish); code != 200 || value["fenced"] != true || value["taskId"] != rejectedID {
		t.Fatal("confirmed rejection did not retain exact Finish", code, value)
	}
	if code, _ := admissionCall(t, endpoint.URL, "finishTask", map[string]any{"binding": admissionBinding(t, b), "taskId": "00000000-0000-4000-8000-000000000901"}); code == 200 {
		t.Fatal("unregistered UUID acquired an ACK")
	}
	engine.mu.Lock()
	engine.memoryStats = nil
	engine.mu.Unlock()
	before := a.beginCount.Load()
	restarted := sharednode.NewRelay(client, b.PhysicalInstanceId)
	restarted.Admission = gate
	restartServer := httptest.NewServer(restarted)
	t.Cleanup(restartServer.Close)
	if code, _ := admissionCall(t, restartServer.URL, "beginTask", input); code == 200 || a.beginCount.Load() != before {
		t.Fatal("lost local ACK cache replayed a rejected UUID past W tombstone")
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if len(control.active) != 0 || len(control.ended) < 10 || control.releases.Load() < 10 {
		t.Fatal("Host finish did not reach exact W tombstones", len(control.active), len(control.ended))
	}
}
