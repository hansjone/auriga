package network

import (
	"context"
	"errors"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MailboxClient carries complete application frames, including signed fetch
// pages/items. The old Mailbox model is only used by the in-memory skeleton.
type MailboxClient struct {
	rpc     collab.NetworkMailboxServiceClient
	timeout time.Duration
}

// The caller supplies the derived business deadline; parent cancellation wins.
func NewMailboxClient(conn grpc.ClientConnInterface, timeout time.Duration) (*MailboxClient, error) {
	if conn == nil || timeout <= 0 || timeout > 80*time.Second {
		return nil, errors.New("network: invalid mailbox RPC configuration")
	}
	return &MailboxClient{rpc: collab.NewNetworkMailboxServiceClient(conn), timeout: timeout}, nil
}

func (c *MailboxClient) Call(ctx context.Context, b liveBinding, operation string, raw []byte) ([]byte, *AppError) {
	timeout := c.timeout
	if sourceOperation(operation) {
		if _, ae := decodeSourceRequest(raw, operation); ae != nil {
			return nil, ae
		}
	}
	ctx, cancel := context.WithTimeout(team.WithActor(ctx, &collab.Actor{TenantId: b.Tenant}), timeout)
	defer cancel()
	id := &collab.NetworkMailboxConnection{TenantId: b.Tenant, Realm: b.Realm, Node: b.Node, Ingress: b.Ingress, ServerId: b.ServerID, ClientId: u64str(b.ClientID), ClientAccount: b.Account}
	options := []grpc.CallOption{grpc.MaxCallRecvMsgSize(EncodingMaxPayloadBytes + 16), grpc.MaxCallSendMsgSize(EncodingMaxPayloadBytes + 2048)}
	var data []byte
	var err error
	switch operation {
	case "submit":
		var resp *collab.NetworkMailboxServiceSubmitResponse
		resp, err = c.rpc.Submit(ctx, &collab.NetworkMailboxServiceSubmitRequest{Connection: id, SubmitJson: raw}, options...)
		data = resp.GetResultJson()
	case "receive_set":
		var resp *collab.NetworkMailboxServiceReceiveSetResponse
		resp, err = c.rpc.ReceiveSet(ctx, &collab.NetworkMailboxServiceReceiveSetRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "attempt_begin":
		var resp *collab.NetworkMailboxServiceAttemptBeginResponse
		resp, err = c.rpc.AttemptBegin(ctx, &collab.NetworkMailboxServiceAttemptBeginRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "execution_admit":
		var resp *collab.NetworkMailboxServiceExecutionAdmitResponse
		resp, err = c.rpc.ExecutionAdmit(ctx, &collab.NetworkMailboxServiceExecutionAdmitRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "ready":
		var resp *collab.NetworkMailboxServiceReadyResponse
		resp, err = c.rpc.Ready(ctx, &collab.NetworkMailboxServiceReadyRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "head":
		var resp *collab.NetworkMailboxServiceHeadResponse
		resp, err = c.rpc.Head(ctx, &collab.NetworkMailboxServiceHeadRequest{Connection: id}, options...)
		data = resp.GetFrameJson()
	case "fetch":
		var resp *collab.NetworkMailboxServiceFetchResponse
		resp, err = c.rpc.Fetch(ctx, &collab.NetworkMailboxServiceFetchRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "receipt":
		var resp *collab.NetworkMailboxServiceReceiptResponse
		resp, err = c.rpc.Receipt(ctx, &collab.NetworkMailboxServiceReceiptRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "local_register":
		if _, ae := decodeLocalRegister(raw); ae != nil {
			return nil, ae
		}
		var resp *collab.NetworkMailboxServiceLocalRegisterResponse
		resp, err = c.rpc.LocalRegister(ctx, &collab.NetworkMailboxServiceLocalRegisterRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "source_register":
		var resp *collab.NetworkMailboxServiceSourceRegisterResponse
		resp, err = c.rpc.SourceRegister(ctx, &collab.NetworkMailboxServiceSourceRegisterRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "source_get":
		var resp *collab.NetworkMailboxServiceSourceGetResponse
		resp, err = c.rpc.SourceGet(ctx, &collab.NetworkMailboxServiceSourceGetRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	case "source_revoke":
		var resp *collab.NetworkMailboxServiceSourceRevokeResponse
		resp, err = c.rpc.SourceRevoke(ctx, &collab.NetworkMailboxServiceSourceRevokeRequest{Connection: id, FrameJson: raw}, options...)
		data = resp.GetFrameJson()
	default:
		return nil, protocolError("", "unsupported mailbox operation")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, mailboxError(err)
	}
	if len(data) == 0 || len(data) > EncodingMaxPayloadBytes {
		return nil, protocolError("", "invalid collab frame size")
	}
	return data, nil
}

func mailboxError(err error) *AppError {
	// Only exact, public mailbox error codes may cross the service boundary.
	// Never copy arbitrary gRPC messages/details (which may contain credentials).
	code := status.Convert(err).Message()
	scope := "request"
	switch code {
	case "network.unauthorized", "network.revoked", CodeConflict, CodeOutcomeUnknown, "network.expired", CodeProtocolError, "network.resume_unavailable", "network.unsupported_app_version":
	case CodeStaleEpoch:
		scope = "connection"
	default:
		switch status.Code(err) {
		case codes.Unauthenticated, codes.PermissionDenied:
			code = "network.unauthorized"
		case codes.InvalidArgument:
			code = CodeProtocolError
		default:
			code = "network.overloaded"
		}
	}
	return &AppError{Code: code, Scope: scope, Retryable: code == "network.overloaded"}
}
