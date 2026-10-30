package network

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Wire uses decimal string for u64/p64 per contracts/network fixtures.

type wireFetch struct {
	AppVersion   string          `json:"app_version"`
	Type         string          `json:"type"`
	RequestID    string          `json:"request_id"`
	Epoch        string          `json:"epoch"`
	ResumeCursor string          `json:"resume_cursor"`
	Continuation json.RawMessage `json:"continuation"`
	MaxItems     int             `json:"max_items"`
	MaxBytes     int             `json:"max_bytes"`
	HeldIDs      []string        `json:"held_ids"`
	Paused       json.RawMessage `json:"paused"`
	StateBarrier []string        `json:"state_barrier"`
}

type wireReceiptResult struct {
	RecipientID          string `json:"recipient_id"`
	Status               string `json:"status"`
	Code                 string `json:"code,omitempty"`
	ExpectedStateVersion string `json:"expected_state_version"`
	AcceptedBy           string `json:"accepted_by,omitempty"`
	AcceptanceMode       string `json:"acceptance_mode,omitempty"`
	ExecutionStatus      string `json:"execution_status,omitempty"`
	MessageDigest        string `json:"message_digest"`
	HostRequestID        string `json:"host_request_id"`
}

type wireReceipt struct {
	AppVersion  string              `json:"app_version"`
	Type        string              `json:"type"`
	RequestID   string              `json:"request_id"`
	OperationID string              `json:"operation_id"`
	Epoch       string              `json:"epoch"`
	DeliveryID  string              `json:"delivery_id"`
	MailSeq     string              `json:"mail_seq"`
	Results     []wireReceiptResult `json:"results"`
}

// FetchRequest is the validated upstream FETCH.
type FetchRequest struct {
	RequestID    string
	Epoch        uint64
	ResumeCursor uint64
	MaxItems     int
	MaxBytes     int
	HeldIDs      []string
}

// ReceiptRequest is the validated upstream RECEIPT.
type ReceiptRequest struct {
	RequestID   string
	OperationID string
	Epoch       uint64
	DeliveryID  string
	MailSeq     uint64
	Results     []ReceiptItem
}

func parseU64(label, s string) (uint64, *AppError) {
	if s == "" || strings.ContainsAny(s, ".eE+") {
		return 0, protocolError("", "bad "+label)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, protocolError("", "bad "+label)
	}
	return v, nil
}

func parseP64(label, s string) (uint64, *AppError) {
	v, err := parseU64(label, s)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return 0, protocolError("", label+" must be positive")
	}
	return v, nil
}

func u64str(v uint64) string { return strconv.FormatUint(v, 10) }

// DecodeUpstream parses a FETCH or RECEIPT frame. Other types → protocol_error.
func DecodeUpstream(raw []byte) (any, *AppError) {
	if len(raw) == 0 || len(raw) > EncodingMaxPayloadBytes {
		return nil, protocolError("", "payload size")
	}
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil || peek.Type == "" {
		return nil, protocolError("", "not json")
	}
	switch peek.Type {
	case "fetch":
		return decodeFetch(raw)
	case "receipt":
		return decodeReceipt(raw)
	default:
		return nil, protocolError("", "unsupported upstream type")
	}
}

func decodeFetch(raw []byte) (*FetchRequest, *AppError) {
	var w wireFetch
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, protocolError("", "fetch json")
	}
	if w.AppVersion != AppVersion || w.Type != "fetch" {
		return nil, protocolError(w.RequestID, "fetch envelope")
	}
	if w.RequestID == "" {
		return nil, protocolError("", "fetch request_id")
	}
	epoch, err := parseP64("epoch", w.Epoch)
	if err != nil {
		err.RequestID = w.RequestID
		return nil, err
	}
	cursor, err := parseU64("resume_cursor", w.ResumeCursor)
	if err != nil {
		err.RequestID = w.RequestID
		return nil, err
	}
	if w.Continuation == nil {
		return nil, protocolError(w.RequestID, "continuation required")
	}
	if w.MaxItems < 1 || w.MaxItems > FetchMaxItems {
		return nil, protocolError(w.RequestID, "max_items")
	}
	if w.MaxBytes < FetchMinBytes || w.MaxBytes > FetchMaxBytes {
		return nil, protocolError(w.RequestID, "max_bytes")
	}
	if len(w.HeldIDs) > 256 {
		return nil, protocolError(w.RequestID, "held_ids")
	}
	return &FetchRequest{
		RequestID:    w.RequestID,
		Epoch:        epoch,
		ResumeCursor: cursor,
		MaxItems:     w.MaxItems,
		MaxBytes:     w.MaxBytes,
		HeldIDs:      append([]string(nil), w.HeldIDs...),
	}, nil
}

