package relayhttp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
)

func TestInstalledRC2AdapterRuntimeHTTP(t *testing.T) {
	anchor := os.Getenv("AURIGA_RELAY_DSH_ANCHOR")
	if anchor == "" {
		t.Skip("AURIGA_RELAY_DSH_ANCHOR absent: actual installed rc.2 public loader/LlmRuntime probe not run")
	}
	if _, err := os.Stat(anchor); err != nil {
		t.Fatalf("explicit installed SDK anchor unavailable: %v", err)
	}
	f := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, func(ctx context.Context, s relay.Submission, _ []byte) (relay.Exchange, error) {
		calls.Add(1)
		stop := make(chan struct{})
		close(stop)
		body := completion(textChoice)
		if s.Request.GetStream() {
			body = sse(textChoice)
		}
		return relay.Exchange{Body: io.NopCloser(strings.NewReader(body)), Stopped: stop}, nil
	})
	script, err := filepath.Abs("../../../../e2e/relay-device/sdk-probe.mjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	// A raw synthetic test ticket is passed only to the child environment, never
	// argv, logs, files or user configuration. The probe uses global real fetch.
	command.Env = append(os.Environ(), "AURIGA_RELAY_PROBE_URL="+s.URL, "AURIGA_RELAY_PROBE_TOKEN="+f.raw, "AURIGA_RELAY_PROBE_HOME="+t.TempDir())
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual SDK -> HTTP probe: %v\n%s", err, out)
	}
	if calls.Load() != 2 || !strings.Contains(string(out), `"realRuntime":true`) {
		t.Fatalf("missing real JSON/SSE calls=%d: %s", calls.Load(), out)
	}
	t.Log(string(out))
}
