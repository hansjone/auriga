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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const maximum = 16 * 1024 * 1024

var uuid = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type execution struct {
	binding                            *nodev1.SharedDshSessionBinding
	scopes                             []string
	audience                           string
	peer                               string
	serial                             uint64
	parent                             string
	parentSerial                       uint64
	ordinary, reserved, closing, begun bool
	uncertainBegin                     bool
	rejected                           bool
	started                            chan struct{}
	finishing                          chan struct{}
	finishErr                          error
}
type Relay struct {
	Client *Client
	Pool   string
	mu     sync.Mutex
	tasks  map[string]execution
	tokens map[string]*nodev1.SharedDshCapability
	// Set only for a real installed broker transport, never advertised otherwise.
	Message func(context.Context, string, json.RawMessage) (any, error)
	// Production joins SO_PEERCRED to the signed Engine execution on every call.
	VerifyPeer func(context.Context, int, string) (string, error)
	sources    map[string]messageSource
	boots      map[string]string
	Admission  TaskAdmission
	serial     uint64
	ended      map[string]endedExecution
	endedOrder []string
	endedNext  int
}

type endedExecution struct {
	binding *nodev1.SharedDshSessionBinding
	peer    string
}

func NewRelay(c *Client, pool string) *Relay {
	r := &Relay{Client: c, Pool: pool, tasks: map[string]execution{}, tokens: map[string]*nodev1.SharedDshCapability{}, sources: map[string]messageSource{}, boots: map[string]string{}}
	if c.Messages != nil && c.Settings.SharedMessages != nil {
		r.Message = c.message
	}
	return r
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
		var upstream *messageHTTPError
		if errors.As(e, &upstream) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(upstream.status)
			_, _ = w.Write(upstream.body)
			return
		}
		code := e.Error()
		if status.Code(e) == codes.NotFound {
			// Historical sessions can disappear from W while their Host snapshots
			// remain. Preserve the typed absence without exposing RPC details.
			code = "common.not_found"
		} else if !strings.HasPrefix(code, "shared.") && !strings.HasPrefix(code, "common.") {
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
	if check, ok := ctx.Value(executionFenceKey{}).(func() error); ok {
		if err := check(); err != nil {
			return err
		}
	}
	r.mu.Lock()
	t, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok || t.closing || !proto.Equal(t.binding, b) || t.audience != audience || t.peer != executionPeer(ctx) {
		return errors.New("shared.revoked")
	}
	if e := r.checkAdmission(t, id); e != nil {
		return e
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
	var serial uint64
	if cap != nil {
		serial = r.tasks[cap.TaskId].serial
	}
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
	if err := checkAuthorization(cap, out); err != nil {
		return nil, err
	}
	// A valid remote response can arrive after local Finish. It cannot revive
	// this handle or authorize a different execution with the same task UUID.
	r.mu.Lock()
	current, exists := r.tasks[cap.TaskId]
	live := exists && current.serial == serial && !current.closing && r.tokens[token] == cap
	r.mu.Unlock()
	if !live {
		return nil, errors.New("shared.revoked")
	}
	if err := r.task(ctx, cap.Binding, cap.TaskId, cap.Audience, cap.Scopes); err != nil {
		return nil, err
	}
	return cap, nil
}
func (r *Relay) invoke(ctx context.Context, op string, raw []byte) (any, error) {
	switch op {
	case "health":
		if !validRequest(raw, "health") {
			return nil, errors.New("common.validation_failed")
		}
		ops := []string{"health", "resolve", "register", "lookup", "resourceSnapshot", "beginTask", "issueCapability", "finishTask", "authorizeCapability", "readCredentials", "enterpriseOutbound"}
		if r.Client.Model != nil {
			ops = append(ops, "model")
		}
		if r.Message != nil {
			ops = append(ops, "messageContext", "send", "receive")
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
	case "enterpriseOutbound":
		return r.enterpriseOutbound(ctx, raw)
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
		return r.beginTask(ctx, raw)
	case "issueCapability":
		in := new(iamv1.IssueSharedCapabilityRequest)
		if decode(raw, in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		if !validTaskScopes(in.Audience, in.Scopes) {
			return nil, errors.New("common.forbidden")
		}
		r.mu.Lock()
		expected := r.tasks[in.TaskId].serial
		r.mu.Unlock()
		if e := r.task(ctx, in.Binding, in.TaskId, in.Audience, in.Scopes); e != nil {
			return nil, e
		}
		out := new(iamv1.IssueSharedCapabilityResponse)
		if e := r.Client.call(ctx, true, "IssueSharedCapability", in, out); e != nil {
			return nil, e
		}
		if e := r.task(ctx, in.Binding, in.TaskId, in.Audience, in.Scopes); e != nil {
			return nil, e
		}
		if e := r.remember(in.Binding, in.TaskId, out.Token, out.Capability, expected); e != nil {
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
		if !ok {
			return r.finishedReply(ctx, in.Binding, in.TaskId)
		}
		if !proto.Equal(t.binding, in.Binding) || t.peer != executionPeer(ctx) {
			return nil, errors.New("shared.binding_mismatch")
		}
		if e := r.finish(in.Binding, in.TaskId); e != nil {
			return nil, e
		}
		return r.finishedReply(ctx, in.Binding, in.TaskId)
	case "authorizeCapability", "readCredentials":
		var in struct{ Capability json.RawMessage }
		if json.Unmarshal(raw, &in) != nil {
			return nil, errors.New("common.validation_failed")
		}
		token, e := r.localCapability(in.Capability)
		if e != nil {
			return nil, e
		}
		cap, e := r.capability(ctx, token)
		if e != nil {
			return nil, e
		}
		if op == "readCredentials" {
			if cap.Audience != "auriga.shared.enterprise" || !has(cap.Scopes, "credentials") {
				return nil, errors.New("common.forbidden")
			}
			out := new(iamv1.ReadSharedCredentialsResponse)
			if e = r.Client.call(ctx, true, "ReadSharedCredentials", &iamv1.ReadSharedCredentialsRequest{Capability: cap}, out); e != nil {
				return nil, e
			}
			if _, e = r.capability(ctx, token); e != nil {
				return nil, e
			}
			return wire(out)
		}
		out := new(iamv1.AuthorizeSharedCapabilityResponse)
		if e = r.Client.call(ctx, true, "AuthorizeSharedCapability", &iamv1.AuthorizeSharedCapabilityRequest{Capability: cap}, out); e != nil {
			return nil, e
		}
		if e = checkAuthorization(cap, out); e != nil {
			return nil, e
		}
		if e = r.task(ctx, cap.Binding, cap.TaskId, cap.Audience, cap.Scopes); e != nil {
			return nil, e
		}
		return wire(out)
	case "messageContext", "send", "receive":
		return r.message(ctx, op, raw)
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

// This bounded replay cache carries acknowledgments only, never permissions or
// slots. Durable ended authority remains in IAM/W. A restart/evicted entry is
// unknown, not a fabricated success; only the exact original socket peer can
// replay a confirmed response after a lost UDS reply.
func (r *Relay) rememberFinished(id string, t execution) {
	const maximumEnded = 16384
	if r.ended == nil {
		r.ended = map[string]endedExecution{}
	}
	if len(r.endedOrder) < maximumEnded {
		r.endedOrder = append(r.endedOrder, id)
	} else {
		delete(r.ended, r.endedOrder[r.endedNext])
		r.endedOrder[r.endedNext] = id
		r.endedNext = (r.endedNext + 1) % maximumEnded
	}
	r.ended[id] = endedExecution{binding: t.binding, peer: t.peer}
}

func (r *Relay) finishedReply(ctx context.Context, b *nodev1.SharedDshSessionBinding, id string) (any, error) {
	r.mu.Lock()
	done, ok := r.ended[id]
	r.mu.Unlock()
	if !ok || !proto.Equal(done.binding, b) || done.peer != executionPeer(ctx) {
		return nil, errors.New("shared.binding_mismatch")
	}
	return wire(&iamv1.FinishSharedTaskResponse{Fenced: true, Binding: b, TaskId: id})
}
func (r *Relay) remember(b *nodev1.SharedDshSessionBinding, id, token string, cap *nodev1.SharedDshCapability, serial uint64) error {
	if token == "" || cap == nil || cap.TaskId != id || !proto.Equal(b, cap.Binding) {
		return errors.New("shared.binding_mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok || task.closing || task.serial != serial || cap.SchemaVersion != 1 || cap.Audience != task.audience || !validTaskScopes(cap.Audience, cap.Scopes) {
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
	r.mu.Lock()
	t, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok || !proto.Equal(t.binding, b) {
		return errors.New("shared.binding_mismatch")
	}
	return r.finishExecution(id, t.serial)
}