func decodeReceipt(raw []byte) (*ReceiptRequest, *AppError) {
	var w wireReceipt
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, protocolError("", "receipt json")
	}
	if w.AppVersion != AppVersion || w.Type != "receipt" {
		return nil, protocolError(w.RequestID, "receipt envelope")
	}
	if w.RequestID == "" || w.OperationID == "" || w.DeliveryID == "" {
		return nil, protocolError(w.RequestID, "receipt ids")
	}
	epoch, err := parseP64("epoch", w.Epoch)
	if err != nil {
		err.RequestID = w.RequestID
		return nil, err
	}
	mailSeq, err := parseP64("mail_seq", w.MailSeq)
	if err != nil {
		err.RequestID = w.RequestID
		return nil, err
	}
	if len(w.Results) < 1 || len(w.Results) > ReceiptMaxResults {
		return nil, protocolError(w.RequestID, "results size")
	}
	seen := map[string]struct{}{}
	out := make([]ReceiptItem, 0, len(w.Results))
	for _, r := range w.Results {
		if r.RecipientID == "" || r.MessageDigest == "" || r.HostRequestID == "" {
			return nil, protocolError(w.RequestID, "result fields")
		}
		if _, ok := seen[r.RecipientID]; ok {
			return nil, protocolError(w.RequestID, "duplicate recipient")
		}
		seen[r.RecipientID] = struct{}{}
		transport, mapErr := mapReceiptStatus(r.Status, r.Code)
		if mapErr != nil {
			mapErr.RequestID = w.RequestID
			return nil, mapErr
		}
		ver, err := parseU64("expected_state_version", r.ExpectedStateVersion)
		if err != nil {
			err.RequestID = w.RequestID
			return nil, err
		}
		out = append(out, ReceiptItem{
			RecipientID:     r.RecipientID,
			TransportStatus: transport,
			StateVersion:    ver,
		})
	}
	return &ReceiptRequest{
		RequestID:   w.RequestID,
		OperationID: w.OperationID,
		Epoch:       epoch,
		DeliveryID:  w.DeliveryID,
		MailSeq:     mailSeq,
		Results:     out,
	}, nil
}

// mapReceiptStatus maps host RECEIPT status to platform transport_status.
func mapReceiptStatus(status, code string) (string, *AppError) {
	switch status {
	case "host_accepted":
		return "host_accepted", nil
	case "rejected":
		if code != "network.target_unavailable" {
			return "", protocolError("", "rejected code")
		}
		return "revoked", nil
	case "quarantined":
		return "quarantined", nil
	case "retryable", "outcome_unknown":
		// Not terminal for cursor; still recorded as protocol-facing states.
		// Platform stores as quarantined/unresolved equivalents for N1 skeleton.
		if status == "outcome_unknown" {
			return "unresolved", nil
		}
		return "quarantined", nil
	default:
		return "", protocolError("", "unknown receipt status")
	}
}

// EncodeHead builds a downstream HEAD tip (no committed_cursor in schema).
func EncodeHead(route Route, auth map[string]any) ([]byte, error) {
	if auth == nil {
		auth = stubAuth(nil, route.ConnectionEpoch)
	}
	m := map[string]any{
		"app_version": AppVersion,
		"type":        "head",
		"epoch":       u64str(route.ConnectionEpoch),
		"head_seq":    u64str(route.HeadSeq),
		"server_auth": auth,
	}
	return json.Marshal(m)
}

