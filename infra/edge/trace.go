package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

const headerRequestID = "X-Request-Id"
const headerTraceparent = "traceparent"
const maxRequestIDLen = 128

var (
	requestIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	traceparentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota + 1
	ctxKeyClientIP
)

func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get(headerRequestID)); v != "" {
		return v
	}
	return ""
}

func ensureRequestID(r *http.Request) (string, *http.Request) {
	id := strings.TrimSpace(r.Header.Get(headerRequestID))
	if !validRequestID(id) {
		id = newRequestID()
	}
	ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
	nr := r.WithContext(ctx)
	nr.Header.Set(headerRequestID, id)
	return id, nr
}

func ensureTraceparent(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(headerTraceparent)); validTraceparent(v) {
		return v
	}
	return "00-" + newHex(16) + "-" + newHex(8) + "-01"
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	return requestIDPattern.MatchString(id)
}

func validTraceparent(v string) bool {
	return traceparentPattern.MatchString(v)
}

func newRequestID() string { return newHex(16) }

func newHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
