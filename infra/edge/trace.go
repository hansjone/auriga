package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const headerRequestID = "X-Request-Id"
const headerTraceparent = "traceparent"
const maxRequestIDLen = 128

// Only fixed service prefixes are observable before routing; arbitrary path
// segments can be bearer credentials, including on unmatched/rejected routes.
func logRequestPath(path string) string {
	for _, service := range []string{"iam", "org", "workspace", "collab", "ai", "ops", "audit", "resource", "knowledge"} {
		prefix := "/api/" + service + "/v1"
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return prefix + "/**"
		}
	}
	return "/**"
}

var (
	requestIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	traceparentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota + 1
	ctxKeyClientIP
	ctxKeyTraceparent
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
	if v := r.Header.Get(headerTraceparent); len(r.Header.Values(headerTraceparent)) == 1 && validTraceparent(v) {
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
	return len(v) == 55 && traceparentPattern.MatchString(v) && strings.HasPrefix(v, "00-") && v[3:35] != strings.Repeat("0", 32) && v[36:52] != strings.Repeat("0", 16)
}

func newRequestID() string { return newHex(16) }

func newHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const secretKeys = `password|passwd|pwd|secret|(?:access|refresh|session|id)[_-]?token|token|client[_-]?secret|credential|totp|private[_-]?key|api[_-]?key|[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?|authorization|cookie|set-cookie`

var secretKey = regexp.MustCompile(`(?i)^(?:` + secretKeys + `)$`)
var cookieHeader = regexp.MustCompile(`(?i)(\b(?:set-)?cookie\s*[:=]\s*)[^\r\n]+`)
var bearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
var jwt = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]*`)
var privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
var userinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*):[^/\s@]+@`)

const maxLogLength = 65536

var logKeys = regexp.MustCompile(`["']?([A-Za-z][A-Za-z0-9_-]*)["']?\s*[:=]\s*`)

// Classify each complete key once, and skip a masked value without parsing it.
func maskLogFields(text string) string {
	var out strings.Builder
	cursor := 0
	for _, match := range logKeys.FindAllStringSubmatchIndex(text, -1) {
		if match[0] < cursor {
			continue
		}
		key := text[match[2]:match[3]]
		escapedLine := match[0] > 0 && text[match[0]-1] == '\\' && strings.ContainsRune("nrt", rune(key[0]))
		sensitive := secretKey.MatchString(key) || secretKey.MatchString(strings.ReplaceAll(key, "-", "_")) || (escapedLine && secretKey.MatchString(key[1:]))
		if !sensitive {
			continue
		}
		start, end := match[1], match[1]
		quote := byte(0)
		if start < len(text) {
			quote = text[start]
		}
		out.WriteString(text[cursor:start])
		if quote == '"' || quote == '\'' {
			end++
			for end < len(text) {
				c := text[end]
				end++
				if c == '\\' {
					end++
				} else if c == quote {
					break
				}
			}
			end = min(end, len(text))
			out.WriteByte(quote)
			out.WriteString("****")
			out.WriteByte(quote)
		} else {
			for end < len(text) && !strings.ContainsRune(" \t\r\n,;&}\"'", rune(text[end])) {
				end++
			}
			out.WriteString("****")
		}
		cursor = end
	}
	out.WriteString(text[cursor:])
	return out.String()
}

func Mask(s string) string {
	if len(s) > maxLogLength {
		return "[log omitted: size limit]"
	}
	s = privateKeyPattern.ReplaceAllString(s, "****")
	s = userinfo.ReplaceAllString(s, "$1:****@")
	s = bearer.ReplaceAllString(s, "$1 ****")
	s = jwt.ReplaceAllString(s, "****")
	s = maskLogFields(s)
	return cookieHeader.ReplaceAllString(s, "$1****")
}

func edgeTraceparent(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTraceparent).(string)
	return v
}
func newLogLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("AURIGA_LOG_LEVEL"), "DEBUG") {
		level = slog.LevelDebug
	}
	return slog.New(edgeLogHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if secretKey.MatchString(a.Key) {
			a.Value = slog.StringValue("****")
			return a
		}
		if a.Value.Kind() == slog.KindString || a.Value.Kind() == slog.KindAny {
			a.Value = slog.StringValue(strings.TrimPrefix(Mask(a.Key+"="+fmt.Sprint(a.Value.Any())), a.Key+"="))
		}
		return a
	}})}).With("service", "edge")
}

type edgeLogHandler struct{ slog.Handler }

func (h edgeLogHandler) Handle(ctx context.Context, r slog.Record) error {
	present := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "trace_id" {
			present = true
		}
		return true
	})
	if !present {
		parent := edgeTraceparent(ctx)
		id := ""
		if validTraceparent(parent) {
			id = parent[3:35]
		}
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}
func (h edgeLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return edgeLogHandler{h.Handler.WithAttrs(attrs)}
}
func (h edgeLogHandler) WithGroup(name string) slog.Handler {
	return edgeLogHandler{h.Handler.WithGroup(name)}
}
