package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Authenticate must perform online authoritative validation on every request.
type Authenticate func(context.Context, string) (*collab.Actor, error)
type Server struct {
	client  collab.CollabCommunicationServiceClient
	auth    Authenticate
	tools   []map[string]any
	schemas map[string]map[string]any
}

var toolDefs = []struct{ name, def, description string }{
	{"auriga_list_endpoints", "ListEndpoints", "List authorized receiving endpoints."},
	{"auriga_open_thread", "OpenThread", "Open a thread and send its first message."},
	{"auriga_reply_thread", "ReplyThread", "Reply to a participating thread."},
	{"auriga_read_thread", "ReadThread", "Read messages from a participating thread."},
}

// New loads the contract rather than maintaining a second model argument schema.
func New(client collab.CollabCommunicationServiceClient, auth Authenticate, contract []byte) (*Server, error) {
	if client == nil || auth == nil {
		return nil, errors.New("online dependencies required")
	}
	var document map[string]any
	if err := json.Unmarshal(contract, &document); err != nil {
		return nil, err
	}
	defs, ok := document["$defs"].(map[string]any)
	if !ok {
		return nil, errors.New("missing definitions")
	}
	s := &Server{client: client, auth: auth, schemas: map[string]map[string]any{}}
	for _, t := range toolDefs {
		schema, ok := expand(defs[t.def], defs).(map[string]any)
		if !ok {
			return nil, errors.New("missing tool schema")
		}
		s.schemas[t.name] = schema
		s.tools = append(s.tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": schema})
	}
	return s, nil
}
func expand(value any, defs map[string]any) any {
	switch v := value.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			return expand(defs[strings.TrimPrefix(ref, "#/$defs/")], defs)
		}
		result := map[string]any{}
		for k, x := range v {
			result[k] = expand(x, defs)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, x := range v {
			result[i] = expand(x, defs)
		}
		return result
	default:
		return value
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, finishTrace := observability.Begin(w, r)
	defer finishTrace()
	// Node-only API: browsers have no reason to call it; reject every Origin, including null.
	if _, present := r.Header["Origin"]; present {
		http.Error(w, "common.forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/ai/mcp" && !strings.HasPrefix(r.URL.Path, "/ai/team/node/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		http.Error(w, "common.validation_failed", http.StatusUnsupportedMediaType)
		return
	}
	if len(r.Header.Values("Authorization")) != 1 {
		http.Error(w, "common.unauthenticated", http.StatusUnauthorized)
		return
	}
	token := r.Header.Get("Authorization")
	if !strings.HasPrefix(token, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(token, "Bearer ")) == "" {
		http.Error(w, "common.unauthenticated", http.StatusUnauthorized)
		return
	}
	actor, err := s.auth(r.Context(), strings.TrimPrefix(token, "Bearer "))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if actor == nil || actor.TenantId == "" || actor.AccountId == "" || actor.CredentialKind != "DEVICE" || actor.CredentialSubject == "" {
		http.Error(w, "common.unauthenticated", http.StatusUnauthorized)
		return
	}
	actor = proto.Clone(actor).(*collab.Actor)
	r = r.WithContext(WithActor(r.Context(), actor))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "common.validation_failed", http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/ai/mcp" {
		s.mcp(w, r, actor, body)
		return
	}
	s.node(w, r, actor, body)
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func writeHTTPError(w http.ResponseWriter, err error) {
	code := 503
	label := "common.unavailable"
	switch status.Code(err) {
	case codes.Unauthenticated:
		code = 401
		label = "common.unauthenticated"
	case codes.PermissionDenied:
		code = 403
		label = "common.forbidden"
	case codes.InvalidArgument:
		code = 400
		label = "common.validation_failed"
	case codes.NotFound:
		code = 404
		label = "common.not_found"
	case codes.AlreadyExists, codes.Aborted:
		code = 409
		label = "common.conflict"
	case codes.FailedPrecondition:
		code = 412
		label = "common.precondition_failed"
	}
	http.Error(w, label, code)
}
func strict(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func protoValue(p proto.Message) any {
	b, _ := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(p)
	var value any
	_ = json.Unmarshal(b, &value)
	return value
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func rpcError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}
func (s *Server) mcp(w http.ResponseWriter, r *http.Request, a *collab.Actor, body []byte) {
	if !accepts(r.Header.Get("Accept"), "application/json") || !accepts(r.Header.Get("Accept"), "text/event-stream") {
		http.Error(w, "common.validation_failed", http.StatusNotAcceptable)
		return
	}
	if version := r.Header.Get("MCP-Protocol-Version"); version != "" && version != "2025-06-18" {
		http.Error(w, "common.validation_failed", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		rpcError(w, nil, -32700, "Parse error")
		return
	}
	var q rpcRequest
	if strict(body, &q) != nil || q.JSONRPC != "2.0" || q.Method == "" {
		rpcError(w, nil, -32600, "Invalid Request")
		return
	}
	if len(q.ID) == 0 {
		if strings.HasPrefix(q.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		rpcError(w, nil, -32600, "Invalid Request")
		return
	}
	if !validID(q.ID) {
		rpcError(w, nil, -32600, "Invalid Request")
		return
	}
	var result any
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ClientInfo      map[string]any `json:"clientInfo"`
			Meta            map[string]any `json:"_meta"`
		}
		if strict(q.Params, &p) != nil || p.ProtocolVersion == "" || p.Capabilities == nil || p.ClientInfo == nil {
			rpcError(w, q.ID, -32602, "Invalid params")
			return
		}
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "auriga-team", "version": "1"}}
	case "ping":
		if !emptyParams(q.Params) {
			rpcError(w, q.ID, -32602, "Invalid params")
			return
		}
		result = map[string]any{}
	case "tools/list":
		if !emptyParams(q.Params) {
			rpcError(w, q.ID, -32602, "Invalid params")
			return
		}
		result = map[string]any{"tools": s.tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Meta      map[string]any `json:"_meta"`
		}
		if strict(q.Params, &p) != nil || s.schemas[p.Name] == nil || validate(p.Arguments, s.schemas[p.Name]) != nil {
			rpcError(w, q.ID, -32602, "Invalid params")
			return
		}
		for _, key := range []string{"auriga/sessionId", "auriga/toolCallId", "auriga/rootCallId"} {
			value, ok := p.Meta[key].(string)
			if !ok || strings.TrimSpace(value) == "" || len(value) > 512 {
				rpcError(w, q.ID, -32602, "Invalid session context")
				return
			}
		}
		if len(r.Header.Values("Auriga-Endpoint-Id")) != 1 || len(r.Header.Values("Auriga-Binding-Id")) != 1 {
			rpcError(w, q.ID, -32602, "Invalid binding headers")
			return
		}
		a.EndpointId = r.Header.Get("Auriga-Endpoint-Id")
		a.BindingId = r.Header.Get("Auriga-Binding-Id")
		a.HostSessionId = p.Meta["auriga/sessionId"].(string)
		if a.EndpointId == "" || a.BindingId == "" {
			rpcError(w, q.ID, -32602, "Missing binding")
			return
		}
		message, err := s.call(r.Context(), p.Name, a, p.Arguments)
		if err != nil {
			label := publicError(err)
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": label}}}
		} else {
			value := protoValue(message)
			encoded, _ := json.Marshal(value)
			result = map[string]any{"structuredContent": value, "content": []any{map[string]any{"type": "text", "text": string(encoded)}}}
		}
	default:
		rpcError(w, q.ID, -32601, "Method not found")
		return
	}
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
}
func publicError(err error) string {
	switch status.Code(err) {
	case codes.PermissionDenied:
		return "common.forbidden"
	case codes.Unauthenticated:
		return "common.unauthenticated"
	case codes.InvalidArgument:
		return "common.validation_failed"
	case codes.NotFound:
		return "common.not_found"
	case codes.AlreadyExists, codes.Aborted:
		return "common.conflict"
	case codes.FailedPrecondition:
		return "common.precondition_failed"
	default:
		return "common.unavailable"
	}
}
func (s *Server) call(ctx context.Context, name string, a *collab.Actor, args map[string]any) (proto.Message, error) {
	b, _ := json.Marshal(args)
	switch name {
	case "auriga_list_endpoints":
		q := &collab.ListEndpointsRequest{}
		if err := protojson.Unmarshal(b, q); err != nil {
			return nil, status.Error(codes.InvalidArgument, "common.validation_failed")
		}
		q.Actor = a
		return s.client.ListEndpoints(ctx, q)
	case "auriga_open_thread":
		q := &collab.OpenThreadRequest{}
		if err := protojson.Unmarshal(b, q); err != nil {
			return nil, status.Error(codes.InvalidArgument, "common.validation_failed")
		}
		q.Actor = a
		return s.client.OpenThread(ctx, q)
	case "auriga_reply_thread":
		q := &collab.ReplyThreadRequest{}
		if err := protojson.Unmarshal(b, q); err != nil {
			return nil, status.Error(codes.InvalidArgument, "common.validation_failed")
		}
		q.Actor = a
		return s.client.ReplyThread(ctx, q)
	case "auriga_read_thread":
		q := &collab.ReadThreadRequest{}
		if err := protojson.Unmarshal(b, q); err != nil {
			return nil, status.Error(codes.InvalidArgument, "common.validation_failed")
		}
		q.Actor = a
		return s.client.ReadThread(ctx, q)
	}
	return nil, status.Error(codes.InvalidArgument, "common.validation_failed")
}

func accepts(header, expected string) bool {
	for _, part := range strings.Split(header, ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && media == expected {
			if q, ok := params["q"]; ok {
				weight, err := strconv.ParseFloat(q, 64)
				if err != nil || weight <= 0 {
					continue
				}
			}
			return true
		}
	}
	return false
}
func validID(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return true
	}
	var number json.Number
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&number) != nil {
		return false
	}
	_, err := number.Int64()
	return err == nil
}
func emptyParams(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var p struct {
		Meta map[string]any `json:"_meta"`
	}
	return strict(raw, &p) == nil && string(raw) != "null"
}
