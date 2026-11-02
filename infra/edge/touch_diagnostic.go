package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/status"
)

// Diagnostic-only metadata. It never selects credentials or affects renewal.
type touchDiagnostic struct {
	stage, reason, grpcStatus, detail string
	cacheHit                          bool
	projection                        string
}
type touchDiagnosticKey struct{}

func captureTouchRPC(ctx context.Context, stage string, err error) {
	if d, ok := ctx.Value(touchDiagnosticKey{}).(*touchDiagnostic); ok {
		d.stage, d.grpcStatus = stage, status.Code(err).String()
		if err != nil {
			d.detail = fmt.Sprintf("%T: %v", err, err)
		}
	}
}

func (g *Gateway) logTouch(ctx context.Context, s *Session, d *touchDiagnostic, started time.Time, cookie string) {
	if g.Log == nil {
		return
	}
	detail := safeTouchDetail(d.detail, s, cookie)
	g.Log.InfoContext(ctx, "Browser session touch", "session_id", s.SessionID,
		"tenant_id", s.TenantID, "stage", d.stage, "reason", d.reason,
		"grpc_status", d.grpcStatus, "error", detail, "cache_hit", d.cacheHit,
		"projection", d.projection, "elapsed_ms", time.Since(started).Milliseconds())
}

func safeTouchDetail(detail string, s *Session, cookie string) string {
	for _, secret := range []string{cookie, s.AccessToken, s.CSRFHash} {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "****")
		}
	}
	return Mask(detail)
}
