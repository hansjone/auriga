// Package observability carries correlation metadata only; it never establishes identity.
package observability

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
)

type key struct{}

var pattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

const secretKeys = `password|passwd|pwd|secret|(?:access|refresh|session|id)[_-]?token|token|client[_-]?secret|credential|totp|private[_-]?key|api[_-]?key|[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?|authorization|cookie|set-cookie`

var secretKey = regexp.MustCompile(`(?i)^(?:` + secretKeys + `)$`)
var cookieHeader = regexp.MustCompile(`(?i)(\b(?:set-)?cookie\s*[:=]\s*)[^\r\n]+`)
var bearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
var jwt = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]*`)
var pem = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
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
	s = pem.ReplaceAllString(s, "****")
	s = userinfo.ReplaceAllString(s, "$1:****@")
	s = bearer.ReplaceAllString(s, "$1 ****")
	s = jwt.ReplaceAllString(s, "****")
	s = maskLogFields(s)
	return cookieHeader.ReplaceAllString(s, "$1****")
}

func Valid(s string) bool {
	return len(s) == 55 && pattern.MatchString(s) && s[3:35] != strings.Repeat("0", 32) && s[36:52] != strings.Repeat("0", 16)
}
func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Continue creates a local span. Background work passes an empty parent.
func Continue(ctx context.Context, parent string) context.Context {
	if !Valid(parent) {
		parent = "00-" + random(16) + "-" + random(8) + "-01"
	} else {
		parent = parent[:36] + random(8) + parent[52:]
	}
	return context.WithValue(ctx, key{}, parent)
}

// NATSContext correlates the existing logical request ID without using headers,
// which would consume the broker's unchanged max_payload budget. This hash is
// observation only and is never used to establish identity or admission.
func NATSContext(ctx context.Context, raw []byte) context.Context {
	var request struct {
		RequestID string `json:"request_id"`
	}
	if len(raw) <= 65536 && json.Unmarshal(raw, &request) == nil && natsID.MatchString(request.RequestID) {
		if join := joinID.FindStringSubmatch(request.RequestID); len(join) == 2 && join[1] != strings.Repeat("0", 32) {
			return context.WithValue(ctx, key{}, "00-"+join[1]+"-"+random(8)+"-01")
		}
		sum := sha256.Sum256([]byte("auriga-nats-request-v1:" + request.RequestID))
		return context.WithValue(ctx, key{}, "00-"+hex.EncodeToString(sum[:16])+"-"+random(8)+"-01")
	}
	return Continue(ctx, "")
}

var natsID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
var joinID = regexp.MustCompile(`^join-([a-f0-9]{32})-[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

func Parent(ctx context.Context) string { v, _ := ctx.Value(key{}).(string); return v }
func TraceID(ctx context.Context) string {
	if v := Parent(ctx); Valid(v) {
		return v[3:35]
	}
	return ""
}
func Outgoing(ctx context.Context) context.Context {
	parent := Parent(ctx)
	if !Valid(parent) {
		return ctx
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("traceparent", parent)
	return metadata.NewOutgoingContext(ctx, md)
}

// Begin does not wrap ResponseWriter: deadlines, streaming and Hijacker remain native.
func Begin(w http.ResponseWriter, r *http.Request) (*http.Request, func()) {
	parent := ""
	if v := r.Header.Values("traceparent"); len(v) == 1 {
		parent = v[0]
	}
	ctx := Continue(r.Context(), parent)
	w.Header().Set("traceparent", Parent(ctx))
	w.Header().Set("X-Trace-Id", TraceID(ctx))
	started := time.Now()
	slog.InfoContext(ctx, "HTTP request received", "method", r.Method, "path", r.URL.Path)
	return r.WithContext(ctx), func() {
		if cause := recover(); cause != nil {
			if cause != http.ErrAbortHandler {
				slog.ErrorContext(ctx, "HTTP panic", "error", cause, "exception", string(debug.Stack()))
			}
			panic(cause)
		}
		slog.InfoContext(ctx, "HTTP request completed", "method", r.Method, "path", r.URL.Path, "elapsed_ms", time.Since(started).Milliseconds())
	}
}

type handler struct{ slog.Handler }

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.String("trace_id", TraceID(ctx)))
	return h.Handler.Handle(ctx, r)
}
func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{h.Handler.WithAttrs(attrs)}
}
func (h handler) WithGroup(name string) slog.Handler { return handler{h.Handler.WithGroup(name)} }
func Logger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("AURIGA_LOG_LEVEL"), "DEBUG") {
		level = slog.LevelDebug
	}
	return slog.New(handler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if secretKey.MatchString(a.Key) {
			a.Value = slog.StringValue("****")
			return a
		}
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(strings.TrimPrefix(Mask(a.Key+"="+a.Value.String()), a.Key+"="))
		}
		if a.Value.Kind() == slog.KindAny {
			a.Value = slog.StringValue(strings.TrimPrefix(Mask(a.Key+"="+fmt.Sprint(a.Value.Any())), a.Key+"="))
		}
		return a
	}})}).With("service", "ai-gateway")
}
