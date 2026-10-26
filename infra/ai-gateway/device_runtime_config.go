package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/upstream"

	"github.com/jackc/pgx/v5"
)

type deviceConfig struct {
	Listen          string                `json:"listen"`
	MasterKeyFile   string                `json:"masterKeyFile"`
	Store           store.Config          `json:"store"`
	IAM             deviceRPCConfig       `json:"iam"`
	Org             deviceRPCConfig       `json:"org"`
	SyntheticTenant string                `json:"syntheticTenant"`
	D7SyntheticOnly bool                  `json:"d7SyntheticOnly"`
	Upstream        *deviceUpstreamConfig `json:"upstream,omitempty"`
	Capacity        int                   `json:"capacity"`
	MaxConnections  int                   `json:"maxConnections"`
	MaxInFlight     int                   `json:"maxInFlight"`
	HeaderMillis    int                   `json:"headerMillis"`
	ReadMillis      int                   `json:"readMillis"`
	WriteMillis     int                   `json:"writeMillis"`
	IdleMillis      int                   `json:"idleMillis"`
	ShutdownMillis  int                   `json:"shutdownMillis"`
}

// Wire configuration stays local; RPC validation/authentication still uses team.DialRPC.
type deviceRPCConfig struct {
	Target           string `json:"target"`
	ServiceTokenFile string `json:"serviceTokenFile"`
	CertFile         string `json:"certFile"`
	KeyFile          string `json:"keyFile"`
	CAFile           string `json:"caFile"`
	ServerName       string `json:"serverName"`
}

var deviceTenantPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func deviceError() error         { return errors.New("invalid D7 device configuration") }
func millis(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func loadDeviceConfig(path string) (deviceConfig, error) {
	c := deviceConfig{Listen: "127.0.0.1:0", Capacity: 64, MaxConnections: 64, MaxInFlight: 32, HeaderMillis: 5000, ReadMillis: 15000, WriteMillis: 5000, IdleMillis: 30000, ShutdownMillis: 10000}
	f, e := os.Open(path)
	if e != nil {
		return c, deviceError()
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return c, deviceError()
	}
	// Decoder errors can echo caller-selected keys or numeric secrets.
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return deviceConfig{}, deviceError()
	}
	if v := os.Getenv("AURIGA_AI_PG_DSN"); v != "" {
		c.Store.DSN = v
	}
	if v := os.Getenv("AURIGA_AI_MIGRATION_PG_DSN"); v != "" {
		c.Store.MigrationDSN = v
	}
	if v := os.Getenv("AURIGA_AI_MASTER_KEY_FILE"); v != "" {
		c.MasterKeyFile = v
	}
	return c, validateDeviceConfig(c)
}
func validateDeviceConfig(c deviceConfig) error {
	host, port, e := net.SplitHostPort(c.Listen)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 0 || n > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return deviceError()
	}
	if c.MasterKeyFile == "" {
		return deviceError()
	}
	if c.Upstream == nil {
		if !c.D7SyntheticOnly || !deviceTenantPattern.MatchString(c.SyntheticTenant) {
			return deviceError()
		}
	} else {
		if c.D7SyntheticOnly || c.SyntheticTenant != "" || len(c.Upstream.Models) == 0 {
			return deviceError()
		}
		if _, err := upstream.New(upstream.Config{AllowedHosts: c.Upstream.AllowedHosts, MaxResponseBytes: 1 << 20, MaxEventBytes: modelprotocol.EventLimit}); err != nil {
			return deviceError()
		}
		seen := map[string]bool{}
		for _, m := range c.Upstream.Models {
			if m.MaxOutputTokens > upstreamMaxOutputTokens {
				return deviceError()
			}
			key := m.Tenant + "/" + m.Model
			if !deviceTenantPattern.MatchString(m.Tenant) || !deviceTenantPattern.MatchString(m.ProviderID) || m.Model == "" || len(m.Model) > 128 || m.UpstreamModel == "" || len(m.UpstreamModel) > 256 || !m.ContextLimitEnforced || !m.TrustUsage || m.ContextWindow < 2 || m.ContextWindow > 9007199254740991 || m.MaxOutputTokens < 1 || m.MaxOutputTokens >= m.ContextWindow || seen[key] {
				return deviceError()
			}
			seen[key] = true
		}
	}
	for _, dsn := range []string{c.Store.DSN, c.Store.MigrationDSN} {
		p, e := pgx.ParseConfig(dsn)
		if dsn == "" || e != nil || p.Database != "auriga_d7" {
			return deviceError()
		}
	}
	if c.Store.MaxConnections < 0 || c.Store.MaxConnections > 64 || c.Capacity < 1 || c.Capacity > 4096 || c.MaxConnections < 1 || c.MaxConnections > 1024 || c.MaxInFlight < 1 || c.MaxInFlight > c.MaxConnections {
		return deviceError()
	}
	for _, v := range []int{c.HeaderMillis, c.ReadMillis, c.WriteMillis, c.IdleMillis, c.ShutdownMillis} {
		if v < 10 || v > 60000 {
			return deviceError()
		}
	}
	for _, rpc := range []deviceRPCConfig{c.IAM, c.Org} {
		if rpc.Target == "" || rpc.ServiceTokenFile == "" {
			return deviceError()
		}
	}
	return nil
}
