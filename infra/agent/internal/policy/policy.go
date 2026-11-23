// Package policy is the node-local last line of defense (agent-policy.yaml).
package policy

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultMaxCPUMillis     = int64(8000)  // 8 cores
	DefaultMaxMemoryMB      = int64(16384) // 16 GiB
	DefaultMaxPIDs          = int64(2048)
	DefaultMaxArtifactBytes = int64(2 << 30) // 2 GiB
)

// Policy is loaded from agent-policy.yaml (admin-only on the node).
type Policy struct {
	// AllowedArtifactURLPrefixes: https URL prefixes (e.g. "https://artifacts.example.com/").
	// Empty means deny all remote downloads (tests can set AllowAnyArtifact).
	AllowedArtifactURLPrefixes []string `yaml:"allowedArtifactURLPrefixes"`
	// AllowedImageRepos: registry/path prefixes (e.g. "registry.example.com/auriga/").
	AllowedImageRepos []string `yaml:"allowedImageRepos"`
	// AllowedImageRefs optionally further restricts images to byte-for-byte references.
	// Nil adds no restriction; a non-nil empty list denies all images.
	AllowedImageRefs []string `yaml:"allowedImageRefs"`
	MaxCPUMillis     int64    `yaml:"maxCPUMillis"`
	MaxMemoryMB      int64    `yaml:"maxMemoryMB"`
	MaxPIDs          int64    `yaml:"maxPIDs"`
	MaxArtifactBytes int64    `yaml:"maxArtifactBytes"`
	// AllowAnyArtifact disables URL checks (unit tests only).
	AllowAnyArtifact bool `yaml:"allowAnyArtifact"`
	// AllowAnyImage disables image registry checks (unit tests only).
	AllowAnyImage bool `yaml:"allowAnyImage"`
	// Labels are operator-configured node labels advertised in Hello.Labels.
	// Reserved auriga.* capability labels are rejected.
	Labels map[string]string `yaml:"labels"`

	ArtifactConnectTimeout time.Duration `yaml:"-"`
	ArtifactTotalTimeout   time.Duration `yaml:"-"`
}

// Default returns conservative production defaults (deny-by-default for URLs/images).
func Default() Policy {
	return Policy{
		MaxCPUMillis:           DefaultMaxCPUMillis,
		MaxMemoryMB:            DefaultMaxMemoryMB,
		MaxPIDs:                DefaultMaxPIDs,
		MaxArtifactBytes:       DefaultMaxArtifactBytes,
		ArtifactConnectTimeout: 30 * time.Second,
		ArtifactTotalTimeout:   10 * time.Minute,
	}
}

// Load reads YAML from path. Missing file returns Default().
func Load(path string) (Policy, error) {
	p := Default()
	if path == "" {
		return p, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return Policy{}, err
	}
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return Policy{}, fmt.Errorf("policy: parse: %w", err)
	}
	if p.MaxCPUMillis <= 0 {
		p.MaxCPUMillis = DefaultMaxCPUMillis
	}
	if p.MaxMemoryMB <= 0 {
		p.MaxMemoryMB = DefaultMaxMemoryMB
	}
	if p.MaxPIDs <= 0 {
		p.MaxPIDs = DefaultMaxPIDs
	}
	if p.MaxArtifactBytes <= 0 {
		p.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if p.ArtifactConnectTimeout <= 0 {
		p.ArtifactConnectTimeout = 30 * time.Second
	}
	if p.ArtifactTotalTimeout <= 0 {
		p.ArtifactTotalTimeout = 10 * time.Minute
	}
	p.AllowedArtifactURLPrefixes = NormalizeURLPrefixes(p.AllowedArtifactURLPrefixes)
	if len(p.Labels) == 0 {
		p.Labels = nil
	} else if err := ValidateLabels(p.Labels); err != nil {
		return Policy{}, err
	}
	// AllowAny* from YAML is ignored in production loads — only tests set them in code.
	p.AllowAnyArtifact = false
	p.AllowAnyImage = false
	return p, nil
}

// NormalizeURLPrefixes ensures each prefix ends with "/" so HasPrefix cannot
// match a longer host/path (e.g. https://a.example.com vs https://a.example.com.evil/).
func NormalizeURLPrefixes(prefixes []string) []string {
	if len(prefixes) == 0 {
		return prefixes
	}
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		out = append(out, p)
	}
	return out
}

