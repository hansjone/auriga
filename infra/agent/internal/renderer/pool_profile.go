package renderer

import (
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const SharedBrowserModule = "file:///opt/auriga-dsh/src/shared-browser/plugin.js"
const SharedFrontendIndex = "/opt/dsh/node_modules/@deepseek-ai/dsh-web-frontend/dist/index.html"

// The exact rc.3 public root closure frozen with profile-resolution.js. Tests
// compare it to the tracked recipe; no prefix or arbitrary URL is trusted.
const sharedSDKNames = `fs-local llm system-prompt session session-projection
session-persistence-jsonl session-query storage storage-json storage-domain
session-projection-cache working-directory agent tools agent-loop
agent-preset-registry agent-preset agent-default-model skill skill-filesystem
tool-skill shell-env commands credentials-local client-connection attachment-local
client-file-upload file-reference-local workspace host-directory-picker-browse
api-remote-access typert-registry typert-loader api-session-controller
api-workspace-controller api-gateway host-webserver client-modules api-remotes
client-ui-renderer client-locale client-shortcuts client-ui-theme client-ui-settings
client-ui-session client-ui-layout client-resources client-ui-sidebar
client-ui-sidebar-right client-ui-workspace client-ui-conversation
client-ui-input-trigger client-ui-chat client-ui-tool client-ui-trajectory
client-ui-attachment client-ui-agent-preset host-frontend-static`

// Five agent-preset rows: empty default + four shell-stripped native ids.
const approvedSharedAgentPresets = 5

type poolProfileRow struct {
	ID     string         `yaml:"id"`
	Name   string         `yaml:"name"`
	Config map[string]any `yaml:"config"`
}

var poolRowID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var poolHostConfig = regexp.MustCompile(`^/var/lib/auriga/pools/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/control/shared-host\.json$`)

var approvedAgentPresetIDs = map[string]int{
	"auriga-shared": 0,
	"standard":      1,
	"ptc":           2,
	"minimal":       3,
	"cordis":        4,
}

var allowedNestedPresetPlugins = map[string]bool{
	"@deepseek-ai/dsh-persona":                       true,
	"@deepseek-ai/dsh-agent-instructions":            true,
	"@deepseek-ai/dsh-time-context":                  true,
	"@deepseek-ai/dsh-tool-skill":                    true,
	"@deepseek-ai/dsh-command-goal":                  true,
	"@deepseek-ai/dsh-tool-goal":                     true,
	"@deepseek-ai/dsh-plan-mode":                     true,
	"@deepseek-ai/dsh-compaction-basic":              true,
	"@deepseek-ai/dsh-command-compact":               true,
	"@deepseek-ai/dsh-compaction-tool-result-pruner": true,
	"@deepseek-ai/dsh-tool-ask-user":                 true,
	"@deepseek-ai/dsh-tool-todo":                     true,
	"@deepseek-ai/dsh-tool-web":                      true,
	"@deepseek-ai/dsh-agent-tool-presentation":       true,
	"cordis:group":                                   true,
}

var forbiddenNestedPreset = regexp.MustCompile(`(?i)(?:^|/)(?:shell|terminal|tool-bash|tool-pwsh|tool-fs|tool-jobs|tool-schedule|tool-subagent|tool-present$|workflow|tool-workflow|tool-cordis|tool-ralph|plugin-manager|plugin-market|persistent)`)

func ValidateProductionPoolProfile(raw []byte) error { return ValidatePoolProfile(raw) }

// ValidatePoolProfile accepts only the complete literal shared profile. A
// partial fixture, owner bundle or dedicated profile cannot become production
// composition through this validator.
func ValidatePoolProfile(raw []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return err
	}
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		if node.Kind == yaml.AliasNode {
			return fmt.Errorf("aliased pool profile")
		}
		if node.Tag != "" {
			switch node.Tag {
			case "!!map", "!!seq", "!!str", "!!int", "!!bool", "!!null":
			default:
				return fmt.Errorf("nonliteral pool profile tag")
			}
		}
		for _, child := range node.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&document); err != nil {
		return err
	}
	var rows []poolProfileRow
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rows); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple pool profile documents")
	}
	allowed := map[string]bool{"auriga-dsh/shared-ports": true, "auriga-dsh/shared": true, SharedBrowserModule: true}
	for _, name := range strings.Fields(sharedSDKNames) {
		allowed["@deepseek-ai/dsh-"+name] = true
	}
	// Unique package names plus four extra agent-preset rows.
	if len(rows) != len(allowed)+approvedSharedAgentPresets-1 {
		return fmt.Errorf("complete shared profile requires all %d approved rows", len(allowed)+approvedSharedAgentPresets-1)
	}
	ids, names := map[string]bool{}, map[string]bool{}
	presetIDs := map[string]bool{}
	ports, host, browser, presets := -1, -1, -1, 0
	for i, row := range rows {
		multiPreset := row.Name == "@deepseek-ai/dsh-agent-preset"
		if !poolRowID.MatchString(row.ID) || ids[row.ID] || (!multiPreset && names[row.Name]) || !allowed[row.Name] {
			return fmt.Errorf("untrusted or duplicate pool profile row %q", row.ID)
		}
		ids[row.ID], names[row.Name] = true, true
		if multiPreset {
			presets++
			if err := validateAgentPresetConfig(row.Config, presetIDs); err != nil {
				return err
			}
		} else if err := validatePoolConfig(row); err != nil {
			return err
		}
		switch row.Name {
		case "auriga-dsh/shared-ports":
			ports = i
			if row.ID != "auriga-shared-ports" {
				return fmt.Errorf("fixed shared ports role required")
			}
		case "auriga-dsh/shared":
			host = i
			if row.ID != PluginRowID {
				return fmt.Errorf("fixed shared Host role required")
			}
		case SharedBrowserModule:
			browser = i
			if row.ID != "auriga-shared-browser" {
				return fmt.Errorf("fixed shared browser role required")
			}
		}
	}
	if presets != approvedSharedAgentPresets {
		return fmt.Errorf("shared profile requires %d approved agent-preset rows", approvedSharedAgentPresets)
	}
	if len(presetIDs) != approvedSharedAgentPresets {
		return fmt.Errorf("shared profile requires every approved agent-preset id")
	}
	if ports < 0 || host < 0 || browser < 0 || ports >= host || host >= browser {
		return fmt.Errorf("shared ports/Host/browser order required")
	}
	return nil
}

