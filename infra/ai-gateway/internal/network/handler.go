package network

import (
	"context"
)

// Handler serves HEAD tip / FETCH / RECEIPT against a Mailbox port.
// Identity must come from authenticated ingress; payload never supplies tenant/node.
type Handler struct {
	Mailbox Mailbox
}

// Head returns current route for a HEAD tip (downstream).
func (h *Handler) Head(ctx context.Context, id NodeIdentity) (Route, *AppError) {
	route, err := h.Mailbox.Route(ctx, id)
	if err != nil {
		return Route{}, asAppError(err, "")
	}
	return route, nil
}

// HandleUpstream decodes raw JSON and returns a response frame (or ERROR).
func (h *Handler) HandleUpstream(ctx context.Context, id NodeIdentity, raw []byte) ([]byte, error) {
	msg, decErr := DecodeUpstream(raw)
	if decErr != nil {
		return EncodeError(decErr, nil)
	}
	switch req := msg.(type) {
	case *FetchRequest:
		return h.handleFetch(ctx, id, req)
	case *ReceiptRequest:
		return h.handleReceipt(ctx, id, req)
	default:
		return EncodeError(protocolError("", "type"), nil)
	}
}

func (h *Handler) handleFetch(ctx context.Context, id NodeIdentity, req *FetchRequest) ([]byte, error) {
	if req.Epoch != id.Epoch {
		return EncodeError(staleEpoch(req.RequestID), nil)
	}
	limit := req.MaxItems
	if limit > FetchMaxItems {
		limit = FetchMaxItems
	}
	route, items, err := h.Mailbox.Fetch(ctx, id, req.ResumeCursor, limit)
	if err != nil {
		return EncodeError(asAppError(err, req.RequestID), nil)
	}
	if route.ConnectionEpoch != id.Epoch {
		return EncodeError(staleEpoch(req.RequestID), nil)
	}
	return EncodeFetchResult(req, route, items, nil)
}

func (h *Handler) handleReceipt(ctx context.Context, id NodeIdentity, req *ReceiptRequest) ([]byte, error) {
	if req.Epoch != id.Epoch {
		return EncodeError(staleEpoch(req.RequestID), nil)
	}
	commit, err := h.Mailbox.Receipt(ctx, id, req.DeliveryID, req.MailSeq, req.Results)
	if err != nil {
		return EncodeError(asAppError(err, req.RequestID), nil)
	}
	return EncodeReceiptCommitted(req, commit, nil)
}

func asAppError(err error, requestID string) *AppError {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*AppError); ok {
		if ae.RequestID == "" {
			ae.RequestID = requestID
		}
		return ae
	}
	return &AppError{Code: CodeProtocolError, Scope: "request", RequestID: requestID}
}
