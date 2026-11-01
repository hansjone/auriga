package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// Config drives auriga-edge. Values may come from a JSON file and/or environment.
type Config struct {
	AccessConfigFile       string                        `json:"accessConfigFile"`
	Access                 deploymentconfig.Config       `json:"-"`
	ProxyTLS               ProxyTLSConfig                `json:"proxyTLS"`
	InstanceControlIngress *InstanceControlIngressConfig `json:"instanceControlIngress,omitempty"`
	IAMRPC                 IAMRPCConfig                  `json:"iamRpc"`
	IAMHop                 IAMHopConfig                  `json:"iamHop"`
	LiteLLMOrigin          string                        `json:"litellmOrigin"`
	LiteLLMUI              *LiteLLMUIConfig              `json:"litellmUI,omitempty"`
	DeviceHop              IAMHopConfig                  `json:"deviceHop"`
	CollabHop              IAMHopConfig                  `json:"collabHop"`
	NetworkSubmitHop       IAMHopConfig                  `json:"networkSubmitHop"`
	Listen                 string                        `json:"listen"`
	PortalHost             string                        `json:"portalHost"`
	AdminHost              string                        `json:"adminHost"`
	InstanceParentDomain   string                        `json:"instanceParentDomain"`
	SecureCookies          bool                          `json:"secureCookies"`
	TrustedProxies         []string                      `json:"trustedProxies"`
	LinkOrigins            []string                      `json:"linkOrigins,omitempty"`
	LinkTrustedProxies     []string                      `json:"linkTrustedProxies,omitempty"`
	Upstreams              map[string]string             `json:"upstreams"`
	Static                 StaticConfig                  `json:"static"`
	TLS                    TLSConfig                     `json:"tls"`
	RateLimit              RateLimitConfig               `json:"rateLimit"`
	IdentityKeysFile       string                        `json:"identityKeysFile"`
	SessionStore           string                        `json:"sessionStore"` // memory | nats
	NATSURL                string                        `json:"natsUrl"`
	NATSCredsFile          string                        `json:"natsCredsFile"`
	RoutesNATSCredsFile    string                        `json:"routesNatsCredsFile"`
	AllowLocalIngress      bool                          `json:"allowLocalIngress"`
	NodeTLS                NodeTLSConfig                 `json:"nodeTLS"`
	PublicPathPrefixes     []string                      `json:"publicPathPrefixes"`
	NetworkWSS             NetworkWSSConfig              `json:"networkWss"`
	ManagedIngress         ManagedIngressConfig          `json:"managedIngress"`
}

