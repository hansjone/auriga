// Package deploymentconfig validates trusted installation input before any
// listener is opened. It does not authenticate proxy certificates, tenant
// existence, or Docker network membership; those are assembly-time obligations.
package deploymentconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/net/idna"
)

// ValidationError describes startup failure, with a JSON Pointer and the
// existing common error code. It never includes configuration field values.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string  { return e.Code() + " " + e.Field + ": " + e.Message }
func (e *ValidationError) Code() string   { return "common.validation_failed" }
func invalid(field, message string) error { return &ValidationError{Field: field, Message: message} }

// Config contains only validated state. Generated models and their mutable
// union pointers stay private. Accessors return values or copies.
// The zero value is invalid; consumers must obtain a Config through Load.
type Config struct {
	access deployment.Access
	peers  []netip.Prefix
	valid  bool
}

func (c Config) Valid() bool                                       { return c.valid }
func (c Config) TenantID() string                                  { return c.access.TenantId }
func (c Config) Mode() string                                      { return c.access.Mode }
func (c Config) PortalOrigin() deployment.Origin                   { return c.access.PortalOrigin }
func (c Config) AdminOrigin() deployment.Origin                    { return c.access.AdminOrigin }
func (c Config) PortalURL() string                                 { return serializeOrigin(c.access.PortalOrigin) }
func (c Config) AdminURL() string                                  { return serializeOrigin(c.access.AdminOrigin) }
func (c Config) Issuer() string                                    { return serializeOrigin(c.access.IssuerOrigin) }
func (c Config) Listeners() deployment.AccessListeners             { return c.access.Listeners }
func (c Config) InstanceControl() deployment.AccessInstanceControl { return c.access.InstanceControl }
func (c Config) ProxyPeerCIDRs() []netip.Prefix                    { return append([]netip.Prefix(nil), c.peers...) }
func (c Config) IngressKind() string {
	if c.access.Ingress.AccessIngressTrustedProxy != nil {
		return "trusted_proxy"
	}
	if c.valid {
		return "direct"
	}
	return ""
}
func (c Config) InstanceParentDomain() string {
	if branch := c.access.Instances.AccessInstancesSubdomain; branch != nil {
		return branch.ParentDomain
	}
	return ""
}

// InstanceSubdomain returns a value copy of the validated per-instance browser
// origin settings. Path mode and the invalid zero Config have no subdomain.
func (c Config) InstanceSubdomain() (deployment.AccessInstancesSubdomain, bool) {
	if c.valid && c.access.Instances.AccessInstancesSubdomain != nil {
		return *c.access.Instances.AccessInstancesSubdomain, true
	}
	return deployment.AccessInstancesSubdomain{}, false
}

func serializeOrigin(o deployment.Origin) string {
	if o.Scheme == "http" && o.Port == 80 || o.Scheme == "https" && o.Port == 443 {
		return o.Scheme + "://" + o.Host
	}
	return o.Scheme + "://" + net.JoinHostPort(o.Host, strconv.Itoa(int(o.Port)))
}

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

// The schema is compiled once, entirely from the generated original source.
// Neither input nor the schema can trigger filesystem/network reference loads.
var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(deployment.AccessSchemaJSON()))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(denyLoader{})
	const location = "urn:auriga:deployment:access:v1"
	if err := c.AddResource(location, doc); err != nil {
		return nil, err
	}
	return c.Compile(location)
})

