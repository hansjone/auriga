// Package telemetry carries observation data only. It never authorizes a runtime operation.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"regexp"
	"strings"
)

type key struct{}
type Span struct {
	Parent     string
	Value      string
	Instance   string
	Generation int64
	Logger     *slog.Logger
}

var parentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

func Valid(v string) bool {
	return len(v) == 55 && parentPattern.MatchString(v) && v[3:35] != strings.Repeat("0", 32) && v[36:52] != strings.Repeat("0", 16)
}

// WithParent marks a control message. Only Value crosses the durable queue boundary,
// never the session's deadline, cancellation or credentials.
func WithParent(ctx context.Context, parent string) context.Context {
	if !Valid(parent) {
		parent = ""
	}
	return context.WithValue(ctx, key{}, Span{Value: parent})
}
func Current(ctx context.Context) Span { s, _ := ctx.Value(key{}).(Span); return s }
func randomID(n int) string {
	b := make([]byte, n)
	for {
		if _, err := rand.Read(b); err != nil {
			return ""
		}
		id := hex.EncodeToString(b)
		if id != strings.Repeat("0", n*2) {
			return id
		}
	}
}
func Start(ctx context.Context, parent, instance string, generation int64, logger *slog.Logger) context.Context {
	s := Span{Instance: instance, Generation: generation, Logger: logger}
	span := randomID(8)
	if Valid(parent) {
		s.Parent = parent[36:52]
		s.Value = "00-" + parent[3:35] + "-" + span + "-" + parent[53:55]
	} else {
		s.Value = "00-" + randomID(16) + "-" + span + "-01"
	}
	if !Valid(s.Value) {
		s.Value = ""
	}
	return context.WithValue(ctx, key{}, s)
}
func Child(ctx context.Context) context.Context {
	s := Current(ctx)
	return Start(ctx, s.Value, s.Instance, s.Generation, s.Logger)
}
func Log(ctx context.Context, level slog.Level, message string, fields ...any) {
	s := Current(ctx)
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{"instance_id", s.Instance, "generation", s.Generation}
	if Valid(s.Value) {
		attrs = append(attrs, "trace_id", s.Value[3:35], "span_id", s.Value[36:52], "parent_span_id", s.Parent)
	}
	logger.Log(ctx, level, message, append(attrs, fields...)...)
}

var privateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var jwt = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]*`)
var bearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
var credential = regexp.MustCompile(`(?i)(["']?\b(?:[a-z0-9_-]*(?:password|secret|credential|token|ticket|claim|private_key|api_key)|authorization|cookie|totp)["']?\s*[:=]\s*)(?:"(?:[^"\\]|\\.)*"|'[^']*'|[^\s,;&}]+)`)
var userinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*):[^/\s@]+@`)

// Mask preserves diagnostic messages rather than dropping entire error fields.
func Mask(value string) string {
	if len(value) > 65536 {
		return "[diagnostic omitted: size limit]"
	}
	value = privateKey.ReplaceAllString(value, "****")
	value = jwt.ReplaceAllString(value, "****")
	value = bearer.ReplaceAllString(value, "$1 ****")
	value = credential.ReplaceAllString(value, "$1****")
	return userinfo.ReplaceAllString(value, "$1:****@")
}
