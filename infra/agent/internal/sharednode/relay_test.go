package sharednode

import (
	"context"
	"crypto/tls"
	"encoding/json"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testTenant = "00000000-0000-4000-8000-000000000001"
const testPool = "00000000-0000-4000-8000-000000000002"
const testSession = "00000000-0000-4000-8000-000000000003"
const testTask = "00000000-0000-4000-8000-000000000004"
const testOrdinary = "00000000-0000-4000-8000-000000000099"

// Unrelated capability/transport unit tests isolate the admission boundary.
// Production budget and Engine sampling are exercised by the runtime relay tests.
type unitAdmission struct{}

func (*unitAdmission) Admit(context.Context, *nodev1.SharedDshSessionBinding, string) (bool, error) {
	return true, nil
}
func (*unitAdmission) Check(*nodev1.SharedDshSessionBinding, string) error    { return nil }
func (*unitAdmission) Complete(*nodev1.SharedDshSessionBinding, string) error { return nil }

type testAuthority struct {
	workspacev1.UnimplementedSharedDshServiceServer
	iamv1.UnimplementedSharedTaskServiceServer
	t               *testing.T
	binding         *nodev1.SharedDshSessionBinding
	version         atomic.Int64
	stopped         sync.Map
	ordinaryVersion atomic.Int64
	credentialState atomic.Int32
	invalidSession  atomic.Bool
	lookupFailures  sync.Map
	beginStarted    chan struct{}
	beginResume     chan struct{}
	issueStarted    chan struct{}
	issueResume     chan struct{}
	outbound        chan *iamv1.ExecuteSharedOutboundRequest
}

func (a *testAuthority) ExecuteSharedOutbound(ctx context.Context, in *iamv1.ExecuteSharedOutboundRequest) (*iamv1.ExecuteSharedOutboundResponse, error) {
	a.check(ctx)
	if a.outbound != nil {
		a.outbound <- in
	}
	if in.Url == "https://business.example.com/cancel" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &iamv1.ExecuteSharedOutboundResponse{StatusCode: 201, Headers: map[string]string{"content-type": "application/json"}, Body: `{"ok":true}`}, nil
}

func TestEnterpriseOutboundBoundClaimsAndCancellation(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.enterprise", []string{"outbound"})
	a.outbound = make(chan *iamv1.ExecuteSharedOutboundRequest, 4)
	claims, _ := wire(r.tokens["synthetic-capability-1"])
	in := map[string]any{"capability": claims, "url": "https://business.example.com/path", "method": "POST", "headers": map[string]string{"content-type": "application/json"}, "bodyBase64": "e30=", "timeoutMs": 60000}
	raw, _ := json.Marshal(in)
	value, e := r.invoke(context.Background(), "enterpriseOutbound", raw)
	if e != nil {
		t.Fatal("seventh RPC not wired", e)
	}
	if value.(map[string]any)["statusCode"] != float64(201) {
		t.Fatal(value)
	}
	request := <-a.outbound
	if string(request.Body) != "{}" || request.TimeoutMs != 60000 {
		t.Fatal("base64/timeout lost")
	}
	in["bodyBase64"] = "e3\n0="
	raw, _ = json.Marshal(in)
	if _, e = r.invoke(context.Background(), "enterpriseOutbound", raw); e == nil {
		t.Fatal("noncanonical base64 accepted")
	}
	in["bodyBase64"] = ""
	in["url"] = "https://business.example.com/cancel"
	raw, _ = json.Marshal(in)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.invoke(ctx, "enterpriseOutbound", raw); done <- err }()
	<-a.outbound
	cancel()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("canceled outbound succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel not propagated to IAM")
	}
}

func (a *testAuthority) check(ctx context.Context) {
	a.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	if strings.Join(md.Get("authorization"), ",") != "Bearer synthetic-service-token" || strings.Join(md.Get("auriga-tenant-id"), ",") != testTenant {
		a.t.Error("internal caller metadata missing")
	}
}
func (a *testAuthority) LookupSharedDshSession(ctx context.Context, in *workspacev1.LookupSharedDshSessionRequest) (*workspacev1.LookupSharedDshSessionResponse, error) {
	a.check(ctx)
	if failure, ok := a.lookupFailures.Load(in.SessionId); ok {
		return nil, failure.(error)
	}
	if a.invalidSession.Load() {
		return &workspacev1.LookupSharedDshSessionResponse{}, nil
	}
	return &workspacev1.LookupSharedDshSessionResponse{Binding: a.binding}, nil
}

