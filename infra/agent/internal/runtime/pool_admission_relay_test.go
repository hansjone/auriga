package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type admissionAuthority struct {
	workspacev1.UnimplementedSharedDshServiceServer
	iamv1.UnimplementedSharedTaskServiceServer
	t                                            *testing.T
	bindings                                     map[string]*nodev1.SharedDshSessionBinding
	stopped                                      sync.Map
	beginCount                                   atomic.Int32
	failBegin, failFinish, invalid, unknownBegin atomic.Bool
	emptyFinish                                  atomic.Bool
	running                                      sync.Map
}

func (a *admissionAuthority) check(ctx context.Context) {
	a.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	if strings.Join(md.Get("authorization"), ",") != "Bearer synthetic-admission-token" || strings.Join(md.Get("auriga-tenant-id"), ",") != persistentIntent(1).Binding.TenantId {
		a.t.Error("production RPC caller omitted fixed installation identity")
	}
}
func (a *admissionAuthority) LookupSharedDshSession(ctx context.Context, in *workspacev1.LookupSharedDshSessionRequest) (*workspacev1.LookupSharedDshSessionResponse, error) {
	a.check(ctx)
	if a.invalid.Load() {
		return nil, status.Error(codes.Unauthenticated, "fixture-private-revoked")
	}
	b := a.bindings[in.SessionId]
	if b == nil || b.TenantId != in.TenantId || b.PhysicalInstanceId != in.PhysicalInstanceId {
		return nil, status.Error(codes.NotFound, "common.not_found")
	}
	return &workspacev1.LookupSharedDshSessionResponse{Binding: b}, nil
}
func (a *admissionAuthority) BeginSharedTask(ctx context.Context, in *iamv1.BeginSharedTaskRequest) (*iamv1.BeginSharedTaskResponse, error) {
	a.check(ctx)
	a.beginCount.Add(1)
	if a.unknownBegin.Load() {
		return nil, status.Error(codes.Unavailable, "fixture-private-unknown-outcome")
	}
	if a.failBegin.Load() {
		return nil, status.Error(codes.PermissionDenied, "fixture-private-begin")
	}
	cap := &nodev1.SharedDshCapability{SchemaVersion: 1, Binding: in.Binding, TaskId: in.TaskId, Audience: in.Audience, Scopes: in.Scopes,
		CredentialVersion: 1, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 60, Jti: in.TaskId}
	a.running.Store(in.TaskId, proto.Clone(in.Binding))
	return &iamv1.BeginSharedTaskResponse{Token: "synthetic-" + in.TaskId, Capability: cap}, nil
}
func (a *admissionAuthority) FinishSharedTask(ctx context.Context, in *iamv1.FinishSharedTaskRequest) (*iamv1.FinishSharedTaskResponse, error) {
	a.check(ctx)
	if a.failFinish.Load() {
		return nil, status.Error(codes.Unavailable, "fixture-private-finish")
	}
	if a.emptyFinish.Load() {
		return &iamv1.FinishSharedTaskResponse{}, nil
	}
	a.stopped.Store(in.TaskId, true)
	a.running.Delete(in.TaskId)
	return &iamv1.FinishSharedTaskResponse{Fenced: true, Binding: in.Binding, TaskId: in.TaskId}, nil
}
func (a *admissionAuthority) AuthorizeSharedCapability(ctx context.Context, in *iamv1.AuthorizeSharedCapabilityRequest) (*iamv1.AuthorizeSharedCapabilityResponse, error) {
	a.check(ctx)
	_, stopped := a.stopped.Load(in.Capability.TaskId)
	return &iamv1.AuthorizeSharedCapabilityResponse{Active: !stopped, Binding: in.Capability.Binding, CredentialVersion: 1,
		Scopes: in.Capability.Scopes, EnterpriseCredentialState: iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE}, nil
}

func admissionBinding(t *testing.T, b *nodev1.SharedDshSessionBinding) map[string]any {
	t.Helper()
	raw, err := protojson.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["mode"] = "shared"
	return value
}