type StaticConfig struct {
	PortalRoot     string `json:"portalRoot"`
	AdminRoot      string `json:"adminRoot"`
	PanelRoot      string `json:"panelRoot"`
	GzipCacheBytes int    `json:"gzipCacheBytes,omitempty"`
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

func loadConfigWithAccess(path, accessPath string) (Config, error) {
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
	if raw := os.Getenv("AURIGA_EDGE_STATIC_GZIP_CACHE_BYTES"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return Config{}, fmt.Errorf("invalid static gzip cache size")
		}
		cfg.Static.GzipCacheBytes = limit
	}
	if raw := os.Getenv("AURIGA_EDGE_LITELLM_UI_EXTERNAL_ORIGINS"); raw != "" {
		if cfg.LiteLLMUI == nil || json.Unmarshal([]byte(raw), &cfg.LiteLLMUI.ExternalOrigins) != nil {
			return Config{}, fmt.Errorf("invalid LiteLLM UI external origins JSON")
		}
	}
	if file := os.Getenv("AURIGA_EDGE_LITELLM_UI_PASSWORD_FILE"); file != "" {
		if cfg.LiteLLMUI == nil {
			return Config{}, fmt.Errorf("LiteLLM UI login requires UI configuration")
		}
		if cfg.LiteLLMUI.Login == nil {
			cfg.LiteLLMUI.Login = &LiteLLMUILogin{}
		}
		cfg.LiteLLMUI.Login.PasswordFile = file
	}
	if file := os.Getenv("AURIGA_EDGE_LITELLM_KEY_INFO_FILE"); file != "" {
		if cfg.LiteLLMUI == nil || cfg.LiteLLMUI.Login == nil {
			return Config{}, fmt.Errorf("LiteLLM key reader requires UI configuration")
		}
		cfg.LiteLLMUI.Login.KeyInfoKeyFile = file
	}
	if accessPath != "" {
		cfg.AccessConfigFile = accessPath
	}
	if cfg.AccessConfigFile != "" {
		raw, err := os.ReadFile(cfg.AccessConfigFile)
		if err != nil {
			return Config{}, fmt.Errorf("read access config: %w", err)
		}
		cfg.Access, err = deploymentconfig.Load(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.PortalHost = originAuthority(cfg.Access.PortalOrigin())
		cfg.AdminHost = originAuthority(cfg.Access.AdminOrigin())
		cfg.InstanceParentDomain = cfg.Access.InstanceParentDomain()
		cfg.SecureCookies = cfg.Access.PortalOrigin().Scheme == "https"
		cfg.TrustedProxies = nil
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if value := os.Getenv("AURIGA_EDGE_LINKS_ALLOWED_ORIGINS"); value != "" {
		cfg.LinkOrigins = splitCSV(value)
	}
	if value := os.Getenv("AURIGA_EDGE_LINKS_TRUSTED_PROXIES"); value != "" {
		cfg.LinkTrustedProxies = splitCSV(value)
	}
	set := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set("AURIGA_EDGE_MANAGED_ORIGIN", &cfg.ManagedIngress.Origin)
	set("AURIGA_EDGE_MANAGED_CERT_FILE", &cfg.ManagedIngress.CertFile)
	set("AURIGA_EDGE_MANAGED_KEY_FILE", &cfg.ManagedIngress.KeyFile)
	set("AURIGA_EDGE_LISTEN", &cfg.Listen)
	set("AURIGA_EDGE_ACCESS_CONFIG", &cfg.AccessConfigFile)
	set("AURIGA_EDGE_NODE_CERT_FILE", &cfg.NodeTLS.CertFile)
	set("AURIGA_EDGE_NODE_KEY_FILE", &cfg.NodeTLS.KeyFile)
	set("AURIGA_EDGE_NODE_CA_FILE", &cfg.NodeTLS.CAFile)
	set("AURIGA_EDGE_ROUTES_NATS_CREDS_FILE", &cfg.RoutesNATSCredsFile)
	if v := os.Getenv("AURIGA_EDGE_ALLOW_LOCAL_INGRESS"); v != "" {
		cfg.AllowLocalIngress = v == "1" || strings.EqualFold(v, "true")
	}
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
	for key, env := range map[string]string{"resource": "AURIGA_EDGE_UPSTREAM_RESOURCE", "ai-device": "AURIGA_EDGE_UPSTREAM_AI_DEVICE", "network-submit": "AURIGA_EDGE_UPSTREAM_NETWORK_SUBMIT"} {
		if v := os.Getenv(env); v != "" {
			if cfg.Upstreams == nil {
				cfg.Upstreams = map[string]string{}
			}
			cfg.Upstreams[key] = v
		}
	}
	set("AURIGA_EDGE_NETWORK_SUBMIT_CERT_FILE", &cfg.NetworkSubmitHop.CertFile)
	set("AURIGA_EDGE_NETWORK_SUBMIT_KEY_FILE", &cfg.NetworkSubmitHop.KeyFile)
	set("AURIGA_EDGE_NETWORK_SUBMIT_CA_FILE", &cfg.NetworkSubmitHop.CAFile)
	set("AURIGA_EDGE_NETWORK_SUBMIT_SERVER_NAME", &cfg.NetworkSubmitHop.ServerName)
	if v := os.Getenv("AURIGA_EDGE_UPSTREAM_COLLAB"); v != "" {
		if cfg.Upstreams == nil {
			cfg.Upstreams = map[string]string{}
		}
		cfg.Upstreams["collab"] = v
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
	if v := os.Getenv("AURIGA_EDGE_NETWORK_WSS_ENABLED"); v != "" {
		cfg.NetworkWSS.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	set("AURIGA_EDGE_NETWORK_WSS_UPSTREAM", &cfg.NetworkWSS.UpstreamURL)
	set("AURIGA_EDGE_NETWORK_WSS_CA_FILE", &cfg.NetworkWSS.CAFile)
	set("AURIGA_EDGE_NETWORK_WSS_SERVER_NAME", &cfg.NetworkWSS.ServerName)
	set("AURIGA_EDGE_NETWORK_WSS_PATH", &cfg.NetworkWSS.Path)
	if v := os.Getenv("AURIGA_EDGE_NETWORK_WSS_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.NetworkWSS.MaxConcurrent = n
		}
	}
}

func (c Config) validate() error {
	if err := c.validateLinkOrigins(); err != nil {
		return err
	}
	if _, err := c.parseResourceUpstream(); err != nil {
		return err
	}
	if _, err := c.parseDeviceUpstream(); err != nil {
		return err
	}
	if _, err := c.parseNetworkSubmitUpstream(); err != nil {
		return err
	}
	if _, err := c.parseCollabUpstream(); err != nil {
		return err
	}
	if _, err := c.parseControlIngress(); err != nil {
		return err
	}
	if c.NetworkWSS.Enabled {
		if c.NetworkWSS.UpstreamURL == "" {
			return fmt.Errorf("networkWss.upstreamUrl is required when enabled")
		}
	}
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
	if parent := strings.ToLower(strings.TrimSpace(c.InstanceParentDomain)); parent != "" && !c.Access.Valid() {
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

// Network submit lives on the callout runtime's separate HTTPS listener. Never
// borrow the model-device origin, system roots, proxy or IAM hop credentials.
func (c Config) parseNetworkSubmitUpstream() (*url.URL, error) {
	raw := c.Upstreams["network-submit"]
	if raw == "" {
		if c.NetworkSubmitHop != (IAMHopConfig{}) {
			return nil, fmt.Errorf("networkSubmitHop requires upstreams.network-submit")
		}
		return nil, nil
	}
	if !c.Access.Valid() {
		return nil, fmt.Errorf("network-submit requires validated Access configuration")
	}
	u, err := parseResourceDeviceOrigin(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid network-submit upstream origin")
	}
	hop := c.NetworkSubmitHop
	if u.Scheme != "https" || hop.AllowLoopbackHTTP {
		return nil, fmt.Errorf("network-submit requires HTTPS without plaintext fallback")
	}
	name, _, nameErr := parseAuthority(hop.ServerName, "https")
	if nameErr != nil || !strings.EqualFold(name, hop.ServerName) || net.ParseIP(name) != nil || hop.CertFile == "" || hop.KeyFile == "" || hop.CAFile == "" {
		return nil, fmt.Errorf("networkSubmitHop requires fixed DNS serverName, certFile, keyFile and caFile")
	}
	return u, nil
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
