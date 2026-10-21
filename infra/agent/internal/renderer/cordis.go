package renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"gopkg.in/yaml.v3"
)

// PluginRowID is the Loader row id of the managed plugin. Spec patches address
// it as `plugins.auriga-dsh.<key>` (contracts/proto/auriga/node/v1/spec.proto).
const PluginRowID = "auriga-dsh"

const (
	pluginEntry  = "auriga-dsh/plugin"
	mcpClientPkg = "@deepseek-ai/dsh-mcp-client"
	mcpRowPrefix = "auriga-mcp-"
)

var (
	rowIDRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	patchKeyRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	mcpNameRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	parentDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	hostLabelRe  = regexp.MustCompile(`^[a-z0-9_-]{1,63}$`)
)

// Rows the renderer owns: a spec patch must not widen the Host/Origin fence or
// move the listener. `connection` takes its trustedHosts from `web-runtime`.
var reservedRows = map[string]struct{}{
	"webserver":   {},
	"web-runtime": {},
	"connection":  {},
}

// Plugin config keys the renderer owns.
var reservedPluginKeys = map[string]struct{}{
	"trustedHosts": {},
}

// jsExpr renders as a DSH Loader `!!js` expression node.
type jsExpr string

func (e jsExpr) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!js", Value: string(e), Style: yaml.DoubleQuotedStyle}, nil
}

// tokenHeader reads the token file when the Loader evaluates the config, so the
// token value never lands in the YAML (contracts/plugin/README.md §3).
func tokenHeader(file string) jsExpr {
	return jsExpr(fmt.Sprintf("'Bearer ' + process.getBuiltinModule('node:fs').readFileSync(%s, 'utf8').trim()", jsString(file)))
}

func jsString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// TrustedHosts returns the edge authorities DSH must accept for this instance.
func TrustedHosts(instanceID, parent string) ([]string, error) {
	if parent == "" {
		return []string{}, nil
	}
	parent = strings.ToLower(strings.TrimSuffix(parent, "."))
	if !parentDomain.MatchString(parent) {
		return nil, fmt.Errorf("renderer: invalid instance parent domain")
	}
	label := strings.ToLower(instanceID)
	if !hostLabelRe.MatchString(label) {
		return nil, fmt.Errorf("renderer: instance id is not a host label")
	}
	return []string{label + "." + parent}, nil
}

