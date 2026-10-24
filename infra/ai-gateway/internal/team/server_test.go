package team

import (
	"context"
	"encoding/json"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type wireCollab struct {
	collab.UnimplementedCollabCommunicationServiceServer
	calls atomic.Int32
}

func (s *wireCollab) OpenThread(_ context.Context, q *collab.OpenThreadRequest) (*collab.OpenThreadResponse, error) {
	s.calls.Add(1)
	if q.Actor.HostSessionId != "real-session" || q.Actor.AccountId != "account-from-iam" {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	return &collab.OpenThreadResponse{Message: &collab.Message{Id: "message", Text: q.Text}}, nil
}
func TestRealHTTPGRPCTransportAndAuthBoundaries(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &wireCollab{}
	rpc := grpc.NewServer()
	collab.RegisterCollabCommunicationServiceServer(rpc, backend)
	go func() { _ = rpc.Serve(listener) }()
	t.Cleanup(rpc.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	contract, err := os.ReadFile("../../../../contracts/plugin/team-mcp.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	var authorities atomic.Int32
	server, err := New(collab.NewCollabCommunicationServiceClient(conn), func(_ context.Context, token string) (*collab.Actor, error) {
		authorities.Add(1)
		if token != "test-device" || revoked.Load() {
			return nil, status.Error(codes.Unauthenticated, "rejected")
		}
		return &collab.Actor{TenantId: "tenant-from-iam", AccountId: "account-from-iam", CredentialKind: "DEVICE", CredentialSubject: "device-from-iam"}, nil
	}, contract)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(Deadline(server))
	defer httpServer.Close()
	send := func(path, body, token string, origin bool) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("POST", httpServer.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Auriga-Endpoint-Id", "endpoint")
		req.Header.Set("Auriga-Binding-Id", "binding")
		if origin {
			req.Header.Set("Origin", "null")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var value map[string]any
		_ = json.NewDecoder(res.Body).Decode(&value)
		return res.StatusCode, value
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"auriga_open_thread","arguments":{"targetEndpointId":"018f6f20-8f00-7000-8000-000000000001","text":"hello","idempotencyKey":"k"},"_meta":{"auriga/sessionId":"real-session","auriga/toolCallId":"call","auriga/rootCallId":"root"}}}`
	code, value := send("/ai/mcp", body, "test-device", false)
	if code != 200 || value["result"] == nil || backend.calls.Load() != 1 {
		t.Fatalf("valid call: %d %v", code, value)
	}
	for _, field := range []string{"actor", "tid", "hostSessionId", "_meta"} {
		bad := strings.Replace(body, `"text":"hello"`, `"text":"hello","`+field+`":"forged"`, 1)
		_, value = send("/ai/mcp", bad, "test-device", false)
		if value["error"] == nil {
			t.Fatalf("forged %s accepted: %v", field, value)
		}
	}
	_, value = send("/ai/mcp", strings.Replace(body, "real-session", "forged-session", 1), "test-device", false)
	if !value["result"].(map[string]any)["isError"].(bool) {
		t.Fatal(value)
	}
	_, value = send("/ai/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "test-device", false)
	tools := value["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatal(tools)
	}
	for _, tool := range tools {
		if strings.Contains(tool.(map[string]any)["name"].(string), "ack") {
			t.Fatal("private tool leaked")
		}
	}
	if code, _ = send("/ai/mcp", body, "test-device", true); code != 403 {
		t.Fatal(code)
	}
	if code, _ = send("/ai/team/node/register", `{"label":"x","actor":{"tenantId":"forged"}}`, "test-device", false); code != 400 {
		t.Fatal(code)
	}

	for _, edge := range []struct {
		method, body, accept, version string
		want                          int
		errorCode                     float64
	}{
		{"GET", "", "text/event-stream", "", 405, 0},
		{"POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "application/json, text/event-stream", "2025-06-18", 202, 0},
		{"POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json", "", 406, 0},
		{"POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json-fake, text/event-stream", "", 406, 0},
		{"POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json, text/event-stream", "unsupported", 400, 0},
		{"POST", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, "application/json, text/event-stream", "", 200, -32600},
		{"POST", `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, "application/json, text/event-stream", "", 200, -32600},
		{"POST", `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"actor":"fake"}}`, "application/json, text/event-stream", "", 200, -32602},
		{"POST", `{"jsonrpc":"2.0","id":1,"method":"unknown"}`, "application/json, text/event-stream", "", 200, -32601},
		{"POST", `{"jsonrpc":`, "application/json, text/event-stream", "", 200, -32700},
		{"POST", `[]`, "application/json, text/event-stream", "", 200, -32600},
	} {
		req, _ := http.NewRequest(edge.method, httpServer.URL+"/ai/mcp", strings.NewReader(edge.body))
		req.Header.Set("Authorization", "Bearer test-device")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", edge.accept)
		if edge.version != "" {
			req.Header.Set("MCP-Protocol-Version", edge.version)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != edge.want {
			t.Fatalf("protocol %s: %d %s", edge.body, res.StatusCode, payload)
		}
		if edge.want == 202 && len(payload) != 0 {
			t.Fatal("notification body must be empty")
		}
		if edge.errorCode != 0 {
			var value map[string]any
			_ = json.Unmarshal(payload, &value)
			if value["error"].(map[string]any)["code"] != edge.errorCode {
				t.Fatalf("incorrect JSON-RPC error %s", payload)
			}
		}
	}
	revoked.Store(true)
	if code, _ = send("/ai/mcp", body, "test-device", false); code != 401 {
		t.Fatal(code)
	}
	if authorities.Load() < 8 {
		t.Fatal("authority not repeated")
	}
	t.Logf("真实随机回环HTTP→gRPC完成；在线核验%d次，撤销/伪造参数/会话及Origin拒绝", authorities.Load())
}