// ClampResources returns min(requested, policy max). Zero request uses a small default then clamps.
func (p Policy) ClampResources(cpuMillis, memoryMB, pids int64) (int64, int64, int64) {
	cpu := cpuMillis
	if cpu <= 0 {
		cpu = 2000
	}
	if cpu > p.MaxCPUMillis {
		cpu = p.MaxCPUMillis
	}
	mem := memoryMB
	if mem <= 0 {
		mem = 2048
	}
	if mem > p.MaxMemoryMB {
		mem = p.MaxMemoryMB
	}
	n := pids
	if n <= 0 {
		n = 512
	}
	if n > p.MaxPIDs {
		n = p.MaxPIDs
	}
	return cpu, mem, n
}

// AllowArtifactURL rejects non-https, non-allowlisted hosts, and private-looking schemes.
func (p Policy) AllowArtifactURL(raw string) error {
	if p.AllowAnyArtifact {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("policy: bad artifact url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("policy: artifact url must be https")
	}
	if u.Host == "" || strings.Contains(u.Host, "..") {
		return fmt.Errorf("policy: artifact url host invalid")
	}
	if len(p.AllowedArtifactURLPrefixes) == 0 {
		return fmt.Errorf("policy: no allowedArtifactURLPrefixes configured")
	}
	full := u.String()
	for _, prefix := range NormalizeURLPrefixes(p.AllowedArtifactURLPrefixes) {
		if prefix != "" && strings.HasPrefix(full, prefix) {
			return nil
		}
	}
	return fmt.Errorf("policy: artifact url not allowlisted")
}

// AllowImage requires repo@sha256:… and an allowlisted registry/path prefix,
// plus an exact reference match when AllowedImageRefs is non-nil.
func (p Policy) AllowImage(image string) error {
	if p.AllowedImageRefs != nil && !slices.Contains(p.AllowedImageRefs, image) {
		return fmt.Errorf("policy: image reference not allowlisted")
	}
	if p.AllowAnyImage {
		if !strings.Contains(image, "@sha256:") {
			return fmt.Errorf("policy: image must be pinned by digest")
		}
		return nil
	}
	repo, digest, ok := strings.Cut(image, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return fmt.Errorf("policy: image must be repo@sha256:<64 hex>")
	}
	if repo == "" || strings.Contains(repo, "..") {
		return fmt.Errorf("policy: image repository invalid")
	}
	if len(p.AllowedImageRepos) == 0 {
		return fmt.Errorf("policy: no allowedImageRepos configured")
	}
	for _, prefix := range p.AllowedImageRepos {
		if prefix != "" && strings.HasPrefix(repo, prefix) {
			return nil
		}
	}
	return fmt.Errorf("policy: image repository not allowlisted")
}

const (
	MaxLabelKeyLength   = 128
	MaxLabelValueLength = 63
)

// IsReservedLabelKey reports whether key uses the platform-reserved auriga.* prefix.
func IsReservedLabelKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(key), "auriga.")
}

// ValidateLabel validates a single operator-configured node label key and value.
func ValidateLabel(key, value string) error {
	if key == "" {
		return fmt.Errorf("policy: label key must not be empty")
	}
	if len(key) > MaxLabelKeyLength {
		return fmt.Errorf("policy: label key %q exceeds %d bytes", key, MaxLabelKeyLength)
	}
	if IsReservedLabelKey(key) {
		return fmt.Errorf("policy: label key %q uses reserved prefix %q", key, "auriga.")
	}
	if !validLabelToken(key, true) {
		return fmt.Errorf("policy: label key %q contains invalid characters", key)
	}
	if value == "" {
		return fmt.Errorf("policy: label %q value must not be empty", key)
	}
	if len(value) > MaxLabelValueLength {
		return fmt.Errorf("policy: label %q value exceeds %d bytes", key, MaxLabelValueLength)
	}
	if !validLabelToken(value, false) {
		return fmt.Errorf("policy: label %q value %q contains invalid characters", key, value)
	}
	return nil
}

// ValidateLabels validates all entries in an operator-configured label map.
func ValidateLabels(labels map[string]string) error {
	for k, v := range labels {
		if err := ValidateLabel(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ParseLabelsEnv parses a comma-separated "k1=v1,k2=v2" label list and validates each pair.
func ParseLabelsEnv(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]string, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("policy: empty label entry in %q", raw)
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("policy: label %q must be key=value", part)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if err := ValidateLabel(k, v); err != nil {
			return nil, err
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("policy: duplicate label key %q", k)
		}
		out[k] = v
	}
	return out, nil
}

func validLabelToken(s string, allowSlash bool) bool {
	if len(s) == 0 {
		return false
	}
	if !isASCIIAlnum(s[0]) || !isASCIIAlnum(s[len(s)-1]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isASCIIAlnum(c) || c == '-' || c == '_' || c == '.' || (allowSlash && c == '/') {
			continue
		}
		return false
	}
	return true
}

func isASCIIAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
