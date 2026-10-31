package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// ManagedIngressConfig is an installation-owned TLS endpoint on the already
// isolated instance network. It does not serve browser sessions or admin APIs.
type ManagedIngressConfig struct {
	Origin   string `json:"origin"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

func (c Config) managedEntry() (accessEntry, error) {
	m := c.ManagedIngress
	u, err := url.Parse(m.Origin)
	if err != nil || !c.Access.Valid() || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(m.Origin, "#") || u.String() != m.Origin || !slices.Contains(c.LinkOrigins, m.Origin) || m.CertFile == "" || m.KeyFile == "" {
		return accessEntry{}, fmt.Errorf("managed ingress requires an explicitly approved canonical HTTPS origin and TLS material")
	}
	host, port, err := parseAuthority(u.Host, "https")
	if err != nil || host != c.Access.InstanceControl().DnsAlias || originAuthorityFromEntry(host, port) != u.Host {
		return accessEntry{}, fmt.Errorf("managed ingress must use the existing instance control DNS alias")
	}
	ls := c.Access.Listeners()
	if port == int(ls.Portal.Port) || port == int(ls.Admin.Port) || port == int(ls.InstanceControl.Port) || c.LiteLLMUI != nil && port == int(c.LiteLLMUI.Listener.Port) {
		return accessEntry{}, fmt.Errorf("managed ingress needs a separate port")
	}
	policy, err := c.parseControlIngress()
	if err != nil || policy == nil {
		return accessEntry{}, fmt.Errorf("managed ingress requires private interface ingress restrictions")
	}
	return accessEntry{role: "portal", scheme: "https", host: host, port: port}, nil
}

func originAuthorityFromEntry(host string, port int) string {
	if port == 443 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func managedPathAllowed(r *http.Request) bool {
	path, ok := sanitizeRequestPath(r)
	if !ok || path != r.URL.Path {
		return false
	}
	if r.Method == http.MethodGet {
		switch path {
		case "/oauth2/jwks", "/.well-known/openid-configuration", "/api/collab/v1/network/keys", "/api/collab/v1/network/keys/announcement", "/api/iam/v1/network/systems", "/ai/v1/models":
			return true
		case "/net/v1/ws":
			return isUpgradeRequest(r)
		}
	}
	if r.Method == http.MethodPost {
		switch path {
		case "/oauth2/token", "/api/iam/v1/network/register", "/api/iam/v1/network/tickets", "/api/ai/v1/network/submit", "/ai/v1/chat/completions", "/ai/v1/responses":
			return true
		}
	}
	return false
}
