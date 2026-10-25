package network

import "fmt"

// Known codes from contracts/network/errors.json used by this adapter.
const (
	CodeProtocolError = "network.protocol_error"
	CodeStaleEpoch    = "network.stale_epoch"
	CodeQuotaExceeded = "network.quota_exceeded"
	CodeConflict      = "network.conflict"
)

// AppError is a network.* failure returned as an ERROR frame.
type AppError struct {
	Code      string
	Scope     string // request | recipient | contact | connection | node
	Retryable bool
	RequestID string // may be empty → JSON null
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s (%s)", e.Code, e.Scope)
}

func protocolError(requestID, detail string) *AppError {
	_ = detail // detail stays server-side; never leak into ERROR frame
	return &AppError{Code: CodeProtocolError, Scope: "request", RequestID: requestID}
}

func staleEpoch(requestID string) *AppError {
	return &AppError{Code: CodeStaleEpoch, Scope: "connection", RequestID: requestID}
}

func quotaExceeded(requestID string) *AppError {
	return &AppError{Code: CodeQuotaExceeded, Scope: "request", RequestID: requestID}
}
