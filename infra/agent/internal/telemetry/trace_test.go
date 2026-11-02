package telemetry

import (
	"context"
	"strings"
	"testing"
)

func TestTraceChildrenAndInvalidParents(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := Start(context.Background(), parent, "instance-a", 7, nil)
	child := Current(Child(ctx))
	first := Current(ctx)
	if !Valid(child.Value) || child.Value[3:35] != parent[3:35] || first.Parent != parent[36:52] || child.Parent != first.Value[36:52] || child.Value == first.Value {
		t.Fatal("broken span ancestry")
	}
	for _, invalid := range []string{"", strings.Repeat("a", 10000), "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", parent + "," + parent} {
		fresh := Current(Start(context.Background(), invalid, "instance-b", 8, nil))
		if !Valid(fresh.Value) || fresh.Parent != "" || fresh.Value[3:35] == parent[3:35] {
			t.Fatal("invalid parent reused")
		}
	}
}

func TestDiagnosticMaskKeepsBusinessContext(t *testing.T) {
	original := `instance_id=alice generation=7 source.go:42 error="failed" password=sensitive1 {"access_token":"sensitive2"} Authorization: Bearer sensitive3 ticket=sensitive4 eyJhbGciOiJ4In0.eyJ4IjoxfQ.signature`
	got := Mask(original)
	for _, secret := range []string{"sensitive1", "sensitive2", "sensitive3", "sensitive4", "eyJhbGci"} {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic leaked %s", secret)
		}
	}
	for _, kept := range []string{"alice", "generation=7", "source.go:42", "failed"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("business context lost: %s", kept)
		}
	}
	if strings.Contains(Mask("-----BEGIN PRIVATE KEY-----\nprivate-sentinel"), "private-sentinel") {
		t.Fatal("unterminated key leaked")
	}
}