// Load performs strict JSON decoding, full Draft 2020-12 structure validation,
// generated model decoding, then startup semantics. No services are started.
// Input is bounded to 1 MiB and 64 nested containers.
func Load(data []byte) (Config, error) {
	if len(data) > 1<<20 || !utf8.Valid(data) {
		return Config{}, invalid("", "invalid UTF-8 or configuration exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := readValue(d, "", 0)
	if err != nil {
		return Config{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Config{}, invalid("", "trailing JSON content")
	}
	schema, err := compiledSchema()
	if err != nil {
		return Config{}, invalid("", "embedded deployment schema cannot compile")
	}
	if err := schema.Validate(value); err != nil {
		field := ""
		var failure *jsonschema.ValidationError
		if errors.As(err, &failure) {
			for len(failure.Causes) > 0 {
				failure = failure.Causes[0]
			}
			for _, part := range failure.InstanceLocation {
				field += "/" + escapePointer(part)
			}
			switch issue := failure.ErrorKind.(type) {
			case *kind.Required:
				if len(issue.Missing) > 0 {
					field += "/" + escapePointer(issue.Missing[0])
				}
			case *kind.AdditionalProperties:
				if len(issue.Properties) > 0 {
					field += "/" + escapePointer(issue.Properties[0])
				}
			}
		}
		return Config{}, invalid(field, "does not satisfy deployment schema")
	}
	// JSON Schema integers include 80.0 and 8e1. Canonicalize only after the
	// complete schema has checked their exact values, before int32 model decode.
	canonical, err := json.Marshal(integerValues(value))
	if err != nil {
		return Config{}, invalid("", "cannot normalize JSON")
	}
	var access deployment.Access
	if err := json.Unmarshal(canonical, &access); err != nil {
		return Config{}, invalid("", "cannot decode generated deployment model")
	}
	access.TenantId = strings.ToLower(access.TenantId)
	c := Config{access: access}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	c.valid = true
	return c, nil
}

func integerValues(v any) any {
	switch v := v.(type) {
	case json.Number:
		if r, ok := new(big.Rat).SetString(string(v)); ok && r.IsInt() {
			return json.Number(r.Num().String())
		}
	case map[string]any:
		for key, child := range v {
			v[key] = integerValues(child)
		}
	case []any:
		for i, child := range v {
			v[i] = integerValues(child)
		}
	}
	return v
}

func escapePointer(part string) string {
	return strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
}

// This is a JSON syntax/duplicate-key reader, not a JSON Schema interpreter.
func readValue(d *json.Decoder, field string, depth int) (any, error) {
	if depth > 64 {
		return nil, invalid(field, "JSON nesting exceeds 64")
	}
	token, err := d.Token()
	if err != nil {
		return nil, invalid(field, "invalid JSON")
	}
	delim, isContainer := token.(json.Delim)
	if !isContainer {
		return token, nil
	}
	if depth >= 64 {
		return nil, invalid(field, "JSON nesting exceeds 64")
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return nil, invalid(field, "invalid object key")
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, invalid(field, "invalid object key")
			}
			childField := field + "/" + escapePointer(key)
			if _, duplicate := object[key]; duplicate {
				return nil, invalid(childField, "duplicate JSON key")
			}
			child, err := readValue(d, childField, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = child
		}
		if close, err := d.Token(); err != nil || close != json.Delim('}') {
			return nil, invalid(field, "invalid object terminator")
		}
		return object, nil
	case '[':
		array := []any{}
		for d.More() {
			child, err := readValue(d, field+"/"+strconv.Itoa(len(array)), depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, child)
		}
		if close, err := d.Token(); err != nil || close != json.Delim(']') {
			return nil, invalid(field, "invalid array terminator")
		}
		return array, nil
	default:
		return nil, invalid(field, "unexpected JSON delimiter")
	}
}

func (c *Config) validate() error {
	a := &c.access
	for _, entry := range []struct {
		field, host string
		ipv4        bool
	}{
		{"/portalOrigin/host", a.PortalOrigin.Host, a.Mode == "ip"},
		{"/adminOrigin/host", a.AdminOrigin.Host, a.Mode == "ip"},
		{"/issuerOrigin/host", a.IssuerOrigin.Host, a.Mode == "ip"},
		{"/instanceControl/dnsAlias", a.InstanceControl.DnsAlias, false},
		{"/instanceControl/origin/host", a.InstanceControl.Origin.Host, false},
	} {
		if err := canonicalHost(entry.field, entry.host, entry.ipv4); err != nil {
			return err
		}
	}
	if a.IssuerOrigin != a.PortalOrigin {
		return invalid("/issuerOrigin", "must equal portalOrigin")
	}
	if a.Mode == "ip" {
		if a.AdminOrigin.Host != a.PortalOrigin.Host || a.AdminOrigin.Scheme != a.PortalOrigin.Scheme || a.AdminOrigin.Port == a.PortalOrigin.Port {
			return invalid("/adminOrigin", "IP entries require the same IPv4 and scheme with distinct ports")
		}
	} else {
		if a.AdminOrigin.Host == a.PortalOrigin.Host {
			return invalid("/adminOrigin/host", "domain entries require distinct hosts")
		}
		instances := a.Instances.AccessInstancesSubdomain
		if instances == nil {
			return invalid("/instances", "missing subdomain branch")
		}
		if err := canonicalHost("/instances/parentDomain", instances.ParentDomain, false); err != nil {
			return err
		}
		for _, entry := range []struct{ field, host string }{{"portalOrigin", a.PortalOrigin.Host}, {"adminOrigin", a.AdminOrigin.Host}} {
			if inNamespace(entry.host, instances.ParentDomain) {
				return invalid("/"+entry.field+"/host", "reserved instance namespace")
			}
		}
		if instances.Scheme != a.PortalOrigin.Scheme || instances.Port != a.PortalOrigin.Port {
			return invalid("/instances", "scheme and port must equal portalOrigin")
		}
	}
	listeners := []struct {
		name     string
		listener deployment.Listener
	}{
		{"portal", a.Listeners.Portal}, {"admin", a.Listeners.Admin}, {"instanceControl", a.Listeners.InstanceControl},
	}
	for i, left := range listeners {
		ip, err := netip.ParseAddr(left.listener.Address)
		if err != nil || !ip.Is4() || ip.String() != left.listener.Address {
			return invalid("/listeners/"+left.name+"/address", "requires canonical IPv4")
		}
		for _, right := range listeners[:i] {
			if left.listener.Port == right.listener.Port && (left.listener.Address == right.listener.Address || left.listener.Address == "0.0.0.0" || right.listener.Address == "0.0.0.0") {
				return invalid("/listeners/"+left.name, "socket overlaps another listener role")
			}
		}
	}
	if proxy := a.Ingress.AccessIngressTrustedProxy; proxy != nil {
		if !a.Listeners.Portal.Tls || !a.Listeners.Admin.Tls {
			return invalid("/listeners", "trusted proxy requires TLS and configured mTLS")
		}
		for i, text := range proxy.PeerCidrs {
			p, err := netip.ParsePrefix(text)
			if err != nil || p.Bits() == 0 || p != p.Masked() || p.String() != text || p.Addr().Is4In6() {
				return invalid(fmt.Sprintf("/ingress/peerCidrs/%d", i), "requires canonical network CIDR excluding /0 and mapped IPv4")
			}
			c.peers = append(c.peers, p)
		}
	} else if a.Listeners.Portal.Tls != (a.PortalOrigin.Scheme == "https") || a.Listeners.Admin.Tls != (a.AdminOrigin.Scheme == "https") {
		return invalid("/listeners", "direct listener TLS must match corresponding browser scheme")
	}
	if !a.InsecureHttpAcknowledged && (a.PortalOrigin.Scheme == "http" || a.AdminOrigin.Scheme == "http" || a.Instances.AccessInstancesSubdomain != nil && a.Instances.AccessInstancesSubdomain.Scheme == "http") {
		return invalid("/insecureHttpAcknowledged", "browser HTTP requires explicit acknowledgement")
	}
	control := a.InstanceControl
	if control.Origin.Host != control.DnsAlias {
		return invalid("/instanceControl/origin/host", "must equal dnsAlias")
	}
	if control.Origin.Port != a.Listeners.InstanceControl.Port || (control.Origin.Scheme == "https") != a.Listeners.InstanceControl.Tls {
		return invalid("/instanceControl/origin", "port and TLS must match instanceControl listener")
	}
	if control.DnsAlias == a.PortalOrigin.Host || control.DnsAlias == a.AdminOrigin.Host || c.InstanceParentDomain() != "" && inNamespace(control.DnsAlias, c.InstanceParentDomain()) {
		return invalid("/instanceControl/dnsAlias", "conflicts with browser or reserved instance host")
	}
	return nil
}

func inNamespace(host, parent string) bool {
	return host == parent || strings.HasSuffix(host, "."+parent)
}

var dnsProfile = idna.New(idna.ValidateForRegistration(), idna.VerifyDNSLength(true), idna.Transitional(false))

func canonicalHost(field, host string, ipv4 bool) error {
	if ipv4 {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() || ip.String() != host {
			return invalid(field, "requires canonical IPv4")
		}
		return nil
	}
	ascii, err := dnsProfile.ToASCII(host)
	if err != nil || ascii != host {
		return invalid(field, "requires canonical lowercase ASCII DNS A-labels")
	}
	return nil
}
