package sharedhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	node "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const MessagePrefix = "/internal/shared/messages/"
const maxMessageRequest = 65536
const maxMessageResult = 4 << 20

type MessageCaller interface {
	Call(context.Context, *collab.CallSharedMessageRequest) ([]byte, error)
}
type messageHandler struct {
	tenant string
	gate   *sharedauth.Gate
	caller MessageCaller
}
type messageRequest struct {
	TenantID      string          `json:"tenantId"`
	SessionID     string          `json:"sessionId"`
	TaskID        string          `json:"taskId"`
	Capability    string          `json:"capability"`
	OperationID   string          `json:"operationId"`
	ContextID     string          `json:"contextId"`
	BootID        string          `json:"bootId"`
	NativeWitness json.RawMessage `json:"nativeWitness"`
	MessageID     string          `json:"messageId"`
	Target        json.RawMessage `json:"target"`
	Body          json.RawMessage `json:"body"`
	Operation     string          `json:"operation"`
	Frame         json.RawMessage `json:"frame"`
}

func NewMessageHandler(tenant string, nodes []string, gate *sharedauth.Gate, caller MessageCaller) (http.Handler, error) {
	if !canonicalUUID(tenant) || gate == nil || caller == nil {
		return nil, sharedauth.ErrInvalid
	}
	return NewNodeHandler(nodes, &messageHandler{tenant, gate, caller})
}
func (h *messageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, finishTrace := observability.Begin(w, r)
	defer finishTrace()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	op := strings.TrimPrefix(r.URL.Path, MessagePrefix)
	if r.URL.Path != MessagePrefix+op || (op != "context" && op != "send" && op != "receive") {
		writeError(w, http.StatusNotFound, "common.not_found")
		return
	}
	if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Content-Encoding") != "" {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	token, tokenOK := singleHeader(r, "Auriga-Shared-Capability")
	session, sessionOK := singleHeader(r, "Auriga-Shared-Session-Id")
	task, taskOK := singleHeader(r, "Auriga-Shared-Task-Id")
	if !tokenOK || len(token) > 16384 || !sessionOK || !taskOK || !canonicalUUID(session) || !canonicalUUID(task) {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMessageRequest))
	if err != nil {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	var in messageRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || in.TenantID != h.tenant || in.SessionID != session || in.TaskID != task || in.Capability != token || !canonicalUUID(in.BootID) || len(in.NativeWitness) == 0 {
		writeError(w, http.StatusBadRequest, "shared.binding_mismatch")
		return
	}
	if !validMessageOperation(op, in) {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	scope := "message.send"
	if op == "receive" {
		scope = "message.receive"
	}
	claims, err := h.gate.Verify(r.Context(), token, h.tenant, session, task, sharedauth.Message, scope)
	if op == "context" && errors.Is(err, sharedauth.ErrRevoked) {
		scope = "message.receive"
		claims, err = h.gate.Verify(r.Context(), token, h.tenant, session, task, sharedauth.Message, scope)
	}
	if err != nil {
		status, code := invocationError(err)
		writeError(w, status, code)
		return
	}
	nodeID, ok := NodeID(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "common.forbidden")
		return
	}
	call, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	md, _ := metadata.FromOutgoingContext(call)
	md = md.Copy()
	md.Set("auriga-tenant-id", h.tenant)
	call = metadata.NewOutgoingContext(call, md)
	result, err := h.caller.Call(call, &collab.CallSharedMessageRequest{Capability: messageCapability(claims), AuthenticatedNodeId: nodeID, Operation: op, RequestJson: raw})
	if err == nil {
		err = h.gate.Recheck(call, claims, scope)
	}
	if err != nil || call.Err() != nil {
		httpStatus, code := messageError(err)
		writeError(w, httpStatus, code)
		return
	}
	if len(result) == 0 || len(result) > maxMessageResult || !json.Valid(result) {
		writeError(w, http.StatusServiceUnavailable, "common.unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(result)
}
func validMessageOperation(op string, in messageRequest) bool {
	switch op {
	case "context":
		return canonicalUUID(in.OperationID) && in.ContextID == "" && in.MessageID == "" && len(in.Target) == 0 && len(in.Body) == 0 && in.Operation == "" && len(in.Frame) == 0
	case "send":
		return canonicalUUID(in.ContextID) && in.OperationID == "" && canonicalUUID(in.MessageID) && len(in.Target) > 0 && len(in.Body) > 0 && len(in.Body) <= 16384 && in.Operation == "" && len(in.Frame) == 0
	case "receive":
		return canonicalUUID(in.ContextID) && in.OperationID == "" && in.MessageID == "" && len(in.Target) == 0 && len(in.Body) == 0 && len(in.Frame) > 0 && (in.Operation == "receiveSet" || in.Operation == "fetch" || in.Operation == "attemptBegin" || in.Operation == "executionAdmit" || in.Operation == "receipt")
	}
	return false
}
func messageCapability(c sharedauth.Claims) *node.SharedDshCapability {
	b := c.Binding
	mode := node.DshInstanceMode_DSH_INSTANCE_MODE_SHARED
	if b.Mode == "dedicated" {
		mode = node.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	}
	return &node.SharedDshCapability{SchemaVersion: c.SchemaVersion, Binding: &node.SharedDshSessionBinding{TenantId: b.TenantID, MemberId: b.MemberID, PlacementId: b.PlacementID, PhysicalInstanceId: b.PhysicalInstanceID, Mode: mode, Epoch: b.Epoch, Revision: b.Revision, SessionId: b.SessionID, PlatformSessionId: b.PlatformSessionID, AuthGeneration: b.AuthGeneration}, TaskId: c.TaskID, Audience: c.Audience, Scopes: append([]string(nil), c.Scopes...), CredentialVersion: c.CredentialVersion, IssuedAt: c.IssuedAt, ExpiresAt: c.ExpiresAt, Jti: c.JTI}
}

type RPCMessageCaller struct {
	Client collab.SharedMessageServiceClient
}

func (c RPCMessageCaller) Call(ctx context.Context, in *collab.CallSharedMessageRequest) ([]byte, error) {
	out, err := c.Client.CallSharedMessage(ctx, in, grpc.MaxCallRecvMsgSize(maxMessageResult+4096))
	if err != nil {
		code := status.Convert(err).Message()
		switch code {
		case "network.unauthorized", "network.revoked", "network.conflict", "network.outcome_unknown", "network.expired", "network.protocol_error", "network.stale_epoch", "network.recovery_conflict", "network.local_registration_required", "network.quota_exceeded", "network.overloaded":
			return nil, messageFault(code)
		}
		return nil, sharedauth.ErrUnavailable
	}
	if out == nil {
		return nil, sharedauth.ErrUnavailable
	}
	return out.ResultJson, nil
}

type messageFault string

func (e messageFault) Error() string { return string(e) }
func messageError(err error) (int, string) {
	var fault messageFault
	if errors.As(err, &fault) {
		switch string(fault) {
		case "network.unauthorized", "network.revoked":
			return http.StatusForbidden, string(fault)
		case "network.conflict", "network.stale_epoch":
			return http.StatusConflict, string(fault)
		case "network.protocol_error":
			return http.StatusBadRequest, string(fault)
		}
		return http.StatusServiceUnavailable, string(fault)
	}
	return invocationError(err)
}
