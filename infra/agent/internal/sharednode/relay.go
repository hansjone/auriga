package sharednode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/protobuf/proto"
)

const maximum = 16 * 1024 * 1024

var uuid = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type execution struct {
	binding  *nodev1.SharedDshSessionBinding
	scopes   []string
	audience string
}
type Relay struct {
	Client *Client
	Pool   string
	mu     sync.Mutex
	tasks  map[string]execution
	tokens map[string]*nodev1.SharedDshCapability
	// Set only for a real installed broker transport, never advertised otherwise.
	Message func(context.Context, string, json.RawMessage) (any, error)
}

func NewRelay(c *Client, pool string) *Relay {
	return &Relay{Client: c, Pool: pool, tasks: map[string]execution{}, tokens: map[string]*nodev1.SharedDshCapability{}}
}
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" || req.URL.RawQuery != "" || req.URL.RawPath != "" || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("Upgrade") != "" {
		replyError(w, "common.validation_failed")
		return
	}
	op := strings.TrimPrefix(req.URL.Path, "/_auriga/shared-rpc/")
	if req.URL.Path != "/_auriga/shared-rpc/"+op {
		replyError(w, "common.not_found")
		return
	}
	raw, e := io.ReadAll(io.LimitReader(req.Body, maximum+1))
	if e != nil || len(raw) > maximum || !validRequest(raw, op) {
		replyError(w, "common.validation_failed")
		return
	}
	if op == "model" {
		if e = r.model(req.Context(), w, raw); e != nil {
			replyError(w, "common.unavailable")
		}
		return
	}
	value, e := r.invoke(req.Context(), op, raw)
	if e != nil {
		code := e.Error()
		if !strings.HasPrefix(code, "shared.") && !strings.HasPrefix(code, "common.") {
			code = "common.unavailable"
		}
		replyError(w, code)
		return
	}
	b, e := json.Marshal(value)
	if e != nil || len(b) > maximum {
		replyError(w, "common.unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
func replyError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(503)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
func (r *Relay) session(ctx context.Context, b *nodev1.SharedDshSessionBinding) error {
	if b == nil || b.TenantId != r.Client.Settings.TenantID || b.PhysicalInstanceId != r.Pool || !uuid.MatchString(b.SessionId) || !uuid.MatchString(b.PlatformSessionId) {
		return errors.New("shared.binding_mismatch")
	}
	out := new(workspacev1.LookupSharedDshSessionResponse)
	if e := r.Client.call(ctx, false, "LookupSharedDshSession", &workspacev1.LookupSharedDshSessionRequest{TenantId: b.TenantId, PhysicalInstanceId: r.Pool, SessionId: b.SessionId}, out); e != nil {
		return e
	}
	if !proto.Equal(b, out.Binding) {
		return errors.New("shared.revoked")
	}
	return nil
}
func (r *Relay) binding(b *nodev1.SharedDshBinding) error {
	if b == nil || b.TenantId != r.Client.Settings.TenantID || b.PhysicalInstanceId != r.Pool {
		return errors.New("shared.binding_mismatch")
	}
	return nil
}
func (r *Relay) task(ctx context.Context, b *nodev1.SharedDshSessionBinding, id, audience string, scopes []string) error {
	r.mu.Lock()
	t, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok || !proto.Equal(t.binding, b) || t.audience != audience {
		return errors.New("shared.revoked")
	}
	for _, s := range scopes {
		found := false
		for _, v := range t.scopes {
			found = found || s == v
		}
		if !found {
			return errors.New("common.forbidden")
		}
	}
	return r.session(ctx, b)
}
func (r *Relay) capability(ctx context.Context, token string) (*nodev1.SharedDshCapability, error) {
	r.mu.Lock()
	cap := r.tokens[token]
	r.mu.Unlock()
	if cap == nil || cap.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("shared.expired")
	}
	if e := r.task(ctx, cap.Binding, cap.TaskId, cap.Audience, cap.Scopes); e != nil {
		return nil, e
	}
	out := new(iamv1.AuthorizeSharedCapabilityResponse)
	if e := r.Client.call(ctx, true, "AuthorizeSharedCapability", &iamv1.AuthorizeSharedCapabilityRequest{Capability: cap}, out); e != nil {
		return nil, e
	}
	if !out.Active || !proto.Equal(cap.Binding, out.Binding) || cap.CredentialVersion != out.CredentialVersion {
		return nil, errors.New("shared.revoked")
	}
	return cap, nil
}
func (r *Relay) invoke(ctx context.Context, op string, raw []byte) (any, error) {
	switch op {
	case "health":
		if !validRequest(raw, "health") {
			return nil, errors.New("common.validation_failed")
		}
		ops := []string{"health", "resolve", "register", "lookup", "resourceSnapshot", "beginTask", "issueCapability", "finishTask", "authorizeCapability", "readCredentials"}
		if r.Client.Model != nil {
			ops = append(ops, "model")
		}
		if r.Message != nil {
			ops = append(ops, "send", "receive")
		}
		return map[string]any{"protocol": "auriga.shared.node.v1", "tenantId": r.Client.Settings.TenantID, "physicalInstanceId": r.Pool, "operations": ops}, nil
	case "resolve":
		var in struct {
			Binding           json.RawMessage
			PlatformSessionId string
		}
		if json.Unmarshal(raw, &in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		b := new(nodev1.SharedDshBinding)
		if decode(in.Binding, b) != nil || r.binding(b) != nil {
			return nil, errors.New("shared.binding_mismatch")
		}
		out := new(workspacev1.ResolveSharedDshResponse)
		e := r.Client.call(ctx, false, "ResolveSharedDsh", &workspacev1.ResolveSharedDshRequest{TenantId: b.TenantId, MemberId: b.MemberId, PlacementId: b.PlacementId, PlatformSessionId: in.PlatformSessionId}, out)
		if e != nil {
			return nil, e
		}
		if !proto.Equal(b, out.Binding) || out.State != "ready" {
			return nil, errors.New("shared.revoked")
		}
		return wire(out)
	case "register":
		in := new(workspacev1.RegisterSharedDshSessionRequest)
		if decode(raw, in) != nil || in.Binding == nil || in.Binding.TenantId != r.Client.Settings.TenantID || in.Binding.PhysicalInstanceId != r.Pool {
			return nil, errors.New("shared.binding_mismatch")
		}
		out := new(workspacev1.RegisterSharedDshSessionResponse)
		if e := r.Client.call(ctx, false, "RegisterSharedDshSession", in, out); e != nil {
			return nil, e
		}
		return wire(out)
	case "lookup":
		in := new(workspacev1.LookupSharedDshSessionRequest)
		if decode(raw, in) != nil || in.TenantId != r.Client.Settings.TenantID || in.PhysicalInstanceId != r.Pool {
			return nil, errors.New("shared.binding_mismatch")
		}
		out := new(workspacev1.LookupSharedDshSessionResponse)
		if e := r.Client.call(ctx, false, "LookupSharedDshSession", in, out); e != nil {
			return nil, e
		}
		return wire(out)
	case "resourceSnapshot":
		in := new(workspacev1.GetSharedDshResourceSnapshotRequest)
		if decode(raw, in) != nil || r.binding(in.Binding) != nil {
			return nil, errors.New("shared.binding_mismatch")
		}
		out, e := r.Client.Snapshot(ctx, in.Binding)
		if e != nil {
			return nil, e
		}
		return wire(out)
	case "beginTask":
		in := new(iamv1.BeginSharedTaskRequest)
		if decode(raw, in) != nil || !uuid.MatchString(in.TaskId) {
			return nil, errors.New("common.validation_failed")
		}
		if e := r.session(ctx, in.Binding); e != nil {
			return nil, e
		}
		if in.Audience == "" {
			in.Audience = "auriga.shared.enterprise"
		}
		r.mu.Lock()
		old, exists := r.tasks[in.TaskId]
		if len(r.tasks) >= 4096 && !exists {
			r.mu.Unlock()
			return nil, errors.New("common.unavailable")
		}
		if exists && (!proto.Equal(old.binding, in.Binding) || old.audience != in.Audience || !sameStrings(old.scopes, in.Scopes)) {
			r.mu.Unlock()
			return nil, errors.New("common.conflict")
		}
		// The execution is reserved before Begin. Cancellation compensation uses
		// this exact binding even if the response is lost.
		r.tasks[in.TaskId] = execution{proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding), append([]string{}, in.Scopes...), in.Audience}
		r.mu.Unlock()
		out := new(iamv1.BeginSharedTaskResponse)
		if e := r.Client.call(ctx, true, "BeginSharedTask", in, out); e != nil {
			r.finish(in.Binding, in.TaskId)
			return nil, e
		}
		if ctx.Err() != nil {
			r.finish(in.Binding, in.TaskId)
			return nil, ctx.Err()
		}
		if e := r.remember(in.Binding, in.TaskId, out.Token, out.Capability); e != nil {
			r.finish(in.Binding, in.TaskId)
			return nil, e
		}
		return wire(out)
	case "issueCapability":
		in := new(iamv1.IssueSharedCapabilityRequest)
		if decode(raw, in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		if e := r.task(ctx, in.Binding, in.TaskId, in.Audience, in.Scopes); e != nil {
			return nil, e
		}
		out := new(iamv1.IssueSharedCapabilityResponse)
		if e := r.Client.call(ctx, true, "IssueSharedCapability", in, out); e != nil {
			return nil, e
		}
		if e := r.remember(in.Binding, in.TaskId, out.Token, out.Capability); e != nil {
			return nil, e
		}
		return wire(out)
	case "finishTask":
		in := new(iamv1.FinishSharedTaskRequest)
		if decode(raw, in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		r.mu.Lock()
		t, ok := r.tasks[in.TaskId]
		r.mu.Unlock()
		if !ok || !proto.Equal(t.binding, in.Binding) {
			return nil, errors.New("shared.binding_mismatch")
		}
		e := r.finish(in.Binding, in.TaskId)
		return map[string]any{}, e
	case "authorizeCapability", "readCredentials":
		var in struct{ Capability string }
		if json.Unmarshal(raw, &in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		cap, e := r.capability(ctx, in.Capability)
		if e != nil {
			return nil, e
		}
		if op == "readCredentials" {
			out := new(iamv1.ReadSharedCredentialsResponse)
			if e = r.Client.call(ctx, true, "ReadSharedCredentials", &iamv1.ReadSharedCredentialsRequest{Capability: cap}, out); e != nil {
				return nil, e
			}
			return wire(out)
		}
		out := new(iamv1.AuthorizeSharedCapabilityResponse)
		if e = r.Client.call(ctx, true, "AuthorizeSharedCapability", &iamv1.AuthorizeSharedCapabilityRequest{Capability: cap}, out); e != nil {
			return nil, e
		}
		return wire(out)
	case "send", "receive":
		if r.Message != nil {
			var in struct {
				TenantId, SessionId, TaskId, Capability, MessageId string
				Body                                               json.RawMessage
			}
			if json.Unmarshal(raw, &in) != nil || !uuid.MatchString(in.MessageId) || len(in.Body) == 0 || len(in.Body) > 65536 {
				return nil, errors.New("common.validation_failed")
			}
			cap, e := r.capability(ctx, in.Capability)
			if e != nil {
				return nil, e
			}
			scope := "message." + op
			if cap.Binding.TenantId != in.TenantId || cap.Binding.SessionId != in.SessionId || cap.TaskId != in.TaskId || cap.Audience != "auriga.shared.message" || !has(cap.Scopes, scope) {
				return nil, errors.New("shared.binding_mismatch")
			}
			return r.Message(ctx, op, raw)
		}
	}
	return nil, errors.New("common.unavailable")
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (r *Relay) remember(b *nodev1.SharedDshSessionBinding, id, token string, cap *nodev1.SharedDshCapability) error {
	if token == "" || cap == nil || cap.TaskId != id || !proto.Equal(b, cap.Binding) {
		return errors.New("shared.binding_mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok || cap.SchemaVersion != 1 || cap.Audience != task.audience {
		return errors.New("shared.binding_mismatch")
	}
	for _, scope := range cap.Scopes {
		if !has(task.scopes, scope) {
			return errors.New("shared.binding_mismatch")
		}
	}
	for token, c := range r.tokens {
		if c.ExpiresAt <= time.Now().Unix() {
			delete(r.tokens, token)
		}
	}
	if len(r.tokens) >= 16384 {
		return errors.New("common.unavailable")
	}
	r.tokens[token] = proto.Clone(cap).(*nodev1.SharedDshCapability)
	return nil
}
func (r *Relay) finish(b *nodev1.SharedDshSessionBinding, id string) error {
	// Finish is a tombstone and deliberately survives a canceled Host call.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := new(iamv1.FinishSharedTaskResponse)
	err := r.Client.call(ctx, true, "FinishSharedTask", &iamv1.FinishSharedTaskRequest{Binding: b, TaskId: id}, out)
	if err == nil {
		r.mu.Lock()
		delete(r.tasks, id)
		for t, c := range r.tokens {
			if c.TaskId == id {
				delete(r.tokens, t)
			}
		}
		r.mu.Unlock()
	}
	return err
}
