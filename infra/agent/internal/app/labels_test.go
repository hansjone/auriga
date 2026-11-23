package app

import (
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
)

func TestResolveNodeLabels(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := resolveNodeLabels(policy.Default(), "")
		if err != nil || got != nil {
			t.Fatalf("got=%#v err=%v", got, err)
		}
	})

	t.Run("env_only", func(t *testing.T) {
		got, err := resolveNodeLabels(policy.Default(), "template=real-dsh,zone=nas")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got["template"] != "real-dsh" || got["zone"] != "nas" {
			t.Fatalf("got=%#v", got)
		}
	})

	t.Run("policy_overrides_env", func(t *testing.T) {
		pol := policy.Default()
		pol.Labels = map[string]string{"template": "real-dsh", "pool": "dedicated"}
		got, err := resolveNodeLabels(pol, "template=other,zone=nas")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || got["template"] != "real-dsh" || got["pool"] != "dedicated" || got["zone"] != "nas" {
			t.Fatalf("got=%#v", got)
		}
	})

	t.Run("rejects_reserved_env", func(t *testing.T) {
		if _, err := resolveNodeLabels(policy.Default(), "auriga.shared-dsh-runtime=prepare-v1"); err == nil {
			t.Fatal("expected error for reserved env label")
		}
	})

	t.Run("rejects_reserved_policy", func(t *testing.T) {
		pol := policy.Default()
		pol.Labels = map[string]string{"auriga.shared-dsh-process": "persistent-v1"}
		if _, err := resolveNodeLabels(pol, ""); err == nil {
			t.Fatal("expected error for reserved policy label")
		}
	})
}
