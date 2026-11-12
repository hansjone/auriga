package renderer

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const sharedSkillRow = "auriga-shared-skills"
const sharedSkillExpression = "process.getBuiltinModule('node:path').join(process.env.DSH_HOME, 'auriga', 'skills-active', 'current')"

// MergeSharedSkills preserves the upstream/user document byte for byte. Only a
// host-level insert is appended; preset providers remain owned by upstream.
// The trusted launcher must supply an absolute DSH_HOME.
func MergeSharedSkills(input []byte) ([]byte, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(string(input)))
	if err := decoder.Decode(&doc); err != nil {
		return input, fmt.Errorf("renderer: invalid shared skill YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return input, fmt.Errorf("renderer: multiple YAML documents")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return input, fmt.Errorf("renderer: Cordis patch must be a sequence")
	}
	if err := validateSharedYAML(&doc); err != nil {
		return input, err
	}
	// Decode also rejects duplicate mapping keys, including nested user rows.
	var value any
	if err := doc.Decode(&value); err != nil {
		return input, fmt.Errorf("renderer: invalid shared skill YAML: %w", err)
	}
	rows := doc.Content[0].Content
	var own []*yaml.Node
	for _, row := range rows {
		if insert := yamlField(row, "insert"); insert != nil {
			if insert.Kind != yaml.SequenceNode {
				return input, fmt.Errorf("renderer: invalid Cordis insert")
			}
			rows = append(rows, insert.Content...)
		}
	}
	for _, row := range rows {
		if row.Kind != yaml.MappingNode {
			return input, fmt.Errorf("renderer: invalid Cordis row")
		}
		id := yamlField(row, "id")
		config := yamlField(row, "config")
		provider := yamlField(config, "providerName")
		if id != nil && id.Value == sharedSkillRow {
			own = append(own, row)
		} else if provider != nil && provider.Value == "auriga-shared" {
			return input, fmt.Errorf("renderer: shared skill provider collision")
		}
	}
	if len(own) > 1 {
		return input, fmt.Errorf("renderer: duplicate shared skill row")
	}
	if len(own) == 1 {
		row := own[0]
		cfg := yamlField(row, "config")
		dirs := yamlField(cfg, "customSkillDirs")
		valid := yamlScalar(row, "name", "!!str", "@deepseek-ai/dsh-skill-filesystem") &&
			(yamlField(row, "disabled") == nil || yamlScalar(row, "disabled", "!!bool", "false")) &&
			cfg != nil && cfg.Kind == yaml.MappingNode && len(cfg.Content) == 8 &&
			yamlScalar(cfg, "providerName", "!!str", "auriga-shared") &&
			yamlScalar(cfg, "includeDefaultRoots", "!!bool", "false") &&
			yamlScalar(cfg, "watch", "!!bool", "true") &&
			dirs != nil && dirs.Kind == yaml.SequenceNode && len(dirs.Content) == 1 &&
			dirs.Content[0].Tag == "!!js" && dirs.Content[0].Value == sharedSkillExpression
		if !valid {
			return input, fmt.Errorf("renderer: conflicting shared skill row")
		}
		return input, nil
	}
	prefix := string(input)
	if len(doc.Content[0].Content) == 0 {
		prefix = ""
	} else if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return []byte(prefix + "- insert:\n" +
		"    - id: auriga-shared-skills\n" +
		"      name: '@deepseek-ai/dsh-skill-filesystem'\n" +
		"      config:\n" +
		"        providerName: auriga-shared\n" +
		"        includeDefaultRoots: false\n" +
		"        customSkillDirs:\n" +
		"          - !!js " + sharedSkillExpression + "\n" +
		"        watch: true\n"), nil
}

// Alias/merge expansion can conceal ownership or a duplicate provider from a
// syntactic row scan. Refuse sharing while leaving such user YAML untouched.
func validateSharedYAML(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return fmt.Errorf("renderer: aliases and merge keys cannot establish shared skill ownership")
	}
	for _, child := range node.Content {
		if err := validateSharedYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func yamlField(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func yamlScalar(node *yaml.Node, key, tag, value string) bool {
	field := yamlField(node, key)
	return field != nil && field.Kind == yaml.ScalarNode && field.Tag == tag && field.Value == value
}