// EncodeFetchResult builds a minimal FETCH_RESULT (unsigned skeleton page + items).
func EncodeFetchResult(req *FetchRequest, route Route, items []DeliveryItem, auth map[string]any) ([]byte, error) {
	if auth == nil {
		auth = stubAuth(&req.RequestID, route.ConnectionEpoch)
	}
	manifest := make([]map[string]any, 0, len(items))
	pageItems := make([]map[string]any, 0, len(items))
	var scanAfter uint64
	for _, it := range items {
		if it.MailSeq > scanAfter {
			scanAfter = it.MailSeq
		}
		digest := fmt.Sprintf("sha256:skeleton-%s", it.DeliveryID)
		manifest = append(manifest, map[string]any{
			"item_type": "delivery",
			"mail_seq":  u64str(it.MailSeq),
			"digest":    digest,
		})
		recs := make([]map[string]any, 0, len(it.Recipients))
		for _, r := range it.Recipients {
			recs = append(recs, map[string]any{
				"recipient_id":     r.RecipientID,
				"local":            r.Local,
				"transport_status": r.TransportStatus,
				"state_version":    u64str(r.StateVersion),
			})
		}
		pageItems = append(pageItems, map[string]any{
			"item_type":   "delivery",
			"delivery_id": it.DeliveryID,
			"mail_seq":    u64str(it.MailSeq),
			"message_id":  it.MessageID,
			"kind":        it.Kind,
			"body":        it.Body,
			"recipients":  recs,
		})
	}
	if scanAfter == 0 {
		scanAfter = req.ResumeCursor
	}
	page := map[string]any{
		"app_version":       AppVersion,
		"type":              "fetch_page",
		"request_id":        req.RequestID,
		"epoch":             u64str(route.ConnectionEpoch),
		"head_seq":          u64str(route.HeadSeq),
		"committed_cursor":  u64str(route.CommittedCursor),
		"scan_after":        u64str(scanAfter),
		"snapshot_head":     u64str(route.HeadSeq),
		"next_continuation": nil,
		"manifest":          manifest,
		"server_auth":       auth,
	}
	m := map[string]any{
		"app_version": AppVersion,
		"type":        "fetch_result",
		"page":        page,
		"items":       pageItems,
	}
	return json.Marshal(m)
}

// EncodeReceiptCommitted builds RECEIPT_COMMITTED.
func EncodeReceiptCommitted(req *ReceiptRequest, commit ReceiptCommit, auth map[string]any) ([]byte, error) {
	if auth == nil {
		auth = stubAuth(&req.RequestID, req.Epoch)
	}
	results := make([]map[string]any, 0, len(commit.Results))
	for _, r := range commit.Results {
		results = append(results, map[string]any{
			"recipient_id":     r.RecipientID,
			"transport_status": r.TransportStatus,
			"state_version":    u64str(r.StateVersion),
		})
	}
	m := map[string]any{
		"app_version":      AppVersion,
		"type":             "receipt_committed",
		"request_id":       req.RequestID,
		"operation_id":     req.OperationID,
		"delivery_id":      commit.DeliveryID,
		"mail_seq":         u64str(commit.MailSeq),
		"results":          results,
		"mail_item_closed": commit.MailItemClosed,
		"committed_cursor": u64str(commit.CommittedCursor),
		"server_auth":      auth,
	}
	return json.Marshal(m)
}

// EncodeError builds an ERROR frame (server_auth stub).
func EncodeError(err *AppError, auth map[string]any) ([]byte, error) {
	if auth == nil {
		auth = stubAuth(nil, 0)
	}
	var reqID any
	if err.RequestID == "" {
		reqID = nil
	} else {
		reqID = err.RequestID
	}
	m := map[string]any{
		"app_version": AppVersion,
		"type":        "error",
		"request_id":  reqID,
		"code":        err.Code,
		"scope":       err.Scope,
		"retryable":   err.Retryable,
		"retry_after": nil,
		"server_auth": auth,
	}
	return json.Marshal(m)
}

func stubAuth(requestID *string, epoch uint64) map[string]any {
	var rid any
	if requestID == nil || *requestID == "" {
		rid = nil
	} else {
		rid = *requestID
	}
	return map[string]any{
		"version":        1,
		"purpose":        "protocol_response",
		"alg":            "Ed25519",
		"kid":            "stub-unsigned",
		"issuer_realm":   "0",
		"issued_at":      "1970-01-01T00:00:00Z",
		"not_after":      "1970-01-01T00:00:00Z",
		"audience":       map[string]any{"realm": "0", "node": "0", "incarnation": "stub", "epoch": u64str(epoch)},
		"request_id":     rid,
		"channel_nonce":  "stub-nonce-00000000",
		"keyset_version": "1",
		"signature":      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
}
