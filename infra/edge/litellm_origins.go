package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

func (c Config) validateLiteLLMExternalOrigins() error {
	seen := map[string]bool{}
	for admin, relay := range c.LiteLLMUI.ExternalOrigins {
		if err := (Config{LinkOrigins: []string{admin, relay}}).validateLinkOrigins(); err != nil {
			return err
		}
		a, _ := url.Parse(admin)
		u, _ := url.Parse(relay)
		if a.Scheme != "https" || u.Scheme != "https" || admin == relay || seen[relay] ||
			admin == c.Access.AdminURL() || relay == c.liteLLMUIURL() || relay == c.Access.AdminURL() || relay == c.Access.PortalURL() || relay == c.Access.Issuer() ||
			u.Hostname() == c.Access.InstanceControl().DnsAlias || !slices.Contains(c.LinkOrigins, admin) || !slices.Contains(c.LinkOrigins, relay) || len(c.LinkTrustedProxies) == 0 {
			return fmt.Errorf("LiteLLM external origins require distinct HTTPS admin/relay pairs and explicit link proxy trust")
		}
		if parent := c.Access.InstanceParentDomain(); parent != "" && (u.Hostname() == parent || strings.HasSuffix(u.Hostname(), "."+parent)) {
			return fmt.Errorf("LiteLLM external origin overlaps instance domain")
		}
		seen[relay] = true
	}
	for admin := range c.LiteLLMUI.ExternalOrigins {
		if seen[admin] {
			return fmt.Errorf("LiteLLM admin and relay external origins overlap")
		}
	}
	return nil
}

// accessHandler captures presentation origin before stripping forwarded headers.
// It still admits only the installation's canonical Host/listener and transport.
func (g *Gateway) liteLLMHandoffTarget(r *http.Request) string {
	presentation, _ := r.Context().Value(linkOriginKey{}).(string)
	if presentation == "" || presentation == g.Cfg.Access.AdminURL() {
		return g.Cfg.liteLLMUIURL()
	}
	return g.Cfg.LiteLLMUI.ExternalOrigins[presentation]
}

func (g *Gateway) liteLLMPresentationOrigin(r *http.Request) string {
	presentation, _ := r.Context().Value(linkOriginKey{}).(string)
	if presentation == "" || presentation == g.Cfg.liteLLMUIURL() {
		return g.Cfg.liteLLMUIURL()
	}
	for _, origin := range g.Cfg.LiteLLMUI.ExternalOrigins {
		if origin == presentation {
			return presentation
		}
	}
	return ""
}
