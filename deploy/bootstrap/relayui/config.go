// Package relayui binds LiteLLM's browser origin independently of Auriga's API origins.
package relayui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"golang.org/x/net/idna"
)

type Config struct {
	Origin   deployment.Origin   `json:"origin"`
	Listener deployment.Listener `json:"listener"`
}

const DefaultPort = 8086

func (c Config) URL() string {
	host := c.Origin.Host
	if !(c.Origin.Scheme == "https" && c.Origin.Port == 443 || c.Origin.Scheme == "http" && c.Origin.Port == 80) {
		host = net.JoinHostPort(host, strconv.Itoa(int(c.Origin.Port)))
	}
	return c.Origin.Scheme + "://" + host
}

func (c Config) Digest() string {
	raw, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func Resolve(access deploymentconfig.Config, raw string) (Config, error) {
	invalid := fmt.Errorf("invalid independent LiteLLM UI origin")
	if !access.Valid() {
		return Config{}, invalid
	}
	admin := access.AdminOrigin()
	c := Config{Origin: deployment.Origin{Scheme: admin.Scheme, Host: admin.Host, Port: DefaultPort}, Listener: access.Listeners().Admin}
	c.Listener.Port = DefaultPort
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.Scheme != admin.Scheme || u.Host == "" {
			return Config{}, invalid
		}
		host := u.Hostname()
		if host == "" || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "[]:@/\\ \t\r\n") {
			return Config{}, invalid
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if !ip.Is4() || ip.String() != host {
				return Config{}, invalid
			}
		} else {
			profile := idna.New(idna.ValidateForRegistration(), idna.VerifyDNSLength(true), idna.Transitional(false))
			ascii, err := profile.ToASCII(host)
			if err != nil || ascii != host || regexp.MustCompile(`^(?:[0-9]+|0x[0-9a-f]+)(?:\.(?:[0-9]+|0x[0-9a-f]+))*$`).MatchString(host) {
				return Config{}, invalid
			}
		}
		port := 443
		if u.Scheme == "http" {
			port = 80
		}
		if u.Port() != "" {
			port, err = strconv.Atoi(u.Port())
			if err != nil {
				return Config{}, invalid
			}
		}
		if port < 1 || port > 65535 || strings.HasSuffix(u.Host, ":") {
			return Config{}, invalid
		}
		c.Origin = deployment.Origin{Scheme: u.Scheme, Host: host, Port: int32(port)}
		if access.IngressKind() == "direct" {
			c.Listener.Port = int32(port)
		}
	}
	if c.URL() == access.AdminURL() || c.URL() == access.PortalURL() || c.URL() == access.Issuer() || c.Origin.Host == access.InstanceControl().DnsAlias {
		return Config{}, invalid
	}
	if access.Mode() == "ip" && c.Origin.Host != admin.Host {
		return Config{}, invalid
	}
	if parent := access.InstanceParentDomain(); parent != "" && (c.Origin.Host == parent || strings.HasSuffix(c.Origin.Host, "."+parent)) {
		return Config{}, invalid
	}
	ls := access.Listeners()
	for _, other := range []deployment.Listener{ls.Portal, ls.Admin, ls.InstanceControl} {
		if c.Listener.Port == other.Port {
			return Config{}, invalid
		}
	}
	return c, nil
}
