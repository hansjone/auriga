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

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"github.com/jackc/pgx/v5"
)

type deviceConfig struct {
	Listen          string         `json:"listen"`
	MasterKeyFile   string         `json:"masterKeyFile"`
	Store           store.Config   `json:"store"`
	IAM             team.RPCConfig `json:"iam"`
	Org             team.RPCConfig `json:"org"`
	SyntheticTenant string         `json:"syntheticTenant"`
	D7SyntheticOnly bool           `json:"d7SyntheticOnly"`
	Capacity        int            `json:"capacity"`
	MaxConnections  int            `json:"maxConnections"`
	MaxInFlight     int            `json:"maxInFlight"`
	HeaderMillis    int            `json:"headerMillis"`
	ReadMillis      int            `json:"readMillis"`
	WriteMillis     int            `json:"writeMillis"`
	IdleMillis      int            `json:"idleMillis"`
	ShutdownMillis  int            `json:"shutdownMillis"`
}

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
	if !c.D7SyntheticOnly || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(c.SyntheticTenant) || c.MasterKeyFile == "" {
		return deviceError()
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
	for _, rpc := range []team.RPCConfig{c.IAM, c.Org} {
		if rpc.Target == "" || rpc.ServiceTokenFile == "" {
			return deviceError()
		}
	}
	return nil
}
