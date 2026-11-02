package modelgate

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestModelGateDiagnosticStagesAndFinalRedaction(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(observability.Logger(&output))
	t.Cleanup(func() { slog.SetDefault(old) })
	for _, mode := range []string{"normal", "inactive", "expired", "permission"} {
		t.Run(mode, func(t *testing.T) {
			s, a, _ := controlFixture(t)
			if mode == "inactive" {
				s.verify.Active = false
			}
			if mode == "expired" {
				s.verify.ExpiresAt = timestamppb.New(s.now.Add(-time.Second))
			}
			if mode == "permission" {
				s.permissions.Permissions = nil
			}
			_, err := a.Check(t.Context(), s.raw)
			if (err == nil) != (mode == "normal") {
				t.Fatalf("changed auth result %v", err)
			}
		})
	}
	slog.Warn("fixture safe cause", "error", "connection reset password=sentinel-password ticket=sentinel-ticket Bearer sentinel-bearer amc.fake.sentinel-managed", "instance_id", "business-instance", "code", "common.unavailable")
	for _, value := range []string{"sentinel-password", "sentinel-ticket", "sentinel-bearer", "sentinel-managed"} {
		if strings.Contains(output.String(), value) {
			t.Fatal("final logger leaked secret")
		}
	}
	for _, value := range []string{`"stage":"iam"`, `"stage":"permission"`, `"active":false`, `"reason":"authorized"`, "connection reset", "business-instance"} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("lost diagnostic %s", value)
		}
	}
}
