package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config drives auriga-edge. Values may come from a JSON file and/or environment.
type Config struct {
	Listen               string            `json:"listen"`
	PortalHost           string            `json:"portalHost"`
	AdminHost            string            `json:"adminHost"`
	InstanceParentDomain string            `json:"instanceParentDomain"`
	SecureCookies        bool              `json:"secureCookies"`
	TrustedProxies       []string          `json:"trustedProxies"`
	Upstreams            map[string]string `json:"upstreams"`
	Static               StaticConfig      `json:"static"`
	TLS                  TLSConfig         `json:"tls"`
	RateLimit            RateLimitConfig   `json:"rateLimit"`
	IdentityKeysFile     string            `json:"identityKeysFile"`
	SessionStore         string            `json:"sessionStore"` // memory | nats
	NATSURL              string            `json:"natsUrl"`
	NATSCredsFile        string            `json:"natsCredsFile"`
	NodeTLS              NodeTLSConfig     `json:"nodeTLS"`
	PublicPathPrefixes   []string          `json:"publicPathPrefixes"`
}

type StaticConfig struct {
	PortalRoot string `json:"portalRoot"`
	AdminRoot  string `json:"adminRoot"`
	PanelRoot  string `json:"panelRoot"`
}

type TLSConfig struct {
	Mode     string   `json:"mode"` // off | certmagic | files
	CertFile string   `json:"certFile"`
	KeyFile  string   `json:"keyFile"`
	Domains  []string `json:"domains"`
	Email    string   `json:"email"`
	Storage  string   `json:"storage"`
}

type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requestsPerSecond"`
	Burst             int     `json:"burst"`
	PathPrefix        string  `json:"pathPrefix"` // optional; empty = all
}

