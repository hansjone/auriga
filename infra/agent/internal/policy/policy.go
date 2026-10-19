// Package policy is the node-local last line of defense (agent-policy.yaml).
package policy

import (
	"fmt"
	"net/url"
	"os"
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
	MaxCPUMillis      int64    `yaml:"maxCPUMillis"`
	MaxMemoryMB       int64    `yaml:"maxMemoryMB"`
	MaxPIDs           int64    `yaml:"maxPIDs"`
	MaxArtifactBytes  int64    `yaml:"maxArtifactBytes"`
	// AllowAnyArtifact disables URL checks (unit tests only).
	AllowAnyArtifact bool `yaml:"allowAnyArtifact"`
	// AllowAnyImage disables image registry checks (unit tests only).
	AllowAnyImage bool `yaml:"allowAnyImage"`

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

// AllowImage requires repo@sha256:… and an allowlisted registry/path prefix.
func (p Policy) AllowImage(image string) error {
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
