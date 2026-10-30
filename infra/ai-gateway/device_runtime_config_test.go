package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
)

func validDeviceConfig() deviceConfig {
	return deviceConfig{Listen: "127.0.0.1:0", MasterKeyFile: "master.key", Store: store.Config{DSN: "postgres://alice@127.0.0.1/auriga_d7", MigrationDSN: "postgres://alice@127.0.0.1/auriga_d7"}, IAM: deviceRPCConfig{Target: "127.0.0.1:1", ServiceTokenFile: "iam.jwt"}, Org: deviceRPCConfig{Target: "127.0.0.1:2", ServiceTokenFile: "org.jwt"}, D7SyntheticOnly: true, SyntheticTenant: "01900000-0000-7000-8000-000000000001", Capacity: 1, MaxConnections: 2, MaxInFlight: 1, HeaderMillis: 100, ReadMillis: 100, WriteMillis: 100, IdleMillis: 100, ShutdownMillis: 100}
}
func TestDeviceConfigBoundariesAndSecretRedaction(t *testing.T) {
	for _, name := range []string{"AURIGA_AI_PG_DSN", "AURIGA_AI_MIGRATION_PG_DSN", "AURIGA_AI_MASTER_KEY_FILE"} {
		t.Setenv(name, "")
	}
	base, _ := json.Marshal(validDeviceConfig())
	for _, bad := range []string{`{"private-secret":1}`, `{"capacity":99999999999999999999999}`, string(base) + ` {}`, string(base) + strings.Repeat(" ", 1<<20), `{"d7SyntheticOnly":true,"listen":"secret-password"}`} {
		p := filepath.Join(t.TempDir(), "config.json")
		if e := os.WriteFile(p, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		_, e := loadDeviceConfig(p)
		if e == nil || strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), "999999") {
			t.Fatalf("unsafe validation: %v", e)
		}
	}
	for _, mutate := range []func(*deviceConfig){
		func(c *deviceConfig) { c.Listen = "localhost:0" }, func(c *deviceConfig) { c.Listen = "0.0.0.0:0" }, func(c *deviceConfig) { c.Listen = "127.0.0.1:65536" },
		func(c *deviceConfig) { c.D7SyntheticOnly = false }, func(c *deviceConfig) { c.Store.DSN = "postgres://alice@127.0.0.1/production" },
		func(c *deviceConfig) { c.Capacity = -1 }, func(c *deviceConfig) { c.HeaderMillis = 60001 }, func(c *deviceConfig) { c.MaxInFlight = 3 },
		func(c *deviceConfig) { c.Org = deviceRPCConfig{} }, func(c *deviceConfig) { c.IAM = deviceRPCConfig{} }, func(c *deviceConfig) { c.SyntheticTenant = "00000000-0000-0000-0000-000000000000" },
	} {
		c := validDeviceConfig()
		mutate(&c)
		if validateDeviceConfig(c) == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	p := filepath.Join(t.TempDir(), "valid.json")
	if e := os.WriteFile(p, base, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := loadDeviceConfig(p); e != nil {
		t.Fatal(e)
	}
	t.Setenv("AURIGA_AI_MASTER_KEY_FILE", "rotated.key")
	c, e := loadDeviceConfig(p)
	if e != nil || c.MasterKeyFile != "rotated.key" {
		t.Fatal("key override missing")
	}
}
func TestDeviceRuntimeMutualExclusion(t *testing.T) {
	for _, other := range []string{"--admin-config", "--team-config", "--network-config"} {
		var out, logs bytes.Buffer
		e := run([]string{"--device-config", "private-secret", other, "private-secret"}, &out, &logs)
		if e == nil || !strings.Contains(e.Error(), "choose one runtime") || strings.Contains(e.Error(), "private-secret") {
			t.Fatalf("mutual exclusion: %v", e)
		}
	}
}
