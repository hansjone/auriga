package sharednode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type executionPeerKey struct{}
type executionFenceKey struct{}

func executionPeer(ctx context.Context) string {
	peer, _ := ctx.Value(executionPeerKey{}).(string)
	return peer
}

type messageSource struct {
	binding                                    *nodev1.SharedDshSessionBinding
	boot, native, revision, operation, context string
}

type messageHTTPError struct {
	status int
	body   json.RawMessage
}

func (e *messageHTTPError) Error() string { return "shared.message_unavailable" }

// One fixed-origin, Node-mTLS RoundTrip. Preserve the original signed msgnet-v2
// envelopes and errors; neither a successful fetch nor HTTP 200 proves execution.
func (c *Client) message(ctx context.Context, op string, raw json.RawMessage) (any, error) {
	path := map[string]string{"messageContext": "context", "send": "send", "receive": "receive"}[op]
	if path == "" || c.Messages == nil || c.Settings.SharedMessages == nil {
		return nil, errors.New("common.unavailable")
	}
	var in struct{ Capability, SessionId, TaskId string }
	if json.Unmarshal(raw, &in) != nil {
		return nil, errors.New("common.validation_failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.Settings.SharedMessages.Address+"/internal/shared/messages/"+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Auriga-Shared-Capability", in.Capability)
	req.Header.Set("Auriga-Shared-Session-Id", in.SessionId)
	req.Header.Set("Auriga-Shared-Task-Id", in.TaskId)
	response, err := c.Messages.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	// The original signed fetch page (and context trust chain) can be 4MiB;
	// the 64KiB budget applies to input frames, not signed batch results.
	limit := 4 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil || len(body) > limit || !json.Valid(body) || strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return nil, errors.New("common.unavailable")
	}
	if response.StatusCode != http.StatusOK {
		// Only the reviewed error namespace may cross the local boundary.
		var problem struct {
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &problem); err != nil {
			return nil, errors.New("common.unavailable")
		}
		code := problem.Code
		if code == "" {
			code = problem.Error.Code
		}
		if !has([]string{"network", "shared", "common"}, strings.Split(code, ".")[0]) || response.StatusCode < 400 || response.StatusCode > 599 {
			return nil, errors.New("common.unavailable")
		}
		return nil, &messageHTTPError{status: response.StatusCode, body: body}
	}
	return json.RawMessage(body), nil
}

func canonicalUint(value string, positive bool) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && (!positive || n > 0) && strconv.FormatUint(n, 10) == value
}