func validateAgentPresetConfig(cfg map[string]any, seen map[string]bool) error {
	if cfg == nil {
		return fmt.Errorf("agent-preset config required")
	}
	id, _ := cfg["id"].(string)
	expectedOrder, ok := approvedAgentPresetIDs[id]
	if !ok || seen[id] {
		return fmt.Errorf("unapproved or duplicate agent preset %q", id)
	}
	seen[id] = true
	for key := range cfg {
		switch key {
		case "id", "name", "order", "plugins", "description":
		default:
			return fmt.Errorf("unapproved agent-preset field %q", key)
		}
	}
	plugins, ok := cfg["plugins"].([]any)
	if !ok {
		return fmt.Errorf("agent-preset plugins required")
	}
	if id == "auriga-shared" {
		name, _ := cfg["name"].(string)
		if name != "共享助手" && name != "Auriga 共享" {
			return fmt.Errorf("default shared preset display name required")
		}
		if len(plugins) != 0 {
			return fmt.Errorf("default shared preset must stay tool-empty")
		}
		return nil
	}
	order, ok := asInt(cfg["order"])
	if !ok || order != expectedOrder {
		return fmt.Errorf("agent preset %q order required", id)
	}
	return walkApprovedPresetPlugins(plugins)
}

func walkApprovedPresetPlugins(plugins []any) error {
	for _, entry := range plugins {
		row, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("agent-preset plugin must be a map")
		}
		name, _ := row["name"].(string)
		if name == "" || !allowedNestedPresetPlugins[name] || forbiddenNestedPreset.MatchString(name) {
			return fmt.Errorf("unapproved nested agent-preset plugin %q", name)
		}
		for key := range row {
			switch key {
			case "id", "name", "config", "group", "isolate", "disabled":
			default:
				return fmt.Errorf("unapproved nested agent-preset field %q", key)
			}
		}
		if nested, ok := row["config"].([]any); ok {
			if err := walkApprovedPresetPlugins(nested); err != nil {
				return err
			}
			continue
		}
		if cfg, ok := row["config"].(map[string]any); ok {
			if err := walkPresetPluginConfig(cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkPresetPluginConfig(cfg map[string]any) error {
	for key, value := range cfg {
		switch typed := value.(type) {
		case map[string]any:
			if err := walkPresetPluginConfig(typed); err != nil {
				return err
			}
		case []any:
			for _, child := range typed {
				if childMap, ok := child.(map[string]any); ok {
					if name, _ := childMap["name"].(string); name != "" {
						if err := walkApprovedPresetPlugins([]any{childMap}); err != nil {
							return err
						}
						continue
					}
					if err := walkPresetPluginConfig(childMap); err != nil {
						return err
					}
				}
			}
		case string:
			if forbiddenNestedPreset.MatchString(typed) && strings.Contains(typed, "@deepseek-ai/dsh-") {
				return fmt.Errorf("unapproved nested agent-preset reference %q", typed)
			}
		}
		_ = key
	}
	return nil
}

func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float64:
		if typed == float64(int(typed)) {
			return int(typed), true
		}
	}
	return 0, false
}

func validatePoolConfig(row poolProfileRow) error {
	// Only these providers have approved config. Everything else is passive,
	// and cannot receive nested plugins, imports, paths or controller options.
	configs := map[string]map[string]any{
		"fs-local":                  {"cwd": "/home/dsh/.host"},
		"session-persistence-jsonl": {"root": "/home/dsh/.host/.dsh/sessions", "compression": "none"},
		"storage-json":              {"root": "/home/dsh/.host/.dsh/storages"},
		"storage-domain":            {"backend": "json"},
		"session-projection-cache":  {"writeEveryEvents": 200, "writeIntervalMs": 5000},
		"working-directory":         {"defaultDirectory": "/home/dsh/.host"},
		"tools":                     {"mode": "native", "maxParallelSubCalls": 1},
		"agent-loop":                {"agents": []any{}, "maxParallelToolCalls": 1},
		"agent-preset-registry":     {"default": "auriga-shared"},
		"agent-default-model":       {"provider": "auriga-shared", "model": "auto"},
		"skill-filesystem":          {"providerName": "auriga-shared", "includeDefaultRoots": false, "customSkillDirs": []any{"/run/auriga/skills-active/current"}},
		"credentials-local":         {"path": "/home/dsh/.host/.dsh/shared-host-credentials.yaml", "watch": false},
		"client-connection":         {"trustedHosts": []any{}},
		"attachment-local":          {"dshHome": "/home/dsh/.host/.dsh"},
		"host-frontend-static":      {"distIndex": SharedFrontendIndex},
	}
	var expected map[string]any
	switch row.Name {
	case SharedBrowserModule:
		expected = map[string]any{"distIndex": SharedFrontendIndex}
	case "auriga-dsh/shared-ports":
		file, ok := row.Config["configFile"].(string)
		if !ok || !poolHostConfig.MatchString(file) || len(row.Config) != 1 {
			return fmt.Errorf("fixed shared Host config path required")
		}
		return nil
	case "@deepseek-ai/dsh-host-webserver":
		port, ok := row.Config["port"].(int)
		if !ok || port < 1 || port > 65535 || row.Config["host"] != "127.0.0.1" || len(row.Config) != 2 {
			return fmt.Errorf("fixed shared listener placeholder and port required")
		}
		return nil
	default:
		expected = configs[strings.TrimPrefix(row.Name, "@deepseek-ai/dsh-")]
	}
	if len(expected) == 0 && len(row.Config) == 0 {
		return nil
	}
	if !reflect.DeepEqual(expected, row.Config) {
		return fmt.Errorf("unapproved config for %s", row.Name)
	}
	return nil
}
