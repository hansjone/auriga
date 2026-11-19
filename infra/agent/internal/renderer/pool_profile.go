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
client-ui-attachment host-frontend-static`

type poolProfileRow struct {
	ID     string         `yaml:"id"`
	Name   string         `yaml:"name"`
	Config map[string]any `yaml:"config"`
}

var poolRowID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var poolHostConfig = regexp.MustCompile(`^/var/lib/auriga/pools/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/control/shared-host\.json$`)

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
	if len(rows) != len(allowed) {
		return fmt.Errorf("complete shared profile requires all %d approved rows", len(allowed))
	}
	ids, names := map[string]bool{}, map[string]bool{}
	ports, host, browser := -1, -1, -1
	for i, row := range rows {
		if !poolRowID.MatchString(row.ID) || ids[row.ID] || names[row.Name] || !allowed[row.Name] {
			return fmt.Errorf("untrusted or duplicate pool profile row %q", row.ID)
		}
		ids[row.ID], names[row.Name] = true, true
		if err := validatePoolConfig(row); err != nil {
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
	if ports < 0 || host < 0 || browser < 0 || ports >= host || host >= browser {
		return fmt.Errorf("shared ports/Host/browser order required")
	}
	return nil
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
		"agent-preset":              {"id": "auriga-shared", "name": "Auriga 共享", "plugins": []any{}},
		"agent-default-model":       {"provider": "auriga-shared", "model": "gpt-6.1-sol"},
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
