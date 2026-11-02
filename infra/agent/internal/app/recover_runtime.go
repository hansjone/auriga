package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
)

func recoverRuntime(args []string, out, logs io.Writer) error {
	fs := flag.NewFlagSet("recover-runtime", flag.ContinueOnError)
	fs.SetOutput(logs)
	data := fs.String("data-root", "", "original agent data root (required)")
	instance := fs.String("instance", "", "recorded affected instance ID")
	agent := fs.String("stopped-agent", "", "exact original stopped compose agent container ID")
	expect := fs.String("expect", "", "apply only this inspect expectation; empty means inspect only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *data == "" || !filepath.IsAbs(*data) || *instance == "" || *agent == "" {
		return fmt.Errorf("recover-runtime requires --data-root, --instance and --stopped-agent")
	}
	cfg := config.Default()
	cfg.DataRoot = filepath.Clean(*data)
	store, err := state.OpenOfflineRecovery(cfg.AgentDir())
	if err != nil {
		return err
	}
	defer store.Close()
	policyPath := os.Getenv("AURIGA_AGENT_POLICY_FILE")
	if policyPath == "" {
		policyPath = filepath.Join(cfg.DataRoot, "agent-policy.yaml")
	}
	pol, err := policy.Load(policyPath)
	if err != nil {
		return fmt.Errorf("load original policy: %w", err)
	}
	d, err := runtime.NewDocker(runtime.DockerConfig{DataRoot: cfg.DataRoot, Policy: pol})
	if err != nil {
		return err
	}
	proofs := os.Getenv("AURIGA_AGENT_RUNTIME_PROOFS_DIR")
	if proofs == "" {
		proofs = filepath.Join(cfg.AgentDir(), "runtime-proofs")
	}
	d.Activation = &runtime.ReleaseActivation{Store: store, ProofsDir: proofs, Policy: func() policy.Policy { return d.Policy }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snapshot, err := d.RecoverInterruptedRuntime(ctx, store, *instance, *agent, *expect)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		state.RecoverySnapshot
		Applied bool `json:"applied"`
	}{snapshot, *expect != ""})
}
