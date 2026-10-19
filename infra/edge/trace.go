package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

const headerRequestID = "X-Request-Id"
const headerTraceparent = "traceparent"

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota + 1
	ctxKeyClientIP
)

func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get(headerRequestID)); v != "" {
		return v
	}
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

func ensureRequestID(r *http.Request) (string, *http.Request) {
	id := strings.TrimSpace(r.Header.Get(headerRequestID))
	if id == "" {
		id = newRequestID()
	}
	ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
	nr := r.WithContext(ctx)
	nr.Header.Set(headerRequestID, id)
	return id, nr
}

func ensureTraceparent(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(headerTraceparent)); v != "" {
		return v
	}
	return "00-" + newHex(16) + "-" + newHex(8) + "-01"
}

func newRequestID() string { return newHex(16) }

func newHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