// buildCordisPatch renders a DSH Loader patch list (an `--patch` layer). Rows
// with an id replace that row's whole config; `insert` appends new rows.
func buildCordisPatch(spec *nodev1.InstanceSpec, opt Options) ([]byte, error) {
	trusted, err := TrustedHosts(spec.InstanceId, opt.InstanceParentDomain)
	if err != nil {
		return nil, err
	}
	prof := spec.GetProfile()
	port := uint32(7860)
	if prof.GetListenPort() != 0 {
		port = prof.GetListenPort()
	}
	// Containers are reached over their isolated network; native runtimes only
	// through the agent's loopback ingress.
	host := "127.0.0.1"
	if spec.GetRuntime() == nodev1.RuntimeKind_RUNTIME_KIND_DOCKER {
		host = "0.0.0.0"
	}

	pluginCfg := map[string]any{"trustedHosts": trusted}
	overrides := map[string]map[string]any{}
	var overrideOrder []string
	for _, p := range prof.GetPatches() {
		if p == nil {
			continue
		}
		if err := validatePatch(p.Path, p.ValueJson); err != nil {
			return nil, err
		}
		row, keys, err := splitPatchPath(p.Path)
		if err != nil {
			return nil, err
		}
		value, err := decodePatchValue(p.ValueJson)
		if err != nil {
			return nil, err
		}
		target := pluginCfg
		if row == PluginRowID {
			if _, ok := reservedPluginKeys[keys[0]]; ok {
				return nil, fmt.Errorf("renderer: patch path %q is reserved", p.Path)
			}
		} else {
			if _, ok := overrides[row]; !ok {
				overrides[row] = map[string]any{}
				overrideOrder = append(overrideOrder, row)
			}
			target = overrides[row]
		}
		if err := setPath(target, keys, value); err != nil {
			return nil, fmt.Errorf("renderer: patch path %q: %w", p.Path, err)
		}
	}

	inserts := []any{map[string]any{
		"id":     PluginRowID,
		"name":   pluginEntry,
		"inject": []string{"webServer", "connection"},
		"config": pluginCfg,
	}}
	for _, name := range sortedKeysMap(prof.GetMcpServers()) {
		if !mcpNameRe.MatchString(name) {
			return nil, fmt.Errorf("renderer: invalid mcp server name")
		}
		srv := prof.GetMcpServers()[name]
		if err := rejectUnsafeString("mcp.url", srv.GetUrl()); err != nil {
			return nil, err
		}
		if !safeURLRe.MatchString(srv.GetUrl()) {
			return nil, fmt.Errorf("renderer: invalid mcp url")
		}
		tokenFile, err := tokenFileFor(srv.GetTokenRef())
		if err != nil {
			return nil, err
		}
		inserts = append(inserts, map[string]any{
			"id":   mcpRowPrefix + name,
			"name": mcpClientPkg,
			"config": map[string]any{
				"transport":  "streamable-http",
				"serverName": name,
				"url":        srv.GetUrl(),
				"headers":    map[string]any{"Authorization": tokenHeader(tokenFile)},
			},
		})
	}
	if len(prof.GetModelProviders()) > 0 {
		// Team models belong in the user's settings document (only the
		// platform-marked entry), not in a Loader row; fail closed until wired.
		return nil, fmt.Errorf("renderer: model providers are not rendered by this agent yet")
	}

	rows := []any{
		map[string]any{"insert": inserts},
		map[string]any{"id": "webserver", "config": map[string]any{
			"host":                      host,
			"port":                      int(port),
			"compression":               "gzip",
			"compressionLevel":          1,
			"compressionThresholdBytes": 1024,
		}},
		map[string]any{"id": "web-runtime", "config": map[string]any{
			"openBrowser":    false,
			"printUrl":       false,
			"surfaceContext": true,
			"trustedHosts":   trusted,
		}},
	}
	for _, row := range overrideOrder {
		rows = append(rows, map[string]any{"id": row, "config": overrides[row]})
	}
	// Audit the assembled document as well as each input value: path expansion
	// must never synthesize the Loader's executable-object marker. Renderer-owned
	// jsExpr values are a distinct type and remain the only allowed expressions.
	if containsLoaderExpression(rows) {
		return nil, fmt.Errorf("renderer: assembled patch contains a Loader expression")
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(rows); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tokenFileFor(ref nodev1.TokenRef) (string, error) {
	switch ref {
	case nodev1.TokenRef_TOKEN_REF_INSTANCE_TOKEN:
		return ContainerInstanceToken, nil
	case nodev1.TokenRef_TOKEN_REF_AI_GATEWAY_TOKEN:
		return ContainerAIGatewayToken, nil
	default:
		return "", fmt.Errorf("renderer: mcp token_ref required")
	}
}

// splitPatchPath maps `plugins.<rowId>.<key>[.<key>…]` to a Loader row config path.
func splitPatchPath(path string) (string, []string, error) {
	parts := strings.Split(path, ".")
	if len(parts) < 3 || parts[0] != "plugins" {
		return "", nil, fmt.Errorf("renderer: patch path %q must be plugins.<row>.<key>", path)
	}
	for _, part := range parts[1:] {
		if part == "__jsExpr" {
			return "", nil, fmt.Errorf("renderer: patch path %q contains a Loader expression", path)
		}
	}
	row := parts[1]
	if !rowIDRe.MatchString(row) {
		return "", nil, fmt.Errorf("renderer: invalid patch row in %q", path)
	}
	if _, ok := reservedRows[row]; ok || strings.HasPrefix(row, mcpRowPrefix) {
		return "", nil, fmt.Errorf("renderer: patch path %q is reserved", path)
	}
	for _, k := range parts[2:] {
		if !patchKeyRe.MatchString(k) {
			return "", nil, fmt.Errorf("renderer: invalid patch key in %q", path)
		}
	}
	return row, parts[2:], nil
}

func decodePatchValue(raw string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("renderer: patch value is not JSON")
	}
	if dec.More() {
		return nil, fmt.Errorf("renderer: patch value is not a single JSON value")
	}
	if containsLoaderExpression(v) {
		return nil, fmt.Errorf("renderer: patch value contains a Loader expression")
	}
	return normalizeJSON(v), nil
}

// Loader treats an object carrying __jsExpr as executable even without a !!js
// YAML tag. Only expressions constructed by the renderer may reach that loader.
func containsLoaderExpression(v any) bool {
	switch value := v.(type) {
	case map[string]any:
		if _, ok := value["__jsExpr"]; ok {
			return true
		}
		for _, child := range value {
			if containsLoaderExpression(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsLoaderExpression(child) {
				return true
			}
		}
	}
	return false
}

// normalizeJSON turns json.Number into int64/float64 so YAML emits plain numbers.
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, item := range t {
			t[k] = normalizeJSON(item)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = normalizeJSON(item)
		}
		return t
	default:
		return v
	}
}

func setPath(target map[string]any, keys []string, value any) error {
	for _, k := range keys[:len(keys)-1] {
		next, ok := target[k]
		if !ok {
			child := map[string]any{}
			target[k] = child
			target = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("conflicts with an earlier scalar value")
		}
		target = child
	}
	target[keys[len(keys)-1]] = value
	return nil
}