func defaultConfig() Config {
	return Config{
		Listen:        "127.0.0.1:8080",
		SecureCookies: true,
		Upstreams:     map[string]string{},
		TLS:           TLSConfig{Mode: "off"},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 50,
			Burst:             100,
		},
		SessionStore: "memory",
		PublicPathPrefixes: []string{
			"/oauth2/",
			"/.well-known/",
			"/userinfo",
			"/api/iam/v1/login/",
		},
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
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
	if err := cfg.validate(); err != nil {
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
	set("AURIGA_EDGE_LISTEN", &cfg.Listen)
	set("AURIGA_EDGE_NODE_CERT_FILE", &cfg.NodeTLS.CertFile)
	set("AURIGA_EDGE_NODE_KEY_FILE", &cfg.NodeTLS.KeyFile)
	set("AURIGA_EDGE_NODE_CA_FILE", &cfg.NodeTLS.CAFile)
	set("AURIGA_EDGE_PORTAL_HOST", &cfg.PortalHost)
	set("AURIGA_EDGE_ADMIN_HOST", &cfg.AdminHost)
	set("AURIGA_EDGE_INSTANCE_PARENT_DOMAIN", &cfg.InstanceParentDomain)
	set("AURIGA_EDGE_IDENTITY_KEYS_FILE", &cfg.IdentityKeysFile)
	set("AURIGA_EDGE_SESSION_STORE", &cfg.SessionStore)
	set("AURIGA_EDGE_NATS_URL", &cfg.NATSURL)
	if cfg.NATSURL == "" {
		set("AURIGA_NATS_URL", &cfg.NATSURL)
	}
	set("AURIGA_EDGE_NATS_CREDS_FILE", &cfg.NATSCredsFile)
	if cfg.NATSCredsFile == "" {
		set("AURIGA_NATS_CREDS_FILE", &cfg.NATSCredsFile)
	}
	if v := os.Getenv("AURIGA_EDGE_SECURE_COOKIES"); v != "" {
		cfg.SecureCookies = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("AURIGA_EDGE_TRUSTED_PROXIES"); v != "" {
		cfg.TrustedProxies = splitCSV(v)
	}
	if v := os.Getenv("AURIGA_EDGE_TLS_MODE"); v != "" {
		cfg.TLS.Mode = v
	}
	if v := os.Getenv("AURIGA_EDGE_TLS_CERT_FILE"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("AURIGA_EDGE_TLS_KEY_FILE"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("AURIGA_EDGE_UPSTREAM_IAM"); v != "" {
		if cfg.Upstreams == nil {
			cfg.Upstreams = map[string]string{}
		}
		cfg.Upstreams["iam"] = v
	}
	if v := os.Getenv("AURIGA_EDGE_UPSTREAM_ORG"); v != "" {
		if cfg.Upstreams == nil {
			cfg.Upstreams = map[string]string{}
		}
		cfg.Upstreams["org"] = v
	}
	if v := os.Getenv("AURIGA_EDGE_UPSTREAM_WORKSPACE"); v != "" {
		if cfg.Upstreams == nil {
			cfg.Upstreams = map[string]string{}
		}
		cfg.Upstreams["workspace"] = v
	}
	if v := os.Getenv("AURIGA_EDGE_STATIC_PORTAL"); v != "" {
		cfg.Static.PortalRoot = v
	}
	if v := os.Getenv("AURIGA_EDGE_STATIC_ADMIN"); v != "" {
		cfg.Static.AdminRoot = v
	}
	if v := os.Getenv("AURIGA_EDGE_RATE_RPS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RateLimit.RequestsPerSecond = f
		}
	}
	if v := os.Getenv("AURIGA_EDGE_RATE_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RateLimit.Burst = n
		}
	}
}

func (c Config) validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	if c.PortalHost == "" {
		return fmt.Errorf("portalHost is required")
	}
	if c.AdminHost == "" {
		return fmt.Errorf("adminHost is required")
	}
	if strings.EqualFold(c.PortalHost, c.AdminHost) {
		return fmt.Errorf("portalHost and adminHost must differ")
	}
	for _, name := range []string{"iam", "org", "workspace"} {
		if u := c.Upstreams[name]; u == "" {
			return fmt.Errorf("upstreams.%s is required", name)
		}
	}
	switch strings.ToLower(c.TLS.Mode) {
	case "off", "":
	case "files":
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return fmt.Errorf("tls.certFile and tls.keyFile required for mode=files")
		}
	case "certmagic":
		if len(c.TLS.Domains) == 0 {
			return fmt.Errorf("tls.domains required for mode=certmagic")
		}
	default:
		return fmt.Errorf("unknown tls.mode %q", c.TLS.Mode)
	}
	switch strings.ToLower(c.SessionStore) {
	case "memory", "":
	case "nats":
		if c.NATSURL == "" {
			return fmt.Errorf("natsUrl required when sessionStore=nats")
		}
	default:
		return fmt.Errorf("unknown sessionStore %q", c.SessionStore)
	}
	if c.IdentityKeysFile == "" {
		return fmt.Errorf("identityKeysFile is required")
	}
	if parent := strings.ToLower(strings.TrimSpace(c.InstanceParentDomain)); parent != "" {
		ph := strings.ToLower(c.PortalHost)
		ah := strings.ToLower(c.AdminHost)
		suffix := "." + parent
		if ph == parent || strings.HasSuffix(ph, suffix) {
			return fmt.Errorf("instanceParentDomain %q would capture portalHost %q as an instance host", c.InstanceParentDomain, c.PortalHost)
		}
		if ah == parent || strings.HasSuffix(ah, suffix) {
			return fmt.Errorf("instanceParentDomain %q would capture adminHost %q as an instance host", c.InstanceParentDomain, c.AdminHost)
		}
		// Nonce Domain cookie must cover the portal host or browsers drop it.
		cookieDomain := openNonceCookieDomain(parent)
		if cookieDomain != "" {
			if ph != cookieDomain && !strings.HasSuffix(ph, "."+cookieDomain) {
				return fmt.Errorf("portalHost %q is not under open-nonce cookie Domain %q derived from instanceParentDomain %q", c.PortalHost, cookieDomain, c.InstanceParentDomain)
			}
		}
	}
	for _, p := range c.TrustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ip := net.ParseIP(p); ip != nil {
			continue
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy %q", p)
		}
		ones, bits := n.Mask.Size()
		if ones == 0 && (bits == 32 || bits == 128) {
			return fmt.Errorf("trustedProxies must not include world-open CIDR %q", p)
		}
	}
	if c.RateLimit.RequestsPerSecond <= 0 {
		return fmt.Errorf("rateLimit.requestsPerSecond must be > 0 (got %v); use a large value if you intend to effectively disable limiting", c.RateLimit.RequestsPerSecond)
	}
	if c.RateLimit.Burst <= 0 {
		return fmt.Errorf("rateLimit.burst must be > 0")
	}
	return nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