func (r *Relay) message(ctx context.Context, op string, raw []byte) (any, error) {
	if r.Message == nil {
		return nil, errors.New("common.unavailable")
	}
	var in struct {
		TenantId, SessionId, TaskId, Capability, OperationId, BootId, ContextId, MessageId, Operation string
		Target                                                                                        struct{ Realm, Node, Local string }
		Body, Frame                                                                                   json.RawMessage
		NativeWitness                                                                                 struct {
			WitnessID string `json:"witness_id"`
			BindingID string `json:"binding_id"`
			Revision  string `json:"binding_revision"`
			Observed  string `json:"observed_at"`
			Until     string `json:"valid_until"`
		}
	}
	// Reject nested extra identity fields as well as duplicate keys.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 65536 || !validRequest(raw, op) || decoder.Decode(&in) != nil || !uuid.MatchString(in.BootId) || !uuid.MatchString(in.NativeWitness.WitnessID) || !uuid.MatchString(in.NativeWitness.BindingID) || !canonicalUint(in.NativeWitness.Revision, true) {
		return nil, errors.New("common.validation_failed")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	exactObject := func(raw json.RawMessage, keys []string) bool {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || len(object) != len(keys) {
			return false
		}
		for key := range object {
			if !has(keys, key) {
				return false
			}
		}
		return true
	}
	if !exactObject(fields["nativeWitness"], []string{"witness_id", "binding_id", "binding_revision", "observed_at", "valid_until"}) || op == "send" && !exactObject(fields["target"], []string{"realm", "node", "local"}) {
		return nil, errors.New("common.validation_failed")
	}
	w := in.NativeWitness
	observed, e1 := time.Parse("2006-01-02T15:04:05Z", w.Observed)
	until, e2 := time.Parse("2006-01-02T15:04:05Z", w.Until)
	if e1 != nil || e2 != nil || observed.Format("2006-01-02T15:04:05Z") != w.Observed || until.Format("2006-01-02T15:04:05Z") != w.Until || until.Sub(observed) <= 0 || until.Sub(observed) > 30*time.Second || observed.After(time.Now().Add(5*time.Second)) || !until.After(time.Now()) {
		return nil, errors.New("shared.expired")
	}
	scope := "message." + op
	switch op {
	case "messageContext":
		if !uuid.MatchString(in.OperationId) {
			return nil, errors.New("common.validation_failed")
		}
	case "send":
		if !uuid.MatchString(in.ContextId) || !uuid.MatchString(in.MessageId) || len(in.Body) == 0 || len(in.Body) > 16384 || !canonicalUint(in.Target.Realm, false) || !canonicalUint(in.Target.Node, false) || !canonicalUint(in.Target.Local, true) {
			return nil, errors.New("common.validation_failed")
		}
	case "receive":
		if !uuid.MatchString(in.ContextId) || !has([]string{"receiveSet", "fetch", "attemptBegin", "executionAdmit", "receipt"}, in.Operation) || len(in.Frame) == 0 || bytes.Equal(in.Frame, []byte("null")) {
			return nil, errors.New("common.validation_failed")
		}
	}
	cap, err := r.capability(ctx, in.Capability)
	if err != nil {
		return nil, err
	}
	if cap.Binding.TenantId != in.TenantId || cap.Binding.SessionId != in.SessionId || cap.TaskId != in.TaskId || cap.Audience != "auriga.shared.message" || (op == "messageContext" && !has(cap.Scopes, "message.send") && !has(cap.Scopes, "message.receive")) || (op != "messageContext" && !has(cap.Scopes, scope)) {
		return nil, errors.New("shared.binding_mismatch")
	}
	r.mu.Lock()
	peer := executionPeer(ctx)
	if boot, exists := r.boots[peer]; exists && boot != in.BootId {
		r.mu.Unlock()
		return nil, errors.New("shared.binding_mismatch")
	}
	if r.boots[peer] == "" {
		if len(r.boots) >= 4096 {
			r.mu.Unlock()
			return nil, errors.New("common.unavailable")
		}
		r.boots[peer] = in.BootId
	}
	source, exists := r.sources[in.TaskId]
	if exists && (!proto.Equal(source.binding, cap.Binding) || source.boot != in.BootId || source.native != w.BindingID || source.revision != w.Revision || op == "messageContext" && source.operation != in.OperationId || op != "messageContext" && (source.context == "" || source.context != in.ContextId)) {
		r.mu.Unlock()
		return nil, errors.New("shared.binding_mismatch")
	}
	if !exists {
		if op != "messageContext" {
			r.mu.Unlock()
			return nil, errors.New("shared.binding_mismatch")
		}
		source = messageSource{binding: proto.Clone(cap.Binding).(*nodev1.SharedDshSessionBinding), boot: in.BootId, native: w.BindingID, revision: w.Revision, operation: in.OperationId}
		r.sources[in.TaskId] = source
	}
	r.mu.Unlock()
	value, err := r.Message(ctx, op, raw)
	if err != nil {
		return nil, err
	}
	if _, err = r.capability(ctx, in.Capability); err != nil {
		return nil, err
	}
	if op == "messageContext" {
		result, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		contextID, err := checkMessageContext(result, cap.Binding, in.TaskId, in.BootId, w.WitnessID, w.BindingID, w.Revision, until)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		current, ok := r.sources[in.TaskId]
		if !ok || current.context != "" && current.context != contextID {
			return nil, errors.New("shared.revoked")
		}
		current.context = contextID
		r.sources[in.TaskId] = current
	}
	return value, nil
}
