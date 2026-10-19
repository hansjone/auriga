package config_test

import (
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/config"
)

func TestAllowInsecureFromEnv(t *testing.T) {
	t.Setenv("AURIGA_AGENT_ALLOW_INSECURE", "true")
	t.Setenv("AURIGA_AGENT_DATA_ROOT", t.TempDir())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowInsecure {
		t.Fatal("expected allowInsecure")
	}
}
