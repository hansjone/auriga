package renderer

import (
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Production composition uses the signed package's public ports export before
// the shared Host. A caller-supplied module is retained only for old fixtures.
func ValidateProductionPoolProfile(raw []byte) error {
	if err := ValidatePoolProfile(raw); err != nil {
		return err
	}
	var rows []struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(raw, &rows); err != nil {
		return err
	}
	ports, host, browser := -1, -1, -1
	for i, row := range rows {
		if row.Name == "file:///run/auriga/shared-ports.mjs" {
			return fmt.Errorf("production requires signed shared-ports export")
		}
		if row.Name == "auriga-dsh/shared-ports" {
			if ports >= 0 {
				return fmt.Errorf("duplicate ports")
			}
			ports = i
		}
		if row.Name == "auriga-dsh/shared" {
			if host >= 0 {
				return fmt.Errorf("duplicate shared Host")
			}
			host = i
		}
		if row.Name == "auriga-dsh/shared-browser" {
			if browser >= 0 {
				return fmt.Errorf("duplicate shared browser")
			}
			browser = i
		}
	}
	if ports < 0 || host < 0 || ports >= host {
		return fmt.Errorf("shared-ports must load before shared Host")
	}
	if browser >= 0 && browser <= host {
		return fmt.Errorf("shared browser must load after shared Host")
	}
	return nil
}

// ValidatePoolProfile checks a complete, expanded, installation-owned profile.
// It is NOT a patch over dsh-base/web-app: those bundles reintroduce controllers.
// P must supply the expanded public-host profile and verify it in the real host.
// An upgrade expands this explicit package allowlist only after review.
func ValidatePoolProfile(raw []byte) error {
	var rows []struct {
		ID     string         `yaml:"id"`
		Name   string         `yaml:"name"`
		Config map[string]any `yaml:"config"`
		Inject []string       `yaml:"inject,omitempty"`
	}
	d := yaml.NewDecoder(strings.NewReader(string(raw)))
	d.KnownFields(true)
	if e := d.Decode(&rows); e != nil {
		return e
	}
	if len(rows) == 0 {
		return fmt.Errorf("empty pool profile")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("multiple pool profile documents")
	}
	allowed := map[string]bool{"auriga-dsh": true, "auriga-dsh/shared": true, "auriga-dsh/shared-ports": true, "auriga-dsh/shared-browser": true, "file:///run/auriga/shared-ports.mjs": true}
	for _, name := range strings.Fields("agent agent-loop agent-instructions agent-preset agent-preset-registry agent-default-model tools tool-fs tool-fs-search tool-str-replace-editor tool-ask-user tool-present tool-todo tool-skill session session-reference session-format session-checkpoint-policy skill skill-filesystem workspace authorization llm web-frontend client-connection client-modules api-gateway api-remote-access api-session-controller host-webserver web-runtime system-prompt fs-local paths typert-registry") {
		allowed["@deepseek-ai/dsh-"+name] = true
	}
	ids := map[string]bool{}
	skill := false
	for _, r := range rows {
		if r.ID == "" || ids[r.ID] || !allowed[r.Name] {
			return fmt.Errorf("untrusted or duplicate pool profile row %q", r.ID)
		}
		ids[r.ID] = true
		if containsLoaderExpression(r.Config) {
			return fmt.Errorf("pool profile executable config rejected")
		}
		if r.Name == "auriga-dsh/shared-browser" {
			index, ok := r.Config["distIndex"].(string)
			if !ok || len(r.Config) != 1 || !path.IsAbs(index) || path.Clean(index) != index || path.Base(index) != "index.html" || strings.ContainsAny(index, "\x00\r\n?#\\") {
				return fmt.Errorf("shared browser requires its approved absolute frontend index")
			}
		}
		if r.Name == "@deepseek-ai/dsh-skill-filesystem" {
			dirs, ok := r.Config["customSkillDirs"].([]any)
			if skill || !ok || len(dirs) != 1 || dirs[0] != "/run/auriga/skills-active/current" || r.Config["includeDefaultRoots"] != false || r.Config["providerName"] != "auriga-shared" {
				return fmt.Errorf("pool skill root must be fixed platform view")
			}
			skill = true
		}
	}
	if !skill || !ids[PluginRowID] {
		return fmt.Errorf("missing trusted skill/host adapter")
	}
	// Reject aliases, merges and !!js at every depth, including configs.
	var doc yaml.Node
	if e := yaml.Unmarshal(raw, &doc); e != nil {
		return e
	}
	var walk func(*yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode || n.Tag == "!!merge" || n.Tag == "!!js" {
			return fmt.Errorf("executable/aliased pool profile")
		}
		for _, c := range n.Content {
			if e := walk(c); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(&doc)
}