func (a *testAuthority) GetSharedDshPool(ctx context.Context, in *workspacev1.GetSharedDshPoolRequest) (*workspacev1.GetSharedDshPoolResponse, error) {
	a.check(ctx)
	out := &workspacev1.GetSharedDshPoolResponse{TenantId: testTenant, PhysicalInstanceId: in.PhysicalInstanceId}
	if in.PhysicalInstanceId == testPool {
		out.Registered = true
		out.Mode = "dedicated"
		out.State = "ready"
	}
	return out, nil
}
func (a *testAuthority) BeginSharedTask(ctx context.Context, in *iamv1.BeginSharedTaskRequest) (*iamv1.BeginSharedTaskResponse, error) {
	a.check(ctx)
	if a.beginStarted != nil {
		a.beginStarted <- struct{}{}
		<-a.beginResume
	}
	token := "synthetic-capability-1"
	if in.TaskId != testTask {
		token = "synthetic-capability-" + in.TaskId
	}
	return &iamv1.BeginSharedTaskResponse{Token: token, Capability: a.cap(in.TaskId, in.Audience, in.Scopes)}, nil
}
func (a *testAuthority) cap(id, audience string, scopes []string) *nodev1.SharedDshCapability {
	version := a.version.Load()
	if has(scopes, "task.execute") {
		version = a.ordinaryVersion.Load()
	}
	return &nodev1.SharedDshCapability{SchemaVersion: 1, Binding: a.binding, TaskId: id, Audience: audience, Scopes: scopes, CredentialVersion: version, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 60, Jti: id}
}
func (a *testAuthority) IssueSharedCapability(ctx context.Context, in *iamv1.IssueSharedCapabilityRequest) (*iamv1.IssueSharedCapabilityResponse, error) {
	a.check(ctx)
	if a.issueStarted != nil {
		a.issueStarted <- struct{}{}
		<-a.issueResume
	}
	return &iamv1.IssueSharedCapabilityResponse{Token: "synthetic-capability-refreshed", Capability: a.cap(in.TaskId, in.Audience, in.Scopes)}, nil
}
func (a *testAuthority) AuthorizeSharedCapability(ctx context.Context, in *iamv1.AuthorizeSharedCapabilityRequest) (*iamv1.AuthorizeSharedCapabilityResponse, error) {
	a.check(ctx)
	_, stopped := a.stopped.Load(in.Capability.TaskId)
	state := iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED
	if in.Capability.Audience == "auriga.shared.enterprise" {
		state = iamv1.SharedEnterpriseCredentialState(a.credentialState.Load())
	}
	version := a.version.Load()
	if has(in.Capability.Scopes, "task.execute") {
		version = a.ordinaryVersion.Load()
	}
	scopes := in.Capability.Scopes
	if in.Capability.Audience == "auriga.shared.enterprise" {
		scopes = []string{"task.execute", "credentials", "outbound"}
	}
	return &iamv1.AuthorizeSharedCapabilityResponse{Active: !stopped, CredentialVersion: version, Binding: a.binding, Scopes: scopes, EnterpriseCredentialState: state}, nil
}
func (a *testAuthority) FinishSharedTask(ctx context.Context, in *iamv1.FinishSharedTaskRequest) (*iamv1.FinishSharedTaskResponse, error) {
	a.check(ctx)
	a.stopped.Store(in.TaskId, true)
	return &iamv1.FinishSharedTaskResponse{}, nil
}
func fixture(t *testing.T) (*Relay, *testAuthority) {
	t.Helper()
	b := &nodev1.SharedDshSessionBinding{TenantId: testTenant, MemberId: testSession, PlacementId: testSession, PhysicalInstanceId: testPool, SessionId: testSession, PlatformSessionId: testSession, Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED, Epoch: 1, Revision: 1, AuthGeneration: 1}
	authority := &testAuthority{t: t, binding: b}
	authority.version.Store(1)
	authority.ordinaryVersion.Store(1)
	authority.credentialState.Store(int32(iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE))
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	workspacev1.RegisterSharedDshServiceServer(server, authority)
	iamv1.RegisterSharedTaskServiceServer(server, authority)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, e := grpc.NewClient("passthrough:///synthetic", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = conn.Close() })
	token := filepath.Join(t.TempDir(), "service-token")
	if e = os.WriteFile(token, []byte("synthetic-service-token"), 0600); e != nil {
		t.Fatal(e)
	}
	relay := NewRelay(&Client{Settings: Settings{TenantID: testTenant, ServiceTokenFile: token}, Workspace: conn, IAM: conn}, testPool)
	relay.Admission = &unitAdmission{}
	return relay, authority
}
func begin(t *testing.T, r *Relay, a *testAuthority, audience string, scopes []string) {
	t.Helper()
	b, _ := wire(a.binding)
	in := map[string]any{"binding": b, "taskId": testTask, "audience": audience, "scopes": scopes}
	if audience != "auriga.shared.enterprise" || !sameStrings(scopes, []string{"task.execute"}) {
		root, _ := json.Marshal(map[string]any{"binding": b, "taskId": testOrdinary, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
		if _, err := r.invoke(context.Background(), "beginTask", root); err != nil {
			t.Fatal(err)
		}
		in["parentCapability"] = "synthetic-capability-" + testOrdinary
	}
	raw, _ := json.Marshal(in)
	if _, e := r.invoke(context.Background(), "beginTask", raw); e != nil {
		t.Fatal(e)
	}
}
func TestRelayActualRPCRefreshRemainsBoundToLiveExecution(t *testing.T) {
	r, a := fixture(t)
	if registered, e := r.Client.IsShared(context.Background(), testPool); e != nil || !registered {
		t.Fatal("node registration authority not installed", e)
	}
	begin(t, r, a, "auriga.shared.enterprise", []string{"credentials"})
	if _, e := r.capability(context.Background(), "synthetic-capability-1"); e != nil {
		t.Fatal(e)
	}
	a.version.Store(2)
	if _, e := r.capability(context.Background(), "synthetic-capability-1"); e == nil {
		t.Fatal("stale direct authorization accepted")
	}
	binding, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"credentials"}})
	if _, e := r.invoke(context.Background(), "issueCapability", raw); e != nil {
		t.Fatal("same running task refresh", e)
	}
	if _, e := r.capability(context.Background(), "synthetic-capability-refreshed"); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(map[string]any{"binding": binding, "taskId": testTask})
	if _, e := r.invoke(context.Background(), "finishTask", raw); e != nil {
		t.Fatal(e)
	}
	if _, e := r.capability(context.Background(), "synthetic-capability-refreshed"); e == nil {
		t.Fatal("finished task capability accepted")
	}
}
func TestRelayModelFixedMTLSHeadersAndSSEToPublicNDJSON(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.model", []string{"model.invoke"})
	calls := atomic.Int32{}
	n := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.TLS == nil || len(req.TLS.PeerCertificates) == 0 {
			t.Error("node client certificate missing")
		}
		calls.Add(1)
		if req.URL.Path != "/internal/shared/model/invoke" || req.Method != "POST" || req.Header.Get("Authorization") != "" || req.Header.Get("Auriga-Tenant-Id") != "" {
			t.Error("model route or identity leakage")
		}
		for header, value := range map[string]string{"Auriga-Shared-Capability": "synthetic-capability-1", "Auriga-Shared-Session-Id": testSession, "Auriga-Shared-Task-Id": testTask, "Idempotency-Key": testTask} {
			if len(req.Header.Values(header)) != 1 || req.Header.Get(header) != value {
				t.Error("model binding header", header)
			}
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil || body["max_tokens"] != float64(16) || body["stream"] != true || body["tenantId"] != nil {
			t.Error("not an OpenAI body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	n.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	n.StartTLS()
	defer n.Close()
	r.Client.Model = n.Client()
	r.Client.Model.Transport.(*http.Transport).TLSClientConfig.Certificates = n.TLS.Certificates
	r.Client.Settings.SharedModel = &Endpoint{Address: n.URL}
	raw, _ := json.Marshal(map[string]any{"tenantId": testTenant, "sessionId": testSession, "taskId": testTask, "callId": testTask, "capability": "synthetic-capability-1", "body": map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "synthetic"}}}}, "maxTokens": 16}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/_auriga/shared-rpc/model", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Header().Get("Content-Type") != "application/x-ndjson" || calls.Load() != 1 {
		t.Fatal("stream not connected", w.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 5 {
		t.Fatal("not public chunks", w.Body.String())
	}
	var last map[string]any
	if json.Unmarshal([]byte(lines[4]), &last) != nil || last["type"] != "finish" {
		t.Fatal(w.Body.String())
	}
}

func TestRelayRejectsAmbiguousAndUnboundedJSON(t *testing.T) {
	for _, raw := range []string{`{"capability":"a","capability":"b"}`, `{"capability":"a","serviceToken":"injected"}`, `null`, strings.Repeat("[", 65) + strings.Repeat("]", 65)} {
		if validRequest([]byte(raw), "authorizeCapability") {
			t.Fatal("ambiguous/extra carrier accepted", raw)
		}
	}
	if !validRequest([]byte(`{"capability":"a"}`), "authorizeCapability") {
		t.Fatal("valid carrier rejected")
	}
}

func TestModelCancellationClosesUpstreamWithoutReplay(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.model", []string{"model.invoke"})
	entered, closed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	n := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-req.Context().Done()
		close(closed)
	}))
	defer n.Close()
	r.Client.Model = n.Client()
	r.Client.Settings.SharedModel = &Endpoint{Address: n.URL}
	raw, _ := json.Marshal(map[string]any{"tenantId": testTenant, "sessionId": testSession, "taskId": testTask, "callId": testTask, "capability": "synthetic-capability-1", "body": map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "cancel"}}}}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = r.model(ctx, httptest.NewRecorder(), raw); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model not connected")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("node stream did not exit")
	}
	if calls.Load() != 1 {
		t.Fatal("canceled call replayed")
	}
}
func TestSSEResponseRejectsTruncationAndPreservesToolArguments(t *testing.T) {
	if readSSE(strings.NewReader("data: {}\n\n"), func(string) error { return nil }) == nil {
		t.Fatal("truncated SSE accepted")
	}
	chunks := []map[string]any{}
	state := newResponseState("m", func(v any) error { chunks = append(chunks, v.(map[string]any)); return nil })
	events := []string{`{"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{\"x\":"}}]},"finish_reason":null}]}`, `{"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`}
	for _, v := range events {
		if e := state.accept([]byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	if e := state.complete(); e != nil {
		t.Fatal(e)
	}
	if chunks[2]["argumentsDelta"] != "1}" || chunks[3]["block"].(map[string]any)["arguments"] != `{"x":1}` {
		t.Fatal("tool fragments lost", chunks)
	}
}

func TestModelJSONCompletionBecomesPublicNDJSON(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.model", []string{"model.invoke"})
	var calls atomic.Int32
	n := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Header.Get("Idempotency-Key") != testTask {
			t.Error("call identity lost")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"tool1","type":"function","function":{"name":"lookup","arguments":"{\"n\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer n.Close()
	r.Client.Model, r.Client.Settings.SharedModel = n.Client(), &Endpoint{Address: n.URL}
	raw, _ := json.Marshal(map[string]any{"tenantId": testTenant, "sessionId": testSession, "taskId": testTask, "callId": testTask, "capability": "synthetic-capability-1", "body": map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "synthetic"}}}}}})
	w := httptest.NewRecorder()
	if err := r.model(context.Background(), w, raw); err != nil {
		t.Fatal("JSON completion not connected", err)
	}
	if calls.Load() != 1 || !strings.Contains(w.Body.String(), `"kind":"tool-calls"`) || !strings.Contains(w.Body.String(), `"totalTokens":3`) || !strings.Contains(w.Body.String(), `"arguments":"{\"n\":1}"`) {
		t.Fatal("completion/tool/usage lost", w.Body.String())
	}
}
func TestRelayRejectsOtherPoolAndServiceHeaders(t *testing.T) {
	r, a := fixture(t)
	wrong := proto.Clone(a.binding).(*nodev1.SharedDshSessionBinding)
	wrong.PhysicalInstanceId = testSession
	if r.session(context.Background(), wrong) == nil {
		t.Fatal("other pool accepted")
	}
	req := httptest.NewRequest("POST", "/_auriga/shared-rpc/health", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer injected")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatal("service credential carrier accepted")
	}
}

