package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaximumDomainListsFitChannel(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "policy.json")
	domains := make([]string, 256)
	for i := range domains {
		domains[i] = fmt.Sprintf("%03d.%s.%s.%s.%s.example.com", i, strings.Repeat("a", 55), strings.Repeat("b", 55), strings.Repeat("c", 55), strings.Repeat("d", 55))
	}
	raw, err := json.Marshal(Snapshot{SchemaVersion: 1, TenantId: "00000000-0000-0000-0000-000000000001", Version: 1, Value: Value{Categories: []string{}, AllowDomains: domains, DenyDomains: domains}})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 65536 {
		t.Fatal("fixture does not exercise the old size limit")
	}
	if err = os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runner, err := New(source, filepath.Join(dir, "state.json"), filepath.Join(dir, "report.json"), "egress-primary", &applied{})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Sync(); err != nil {
		t.Fatal(err)
	}
}

type applied struct {
	calls int
	value Value
}

func (a *applied) ApplyPolicy(v Value) { a.calls++; a.value = v }
func TestMonotonicStateAndRestart(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "policy.json")
	state := filepath.Join(dir, "state.json")
	report := filepath.Join(dir, "report.json")
	a := &applied{}
	runner, err := New(source, state, report, "egress-primary", a)
	if err != nil {
		t.Fatal(err)
	}
	write := func(version int64, domains []string) {
		t.Helper()
		raw, e := json.Marshal(Snapshot{SchemaVersion: 1, TenantId: "00000000-0000-0000-0000-000000000001", Version: version, Value: Value{AllowDomains: domains}})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(source, raw, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	write(2, []string{"api.example.com"})
	if err = runner.Sync(); err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 {
		t.Fatal(a.calls)
	}
	if err = runner.Sync(); err != nil || a.calls != 1 {
		t.Fatal(err, a.calls)
	}
	b := &applied{}
	runner, err = New(source, state, report, "egress-primary", b)
	if err != nil || b.calls != 1 {
		t.Fatal(err, b.calls)
	}
	write(1, nil)
	if runner.Sync() == nil {
		t.Fatal("rollback accepted")
	}
	write(2, []string{"other.example.com"})
	if runner.Sync() == nil {
		t.Fatal("equivocation accepted")
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Result != "failed" || r.AppliedVersion != 2 || b.calls != 1 {
		t.Fatal(r, b.calls)
	}
	write(3, nil)
	if err = runner.Sync(); err != nil || b.calls != 2 {
		t.Fatal(err, b.calls)
	}
	if err = os.WriteFile(state, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(source, state, report, "egress-primary", &applied{}); err == nil {
		t.Fatal("corrupt durable state accepted")
	}
}

func TestRejectInvalidPolicy(t *testing.T) {
	for _, host := range []string{"10.0.0.1", "169.254.169.254", "100.64.0.1", "localhost", "metadata.google.internal", "https://api.example.com", "*.com"} { // gitleaks:allow
		if Validate(Value{AllowDomains: []string{host}}) == nil {
			t.Fatal(host)
		}
	}
	if _, err := decode([]byte(`{"schemaVersion":1,"tenantId":"00000000-0000-0000-0000-000000000001","version":1,"value":{"allowPrivate":true}}`)); err == nil {
		t.Fatal("unknown safety switch accepted")
	}
}

func TestPersistenceFailureReportsUnappliedVersion(t *testing.T) {
	dir := t.TempDir()
	source, report := filepath.Join(dir, "policy.json"), filepath.Join(dir, "report.json")
	a := &applied{}
	runner, err := New(source, filepath.Join(dir, "absent", "state.json"), report, "egress-primary", a)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":1,"tenantId":"00000000-0000-0000-0000-000000000001","version":1,"value":{"categories":[],"allowDomains":[],"denyDomains":[]}}`)
	if err = os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if runner.Sync() == nil {
		t.Fatal("state persistence unexpectedly succeeded")
	}
	bytes, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var result Report
	if err = json.Unmarshal(bytes, &result); err != nil {
		t.Fatal(err)
	}
	if result.Result != "failed" || result.AppliedVersion != 0 || a.calls != 0 {
		t.Fatal(result, a.calls)
	}
}
