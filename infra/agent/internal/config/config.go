// Package config loads auriga-agent settings (file + AURIGA_AGENT_* env).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Config drives auriga-agent. Defaults are safe: no public listen.
type Config struct {
	// Server is the platform base URL used by `join` (https://host:port).
	Server string `json:"server"`
	// DataRoot is <data>/ in the node-agent layout (agent/, instances/, …).
	DataRoot string `json:"dataRoot"`
	// Drivers lists enabled runtime kinds (A1: fake is always available for tests).
	Drivers []string `json:"drivers"`
	// MaxConcurrentPrepare limits parallel prepare/start work.
	MaxConcurrentPrepare int `json:"maxConcurrentPrepare"`
	// ControlEndpoint overrides the endpoint from join (tests / recovery).
	ControlEndpoint string `json:"controlEndpoint"`
	// AgentVersion reported in Hello / Join.
	AgentVersion string `json:"agentVersion"`
	// AllowInsecure permits grpc:// and http:// endpoints (local dev / tests only).
	AllowInsecure bool `json:"allowInsecure"`
	// IngressAdvertiseHost is the externally reachable DNS name/IP (no port).
	IngressAdvertiseHost string `json:"ingressAdvertiseHost"`
}

func defaultDataRoot() string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "auriga")
	}
	return "/var/lib/auriga"
}

// Default returns safe defaults (loopback-oriented; agent does not listen).
func Default() Config {
	return Config{
		DataRoot:             defaultDataRoot(),
		Drivers:              []string{"fake"},
		MaxConcurrentPrepare: 4,
		AgentVersion:         "0.0.0-dev",
	}
}

// Load reads optional JSON config then applies environment overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	set := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set("AURIGA_AGENT_SERVER", &cfg.Server)
	set("AURIGA_AGENT_DATA_ROOT", &cfg.DataRoot)
	set("AURIGA_AGENT_CONTROL_ENDPOINT", &cfg.ControlEndpoint)
	set("AURIGA_AGENT_VERSION", &cfg.AgentVersion)
	set("AURIGA_AGENT_INGRESS_ADVERTISE_HOST", &cfg.IngressAdvertiseHost)
	if v := os.Getenv("AURIGA_AGENT_DRIVERS"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		cfg.Drivers = out
	}
	if v := os.Getenv("AURIGA_AGENT_MAX_CONCURRENT_PREPARE"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			cfg.MaxConcurrentPrepare = n
		}
	}
	if v := os.Getenv("AURIGA_AGENT_ALLOW_INSECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			cfg.AllowInsecure = b
		}
	}
}

// Validate checks required fields for a running agent.
func (c Config) Validate() error {
	if c.DataRoot == "" {
		return fmt.Errorf("dataRoot is required")
	}
	if c.MaxConcurrentPrepare < 1 {
		return fmt.Errorf("maxConcurrentPrepare must be >= 1")
	}
	return nil
}

// AgentDir is <data>/agent.
func (c Config) AgentDir() string { return filepath.Join(c.DataRoot, "agent") }

// InstancesDir is <data>/instances.
func (c Config) InstancesDir() string { return filepath.Join(c.DataRoot, "instances") }

// ArtifactsDir is <data>/artifacts.
func (c Config) ArtifactsDir() string { return filepath.Join(c.DataRoot, "artifacts") }

// BackupsDir is <data>/backups.
func (c Config) BackupsDir() string { return filepath.Join(c.DataRoot, "backups") }