func TestHostClaimsCarrierStaysOnOriginalProcessExecution(t *testing.T) {
	r, a := fixture(t)
	peerContext := context.WithValue(context.Background(), executionPeerKey{}, "signed-container/host-pid/start")
	b, _ := wire(a.binding)
	root, _ := json.Marshal(map[string]any{"binding": b, "taskId": testOrdinary, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
	if _, err := r.invoke(peerContext, "beginTask", root); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}, "parentCapability": "synthetic-capability-" + testOrdinary})
	if _, err := r.invoke(peerContext, "beginTask", raw); err != nil {
		t.Fatal(err)
	}
	claims, _ := wire(r.tokens["synthetic-capability-1"])
	raw, _ = json.Marshal(map[string]any{"capability": claims})
	if _, err := r.invoke(peerContext, "authorizeCapability", raw); err != nil {
		t.Fatal("actual Host claims object rejected", err)
	}
	other := context.WithValue(context.Background(), executionPeerKey{}, "signed-container/other-pid/start")
	if _, err := r.invoke(other, "authorizeCapability", raw); err == nil {
		t.Fatal("another process borrowed task capability")
	}
	claims.(map[string]any)["taskId"] = testPool
	raw, _ = json.Marshal(map[string]any{"capability": claims})
	if _, err := r.invoke(peerContext, "authorizeCapability", raw); err == nil {
		t.Fatal("forged claims accepted")
	}
}