func admissionCall(t *testing.T, origin, op string, input any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(origin+"/_auriga/shared-rpc/"+op, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, value
}

func TestRelayProductionAdmissionUsesPersistentBudgetAndEngine(t *testing.T) {
	m, engine, _ := persistentFixture(t)
	a := &admissionAuthority{t: t, bindings: map[string]*nodev1.SharedDshSessionBinding{}}
	bindings := []*nodev1.SharedDshSessionBinding{}
	for i := 0; i < 5; i++ {
		command := persistentIntent(1)
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100+i)
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
		bindings = append(bindings, session)
		a.bindings[id] = session
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	workspacev1.RegisterSharedDshServiceServer(server, a)
	iamv1.RegisterSharedTaskServiceServer(server, a)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///admission-fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tokenFile := filepath.Join(t.TempDir(), "service-token")
	if err := os.WriteFile(tokenFile, []byte("synthetic-admission-token"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &sharednode.Client{Settings: sharednode.Settings{TenantID: bindings[0].TenantId, ServiceTokenFile: tokenFile}, Workspace: conn, IAM: conn}
	samples := make(chan *nodev1.SharedDshPoolMemorySample, 32)
	controller := &agentruntime.PoolTaskAdmission{Pools: m.Pools, Driver: m.Driver, TenantID: bindings[0].TenantId, Report: func(v *nodev1.SharedDshPoolMemorySample) { samples <- v }}
	control := startPoolTaskControl(t, m, controller, a)
	relay := sharednode.NewRelay(client, bindings[0].PhysicalInstanceId)
	relay.Admission = controller
	httpServer := httptest.NewServer(relay)
	t.Cleanup(httpServer.Close)
	taskID := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 500+n) }
	begin := func(member, task int, parent string, scopes []string) (int, map[string]any) {
		input := map[string]any{"binding": admissionBinding(t, bindings[member]), "taskId": taskID(task), "audience": "auriga.shared.enterprise", "scopes": scopes}
		if parent != "" {
			input["parentCapability"] = parent
		}
		return admissionCall(t, httpServer.URL, "beginTask", input)
	}
	finish := func(member, task int) int {
		code, value := admissionCall(t, httpServer.URL, "finishTask", map[string]any{"binding": admissionBinding(t, bindings[member]), "taskId": taskID(task)})
		if code == 200 {
			echo, ok := value["binding"].(map[string]any)
			if len(value) != 3 || value["fenced"] != true || value["taskId"] != taskID(task) || !ok || echo["mode"] != "shared" {
				t.Fatal("HTTP Finish did not emit the exact fenced contract", value)
			}
			for _, key := range []string{"epoch", "revision", "authGeneration"} {
				if _, ok := echo[key].(float64); !ok {
					t.Fatal("HTTP Finish binding did not use public numeric integers", key)
				}
			}
			echo["mode"] = "DSH_INSTANCE_MODE_SHARED"
			raw, err := json.Marshal(echo)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(nodev1.SharedDshSessionBinding)
			if err = protojson.Unmarshal(raw, decoded); err != nil || !proto.Equal(decoded, bindings[member]) {
				t.Fatal("HTTP Finish binding echo changed", err)
			}
		}
		return code
	}
	ordinary := []string{"task.execute"}
	code, root := begin(0, 0, "", ordinary)
	if code != 200 {
		t.Fatal("first actual admission failed", root)
	}
	select {
	case sample := <-samples:
		if sample.TenantId != bindings[0].TenantId || sample.MemoryLimitBytes != 512*1024*1024 || sample.MemoryUsedBytes != (512*1024*1024*79/100) {
			t.Fatal("production Engine sample not reported", sample)
		}
	default:
		t.Fatal("production admission omitted telemetry")
	}
	if code, _ := begin(0, 11, "", ordinary); code == 200 || a.beginCount.Load() != 1 {
		t.Fatal("second member execution reached IAM")
	}
	if code, _ := begin(0, 10, root["token"].(string), []string{"outbound", "credentials"}); code != 200 {
		t.Fatal("verified child double-counted the ordinary slot")
	}
	for i := 1; i < 4; i++ {
		if code, value := begin(i, i, "", ordinary); code != 200 {
			t.Fatal("pool slot rejected", i, value)
		}
	}
	if code, _ := begin(4, 4, "", ordinary); code == 200 || a.beginCount.Load() != 5 {
		t.Fatal("fifth ordinary reached IAM or child used a second slot")
	}
	if finish(0, 10) != 200 {
		t.Fatal("child Finish failed")
	}
	if code, _ := begin(4, 4, "", ordinary); code == 200 {
		t.Fatal("child Finish released its parent's slot")
	}
	if finish(0, 0) != 200 {
		t.Fatal("ordinary Finish failed")
	}
	if code, value := begin(4, 5, "", ordinary); code != 200 {
		t.Fatal("completed slot leaked", value)
	}
	if finish(4, 5) != 200 {
		t.Fatal("fifth Finish failed")
	}
	a.failBegin.Store(true)
	if code, _ := begin(0, 20, "", ordinary); code == 200 {
		t.Fatal("failed IAM Begin returned a task")
	}
	a.failBegin.Store(false)
	code, root = begin(0, 21, "", ordinary)
	if code != 200 {
		t.Fatal("failed Begin leaked its reservation", root)
	}
	a.failFinish.Store(true)
	if finish(0, 21) == 200 {
		t.Fatal("unknown Finish was declared complete")
	}
	if code, _ := begin(0, 22, "", ordinary); code == 200 {
		t.Fatal("unconfirmed Finish freed a slot")
	}
	if code, _ := admissionCall(t, httpServer.URL, "authorizeCapability", map[string]any{"capability": root["capability"]}); code == 200 {
		t.Fatal("closing task retained a local capability")
	}
	a.failFinish.Store(false)
	relay.RetryFinishes()
	if code, _ := begin(0, 31, "", ordinary); code != 200 {
		t.Fatal("confirmed Finish still leaked its slot")
	}
	if finish(0, 21) != 200 {
		t.Fatal("confirmed old Finish ACK was not replayable")
	}
	if code, _ := begin(0, 23, "", ordinary); code == 200 {
		t.Fatal("old Finish ACK released replacement root")
	}
	// New Node adapter/Relay cannot adopt persisted tasks, even using the same UUID.
	restarted := sharednode.NewRelay(client, bindings[0].PhysicalInstanceId)
	restarted.Admission = &agentruntime.PoolTaskAdmission{Pools: m.Pools, Driver: m.Driver, TenantID: bindings[0].TenantId, Control: controller.Control}
	restartServer := httptest.NewServer(restarted)
	t.Cleanup(restartServer.Close)
	input := map[string]any{"binding": admissionBinding(t, bindings[0]), "taskId": taskID(31), "scopes": ordinary, "audience": "auriga.shared.enterprise"}
	if code, _ := admissionCall(t, restartServer.URL, "beginTask", input); code == 200 {
		t.Fatal("restart adopted an unconfirmed persisted task")
	}
	if code, _ := admissionCall(t, restartServer.URL, "finishTask", map[string]any{"binding": admissionBinding(t, bindings[0]), "taskId": taskID(31)}); code == 200 {
		t.Fatal("same UUID restart rejection fenced another owner's task")
	}
	if code, _ := begin(0, 23, "", ordinary); code == 200 {
		t.Fatal("restart rejection cleared the old live slot")
	}
	if finish(0, 31) != 200 {
		t.Fatal("original exact owner cannot Finish")
	}
	engine.mu.Lock()
	const limit = int64(512 * 1024 * 1024)
	engine.memoryStats = map[string]any{"read": time.Now().UTC().Format(time.RFC3339Nano), "memory_stats": map[string]any{"usage": limit - limit/5, "limit": limit}}
	engine.mu.Unlock()
	if code, _ := begin(0, 24, "", ordinary); code == 200 {
		t.Fatal("80-percent exact byte boundary accepted")
	}
	engine.mu.Lock()
	engine.memoryStats = nil
	engine.mu.Unlock()
	code, finalRoot := begin(0, 25, "", ordinary)
	if code != 200 {
		t.Fatal("fresh memory did not restore admission")
	}
	a.invalid.Store(true)
	if code, _ := begin(4, 26, "", ordinary); code == 200 {
		t.Fatal("revoked platform identity became an admissible task")
	}
	a.invalid.Store(false)
	a.emptyFinish.Store(true)
	a.unknownBegin.Store(true)
	if code, _ := begin(0, 30, finalRoot["token"].(string), []string{"outbound"}); code == 200 {
		t.Fatal("unknown child Begin returned success")
	}
	a.unknownBegin.Store(false)
	if finish(0, 25) == 200 {
		t.Fatal("unknown child outcome released its parent's slot")
	}
	a.emptyFinish.Store(false)
	relay.RetryFinishes()
	for i := 1; i < 4; i++ {
		if finish(i, i) != 200 {
			t.Fatal("cleanup Finish failed")
		}
	}
	a.emptyFinish.Store(true)
	a.unknownBegin.Store(true)
	if code, _ := begin(4, 27, "", ordinary); code == 200 {
		t.Fatal("unknown remote Begin returned success")
	}
	a.unknownBegin.Store(false)
	relay.RetryFinishes()
	if code, _ := begin(4, 28, "", ordinary); code == 200 {
		t.Fatal("empty IAM Finish response proved an absent-task tombstone")
	}
	a.emptyFinish.Store(false)
	relay.RetryFinishes()
	if code, value := begin(4, 29, "", ordinary); code != 200 {
		t.Fatal("confirmed IAM tombstone/W Release did not restore member", value)
	}
	if finish(4, 29) != 200 {
		t.Fatal("typed final Finish failed")
	}
	if control.admits.Load() == 0 || control.releases.Load() == 0 || control.samples.Load() == 0 {
		t.Fatal("production caller omitted actual NodeControl field12/Admit/Release")
	}
	for mode := int32(1); mode <= 5; mode++ {
		t.Run(fmt.Sprintf("unconfirmed-W-Admit-%d", mode), func(t *testing.T) {
			control.admitMode.Store(mode)
			control.releaseMode.Store(1)
			before := a.beginCount.Load()
			id := 100 + int(mode)
			if code, _ := begin(0, id, "", ordinary); code == 200 {
				t.Fatal("unconfirmed W Admit returned handle")
			}
			if a.beginCount.Load() != before {
				t.Fatal("IAM Begin preceded W admission confirmation")
			}
			if code, _ := begin(0, id+50, "", ordinary); code == 200 {
				t.Fatal("unknown W reservation did not retain local slot")
			}
			if finish(0, id) == 200 {
				t.Fatal("empty W Release produced fenced UDS echo")
			}
			control.admitMode.Store(0)
			control.releaseMode.Store(0)
			relay.RetryFinishes()
			if finish(0, id) != 200 {
				t.Fatal("confirmed W tombstone was not echoed")
			}
			if code, _ := begin(0, id, "", ordinary); code == 200 {
				t.Fatal("ended W Admit UUID resurrected")
			}
		})
		t.Run(fmt.Sprintf("unconfirmed-W-Release-%d", mode), func(t *testing.T) {
			id := 200 + int(mode)
			if code, value := begin(0, id, "", ordinary); code != 200 {
				t.Fatal("next root rejected after exact W Release", value)
			}
			control.releaseMode.Store(mode)
			if finish(0, id) == 200 {
				t.Fatal("unconfirmed W Release produced UDS success")
			}
			if code, _ := begin(0, id+50, "", ordinary); code == 200 {
				t.Fatal("unknown Release freed local member")
			}
			control.releaseMode.Store(0)
			relay.RetryFinishes()
			if finish(0, id) != 200 {
				t.Fatal("confirmed Release retry did not complete")
			}
		})
	}
}
