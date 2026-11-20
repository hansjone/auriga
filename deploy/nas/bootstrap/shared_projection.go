package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type sharedPublicEnterprisePolicy struct {
	AllowRawAgentToken   bool     `json:"allowRawAgentToken"`
	OutboundAllowedHosts []string `json:"outboundAllowedHosts"`
	EmpNoHeader          string   `json:"empNoHeader,omitempty"`
	AuthValueHeader      string   `json:"authValueHeader,omitempty"`
}

// sharedEnterprisePolicy projects only public fields from the exact private
// adapter approved by the installation controller. It never resolves DNS,
// substitutes a destination, or includes credentials in output or errors.
// The IAM consumer retains the original full origin/address pinning policy.
func sharedEnterprisePolicy(adapter []byte) ([]byte, error) {
	invalid := func() ([]byte, error) {
		return nil, fmt.Errorf("approved private adapter requires a complete sharedOutbound policy")
	}
	if len(adapter) == 0 || len(adapter) > 2<<20 || sharedUniqueJSON(adapter) != nil {
		return invalid()
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(adapter, &document) != nil || document == nil {
		return invalid()
	}
	if _, configured := document["sharedOutbound"]; !configured {
		// Keep all other approved UAC mappings. The original IAM missing-node
		// path disables only enterprise outbound; Host independently denies all
		// targets. Raw credentials still require current member/task authority.
		return json.Marshal(sharedPublicEnterprisePolicy{AllowRawAgentToken: true, OutboundAllowedHosts: []string{}})
	}
	var policy struct {
		EmpNoHeader         string `json:"empNoHeader"`
		AuthValueHeader     string `json:"authValueHeader"`
		AllowedDestinations []struct {
			Origin    string   `json:"origin"`
			Addresses []string `json:"addresses"`
		} `json:"allowedDestinations"`
	}
	d := json.NewDecoder(bytes.NewReader(document["sharedOutbound"]))
	d.DisallowUnknownFields()
	if d.Decode(&policy) != nil || len(policy.AllowedDestinations) == 0 || len(policy.AllowedDestinations) > 64 {
		return invalid()
	}
	header := regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$")
	blocked := map[string]bool{"host": true, "content-length": true, "transfer-encoding": true, "connection": true, "proxy-authorization": true, "te": true, "trailer": true, "upgrade": true}
	for _, value := range []string{policy.EmpNoHeader, policy.AuthValueHeader} {
		if !header.MatchString(value) || blocked[strings.ToLower(value)] {
			return invalid()
		}
	}
	if strings.EqualFold(policy.EmpNoHeader, policy.AuthValueHeader) {
		return invalid()
	}
	origins, hosts := map[string]bool{}, map[string]bool{}
	for _, destination := range policy.AllowedDestinations {
		u, err := url.Parse(destination.Origin)
		if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), "*%\\ \t\r\n") || len(destination.Addresses) == 0 || len(destination.Addresses) > 32 {
			return invalid()
		}
		host := strings.ToLower(u.Hostname())
		port := u.Port()
		if port == "" {
			port = "443"
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid()
		}
		origin := net.JoinHostPort(host, strconv.Itoa(n))
		if origins[origin] {
			return invalid()
		}
		origins[origin] = true
		pinnedLiteral := net.ParseIP(host) == nil
		for _, address := range destination.Addresses {
			ip := net.ParseIP(address)
			if ip == nil {
				return invalid()
			}
			if ip.Equal(net.ParseIP(host)) {
				pinnedLiteral = true
			}
		}
		if !pinnedLiteral {
			return invalid()
		}
		hosts[host] = true
	}
	public := sharedPublicEnterprisePolicy{AllowRawAgentToken: true, EmpNoHeader: policy.EmpNoHeader, AuthValueHeader: policy.AuthValueHeader}
	for host := range hosts {
		public.OutboundAllowedHosts = append(public.OutboundAllowedHosts, host)
	}
	sort.Strings(public.OutboundAllowedHosts)
	return json.Marshal(public)
}

// Reject duplicate keys anywhere, including unrelated private adapter sections.
// Encoding/json's last-value-wins behavior must not create a second policy.
func sharedUniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting limit")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

// This is conversion, not verification or approval. The Linux controller must
// resolve and validate the committed runtime source before calling it. The full
// signed envelope and resources are retained; personal-only template fields are
// excluded before the resulting shared snapshot is checked by the controller.
func sharedRuntimeSpecJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return nil, fmt.Errorf("bounded runtime template required")
	}
	spec := new(nodev1.InstanceSpec)
	if proto.Unmarshal(raw, spec) != nil || !sharedKnownProto(spec.ProtoReflect()) {
		return nil, fmt.Errorf("unrecognized runtime template")
	}
	if spec.GetRuntime() != nodev1.RuntimeKind_RUNTIME_KIND_DOCKER || spec.GetDsh().GetReleaseEnvelope().GetReleaseSequence() == 0 {
		return nil, fmt.Errorf("signed Docker runtime template required")
	}
	secrets := spec.GetSecrets()
	if proto.Size(secrets.GetInstanceToken()) != 0 || proto.Size(secrets.GetAiGatewayToken()) != 0 ||
		proto.Size(secrets.GetDeviceCredential()) != 0 || proto.Size(secrets.GetModelCredential()) != 0 {
		return nil, fmt.Errorf("personal sealed credentials are not shared public inputs")
	}
	if spec.OwnerId != "" || len(spec.Plugins) != 0 || len(spec.UnifiedResources) != 0 || len(spec.GetProfile().GetEnv()) != 0 || spec.Generation < 0 {
		return nil, fmt.Errorf("personal runtime state is not a shared template")
	}
	// A classic installation supplies an unassigned template. Shared pools own
	// their initial operation and replace classic entry patches with the
	// independently approved complete shared or dedicated profile.
	if spec.Generation == 0 {
		spec.Generation = 1
	}
	// Classic templates carry only public edge identity verification keys here.
	// Shared operation uses the separately approved purpose keys, never this
	// classic Secrets wrapper. The sealed credential fields above remain denied.
	spec.Secrets = nil
	if spec.Profile != nil {
		spec.Profile.Patches = nil
	}
	body, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("runtime JSON conversion failed")
	}
	roundtrip := new(nodev1.InstanceSpec)
	if protojson.Unmarshal(body, roundtrip) != nil || !proto.Equal(spec, roundtrip) {
		return nil, fmt.Errorf("runtime protobuf roundtrip failed")
	}
	return append(body, '\n'), nil
}

func sharedKnownProto(message protoreflect.Message) bool {
	if len(message.GetUnknown()) != 0 {
		return false
	}
	known := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsMap():
			if field.MapValue().Message() != nil {
				value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
					known = sharedKnownProto(entry.Message())
					return known
				})
			}
		case field.IsList():
			if field.Message() != nil {
				for i := 0; i < value.List().Len(); i++ {
					if !sharedKnownProto(value.List().Get(i).Message()) {
						known = false
						break
					}
				}
			}
		case field.Message() != nil:
			known = sharedKnownProto(value.Message())
		}
		return known
	})
	return known
}
