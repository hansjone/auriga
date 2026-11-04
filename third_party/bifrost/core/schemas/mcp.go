// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"

	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MCPOpenAPIConfig struct {
	Spec                string                          `json:"spec,omitempty"`
	SpecURL             *string                         `json:"spec_url,omitempty"`
	SpecFile            *string                         `json:"spec_file,omitempty"`
	BaseURL             *string                         `json:"base_url,omitempty"`
	SecurityCredentials map[string]MCPOpenAPICredential `json:"security_credentials,omitempty"`
	IncludeDeprecated   bool                            `json:"include_deprecated,omitempty"`
	MaxResponseBytes    int                             `json:"max_response_bytes,omitempty"`
	SpecSize            int                             `json:"spec_size,omitempty"`
	SpecHash            string                          `json:"spec_hash,omitempty"`
	SpecTitle           string                          `json:"spec_title,omitempty"`
	OpenAPIVersion      string                          `json:"openapi_version,omitempty"`
	OperationCount      int                             `json:"operation_count,omitempty"`
}
type MCPToolMetadata struct {
	Title        string          `json:"title,omitempty"`
	Icons        []mcp.Icon      `json:"icons,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}
type MCPClientConfig struct {
	ID                string            `json:"client_id"`
	Name              string            `json:"name"`
	EndpointSlug      string            `json:"endpoint_slug"`
	IsCodeModeClient  bool              `json:"is_code_mode_client"`
	ConnectionType    MCPConnectionType `json:"connection_type"`
	ConnectionString  *SecretVar        `json:"connection_string,omitempty"`
	StdioConfig       *MCPStdioConfig   `json:"stdio_config,omitempty"`
	TLSConfig         *MCPTLSConfig     `json:"tls_config,omitempty"`
	OpenAPIConfig     *MCPOpenAPIConfig `json:"openapi_config,omitempty"`
	AuthType          MCPAuthType       `json:"auth_type"`
	OauthConfigID     *string           `json:"oauth_config_id,omitempty"`
	OauthClientID     *SecretVar        `json:"oauth_client_id,omitempty"`
	OauthClientSecret *SecretVar        `json:"oauth_client_secret,omitempty"`

	OauthAuthorizeURL    string               `json:"oauth_authorize_url,omitempty"`
	OauthTokenURL        string               `json:"oauth_token_url,omitempty"`
	OauthRegistrationURL string               `json:"oauth_registration_url,omitempty"`
	OauthScopes          []string             `json:"oauth_scopes,omitempty"`
	OauthResource        string               `json:"oauth_resource,omitempty"`
	State                string               `json:"state,omitempty"`
	Headers              map[string]SecretVar `json:"headers,omitempty"`

	PerUserHeaderKeys []string `json:"per_user_header_keys,omitempty"`

	TokenExchange       *MCPTokenExchangeConfig `json:"token_exchange,omitempty"`
	AllowedExtraHeaders WhiteList               `json:"allowed_extra_headers,omitempty"`
	InProcessServer     *server.MCPServer       `json:"-"`
	ToolsToExecute      WhiteList               `json:"tools_to_execute,omitempty"`

	ToolsToAutoExecute WhiteList `json:"tools_to_auto_execute,omitempty"`

	PromptsToExpose   WhiteList `json:"prompts_to_expose"`
	ResourcesToExpose WhiteList `json:"resources_to_expose"`
	IsPingAvailable   *bool     `json:"is_ping_available,omitempty"`

	NeedsSessionStickiness *bool              `json:"needs_session_stickiness,omitempty"`
	RequirePublicTarget    bool               `json:"require_public_target,omitempty"`
	ToolSyncInterval       time.Duration      `json:"tool_sync_interval,omitempty"`
	MaxProtocolVersion     string             `json:"max_protocol_version,omitempty"`
	ToolExecutionTimeout   time.Duration      `json:"tool_execution_timeout,omitempty"`
	MaxInstructionsLength  int                `json:"max_instructions_length,omitempty"`
	ToolPricing            map[string]float64 `json:"tool_pricing,omitempty"`
	Disabled               bool               `json:"disabled"`
	ConfigHash             string             `json:"-"`

	AllowByDefault bool `json:"allow_by_default"`

	DiscoveredTools           map[string]ChatTool `json:"-"`
	DiscoveredToolNameMapping map[string]string   `json:"-"`

	DiscoveredInstructions string `json:"-"`

	DiscoveredResources         []mcp.Resource         `json:"-"`
	DiscoveredResourceTemplates []mcp.ResourceTemplate `json:"-"`
	DiscoveredPrompts           []mcp.Prompt           `json:"-"`

	PendingOAuthConfig *OAuth2Config `json:"oauth_config,omitempty"`
}
type MCPToolSpecConfig struct {
	MCPClientID   string   `json:"mcp_client_id,omitempty"`
	MCPClientName string   `json:"mcp_client_name,omitempty"`
	ToolNames     []string `json:"tool_names,omitempty"`
}
type MCPCodeModeLimits struct {
	MaxSourceBytes  int `json:"max_source_bytes,omitempty"`
	MaxSteps        int `json:"max_steps,omitempty"`
	MaxMemoryBytes  int `json:"max_memory_bytes,omitempty"`
	MaxLogBytes     int `json:"max_log_bytes,omitempty"`
	MaxToolCalls    int `json:"max_tool_calls,omitempty"`
	MaxValueBytes   int `json:"max_value_bytes,omitempty"`
	MaxNestingDepth int `json:"max_nesting_depth,omitempty"`
}
type MCPTokenExchangeConfig struct {
	Audience string `json:"audience"`

	ClientID *SecretVar `json:"client_id,omitempty"`

	ClientSecret *SecretVar `json:"client_secret,omitempty"`

	UseIdPCredentials bool `json:"use_idp_credentials,omitempty"`

	Scopes []string `json:"scopes,omitempty"`

	AuthorizationServerURL *string `json:"authorization_server_url,omitempty"`

	Profile string `json:"profile,omitempty"`

	Resource              string     `json:"resource,omitempty"`
	ResourceTokenEndpoint *string    `json:"resource_token_endpoint,omitempty"`
	ResourceClientID      *SecretVar `json:"resource_client_id,omitempty"`
	ResourceClientSecret  *SecretVar `json:"resource_client_secret,omitempty"`

	ResourcePrivateKey       *SecretVar `json:"resource_private_key,omitempty"`
	ResourceKeyID            string     `json:"resource_key_id,omitempty"`
	ResourceSigningAlgorithm string     `json:"resource_signing_algorithm,omitempty"`

	ResourceAssertionAudience string `json:"resource_assertion_audience,omitempty"`

	ResourceClientAuthMethod string `json:"resource_client_auth_method,omitempty"`
}
type InProcessServerDeps struct {
	HTTPClient *http.Client
	Headers    func(ctx context.Context) (http.Header, error)
	Logger     Logger
}
type MCPConnectionType string
type InProcessServerFactory func(ctx context.Context, config *MCPClientConfig, deps InProcessServerDeps) (*server.MCPServer, error)
type MCPOpenAPICredential struct {
	Value    *SecretVar `json:"value,omitempty"`
	Username *SecretVar `json:"username,omitempty"`
	Password *SecretVar `json:"password,omitempty"`
}
type MCPAuthType string
type CodeModeBindingLevel string
type MCPAuthRequiredError struct {
	Kind          string `json:"kind"`
	MCPClientID   string `json:"mcp_client_id"`
	MCPClientName string `json:"mcp_client_name"`
	Message       string `json:"message"`

	AuthorizeURL string `json:"authorize_url,omitempty"`
	SessionID    string `json:"session_id,omitempty"`

	SubmitURL          string   `json:"submit_url,omitempty"`
	RequiredHeaderKeys []string `json:"required_header_keys,omitempty"`
	AdminHeaderKeys    []string `json:"admin_header_keys,omitempty"`

	SubjectTokenMissing bool   `json:"subject_token_missing,omitempty"`
	ExchangeError       string `json:"exchange_error,omitempty"`
}
type MCPConfig struct {
	ClientConfigs     []*MCPClientConfig    `json:"client_configs,omitempty"`
	ToolManagerConfig *MCPToolManagerConfig `json:"tool_manager_config,omitempty"`
	ToolSyncInterval  time.Duration         `json:"tool_sync_interval,omitempty"`

	MaxProtocolVersion string `json:"max_protocol_version,omitempty"`

	VirtualMCPs []VirtualMCPConfig `json:"virtual_mcps,omitempty"`

	FetchNewRequestIDFunc func(ctx *BifrostContext) string `json:"-"`

	PluginPipelineProvider func() interface{} `json:"-"`

	ReleasePluginPipeline func(pipeline interface{}) `json:"-"`

	InProcessServerFactory InProcessServerFactory `json:"-"`
}
type MCPTLSConfig struct {
	InsecureSkipVerify bool       `json:"insecure_skip_verify,omitempty"`
	CACertPEM          *SecretVar `json:"ca_cert_pem,omitempty"`
}
type MCPStdioConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Envs    []string `json:"envs"`
}
type MCPToolManagerConfig struct {
	ToolExecutionTimeout  Duration             `json:"tool_execution_timeout"`
	MaxAgentDepth         int                  `json:"max_agent_depth"`
	CodeModeBindingLevel  CodeModeBindingLevel `json:"code_mode_binding_level,omitempty"`
	DisableAutoToolInject bool                 `json:"disable_auto_tool_inject,omitempty"`

	MaxInstructionsPerClient int `json:"max_instructions_per_client,omitempty"`

	MaxInstructionsTotal  int                `json:"max_instructions_total,omitempty"`
	CodeModeLimits        *MCPCodeModeLimits `json:"code_mode_limits,omitempty"`
	OAuthPrivateAllowlist []string           `json:"oauth_private_allowlist,omitempty"`
}
type VirtualMCPConfig struct {
	ID uint `json:"id,omitempty"`

	Name string `json:"name"`

	EndpointSlug string `json:"endpoint_slug,omitempty"`

	Description *string `json:"description,omitempty"`

	Enabled *bool `json:"enabled,omitempty"`

	Tools []MCPToolSpecConfig `json:"tools"`

	VirtualKeyIDs []string `json:"virtual_key_ids,omitempty"`
}
type MCPClientConnectionInfo struct {
	Type               MCPConnectionType `json:"type"`
	ConnectionURL      *string           `json:"connection_url,omitempty"`
	StdioCommandString *string           `json:"stdio_command_string,omitempty"`
}

func (e *MCPAuthRequiredError) Error() string {
	return e.Message
}

func (c *MCPConfig) UnmarshalJSON(data []byte) error {
	type alias MCPConfig
	aux := &struct {
		ToolSyncInterval *json.Number `json:"tool_sync_interval,omitempty"`
		*alias
	}{alias: (*alias)(c)}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(aux); err == nil {
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return errors.New("trailing JSON data")
		}
		if aux.ToolSyncInterval == nil {
			return nil
		}
		dur, parseErr := parseFlexibleDurationField(*aux.ToolSyncInterval, "tool_sync_interval")
		if parseErr != nil {
			return parseErr
		}
		c.ToolSyncInterval = dur
		return nil
	}

	auxStr := &struct {
		ToolSyncInterval *string `json:"tool_sync_interval,omitempty"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, auxStr); err != nil {
		return err
	}
	if auxStr.ToolSyncInterval == nil {
		return nil
	}
	dur, err := parseFlexibleDurationField(*auxStr.ToolSyncInterval, "tool_sync_interval")
	if err != nil {
		return err
	}
	c.ToolSyncInterval = dur
	return nil
}

func (l MCPCodeModeLimits) Validate() error {
	for _, field := range []struct {
		name  string
		value int
	}{
		{"max_source_bytes", l.MaxSourceBytes},
		{"max_steps", l.MaxSteps},
		{"max_memory_bytes", l.MaxMemoryBytes},
		{"max_log_bytes", l.MaxLogBytes},
		{"max_tool_calls", l.MaxToolCalls},
		{"max_value_bytes", l.MaxValueBytes},
		{"max_nesting_depth", l.MaxNestingDepth},
	} {
		if field.value < 0 {
			return fmt.Errorf("code mode limit %s must not be negative", field.name)
		}
	}
	if l.MaxValueBytes != 0 && l.MaxValueBytes < MinCodeModeValueBytes {
		return fmt.Errorf("code mode limit max_value_bytes must be at least %d", MinCodeModeValueBytes)
	}
	if l.MaxNestingDepth > MaxCodeModeNestingDepth {
		return fmt.Errorf("code mode limit max_nesting_depth must be at most %d", MaxCodeModeNestingDepth)
	}
	return nil
}

func (c *MCPToolManagerConfig) UnmarshalJSON(data []byte) error {

	type alias MCPToolManagerConfig
	aux := &struct {
		ToolExecutionTimeout *json.RawMessage `json:"tool_execution_timeout,omitempty"`
		*alias
	}{alias: (*alias)(c)}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if aux.ToolExecutionTimeout == nil {
		return nil
	}

	raw := *aux.ToolExecutionTimeout

	if len(raw) > 0 && raw[0] == '"' {
		return json.Unmarshal(raw, &c.ToolExecutionTimeout)
	}

	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return fmt.Errorf("invalid tool_execution_timeout: expected a duration string (e.g. \"30s\") or integer seconds: %w", err)
	}
	c.ToolExecutionTimeout = Duration(time.Duration(n) * time.Second)
	return nil
}

const (
	DefaultMaxAgentDepth        = 10
	DefaultToolExecutionTimeout = 30 * time.Second

	DefaultMaxInstructionsPerClient = 4096
	DefaultMaxInstructionsTotal     = 16384

	DefaultCodeModeMaxSourceBytes  = 64 << 10
	DefaultCodeModeMaxSteps        = 1_000_000
	DefaultCodeModeMaxMemoryBytes  = 64 << 20
	DefaultCodeModeMaxLogBytes     = 64 << 10
	DefaultCodeModeMaxToolCalls    = 64
	DefaultCodeModeMaxValueBytes   = 1 << 20
	DefaultCodeModeMaxNestingDepth = 64

	MinCodeModeValueBytes   = 1 << 10
	MaxCodeModeNestingDepth = 1000
)

func (c *MCPClientConfig) UnmarshalJSON(data []byte) error {
	type alias MCPClientConfig
	aux := &struct {
		ToolSyncInterval      *json.Number     `json:"tool_sync_interval,omitempty"`
		ToolExecutionTimeout  *json.RawMessage `json:"tool_execution_timeout,omitempty"`
		AllowByDefault        *bool            `json:"allow_by_default,omitempty"`
		AllowOnAllVirtualKeys *bool            `json:"allow_on_all_virtual_keys,omitempty"`
		*alias
	}{alias: (*alias)(c)}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(aux); err == nil {
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return errors.New("trailing JSON data")
		}
		if aux.ToolSyncInterval != nil {
			dur, parseErr := parseFlexibleDurationField(*aux.ToolSyncInterval, "tool_sync_interval")
			if parseErr != nil {
				return parseErr
			}
			c.ToolSyncInterval = dur
		}
		if aux.ToolExecutionTimeout != nil {
			dur, err := parseToolExecutionTimeoutField(*aux.ToolExecutionTimeout)
			if err != nil {
				return err
			}
			c.ToolExecutionTimeout = dur
		}
		c.AllowByDefault = ResolveAllowByDefault(aux.AllowByDefault, aux.AllowOnAllVirtualKeys)
		return nil
	}

	auxStr := &struct {
		ToolSyncInterval      *string          `json:"tool_sync_interval,omitempty"`
		ToolExecutionTimeout  *json.RawMessage `json:"tool_execution_timeout,omitempty"`
		AllowByDefault        *bool            `json:"allow_by_default,omitempty"`
		AllowOnAllVirtualKeys *bool            `json:"allow_on_all_virtual_keys,omitempty"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, auxStr); err != nil {
		return err
	}
	if auxStr.ToolSyncInterval != nil {
		dur, err := parseFlexibleDurationField(*auxStr.ToolSyncInterval, "tool_sync_interval")
		if err != nil {
			return err
		}
		c.ToolSyncInterval = dur
	}
	if auxStr.ToolExecutionTimeout != nil {
		dur, err := parseToolExecutionTimeoutField(*auxStr.ToolExecutionTimeout)
		if err != nil {
			return err
		}
		c.ToolExecutionTimeout = dur
	}
	c.AllowByDefault = ResolveAllowByDefault(auxStr.AllowByDefault, auxStr.AllowOnAllVirtualKeys)
	return nil
}

func ResolveAllowByDefault(allowByDefault, allowOnAllVirtualKeys *bool) bool {
	if allowByDefault != nil {
		return *allowByDefault
	}
	if allowOnAllVirtualKeys != nil {
		return *allowOnAllVirtualKeys
	}
	return false
}

func parseToolExecutionTimeoutField(raw json.RawMessage) (time.Duration, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, fmt.Errorf("invalid tool_execution_timeout: %w", err)
		}
		dur, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid tool_execution_timeout %q: %w", s, err)
		}
		if dur < 0 {
			return 0, fmt.Errorf("invalid tool_execution_timeout: value must be >= 0, got %v", dur)
		}
		return dur, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("invalid tool_execution_timeout: expected a duration string (e.g. \"30s\") or integer seconds: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid tool_execution_timeout: value must be >= 0, got %d", n)
	}
	const maxTimeoutSeconds = math.MaxInt64 / int64(time.Second)
	if n > maxTimeoutSeconds {
		return 0, fmt.Errorf("invalid tool_execution_timeout: value %d seconds overflows duration (max %d)", n, maxTimeoutSeconds)
	}
	return time.Duration(n) * time.Second, nil
}

func (c MCPClientConfig) MarshalJSON() ([]byte, error) {
	type alias MCPClientConfig
	type shadow struct {
		ToolExecutionTimeout  string `json:"tool_execution_timeout,omitempty"`
		AllowOnAllVirtualKeys bool   `json:"allow_on_all_virtual_keys"`
		*alias
	}
	s := shadow{alias: (*alias)(&c), AllowOnAllVirtualKeys: c.AllowByDefault}
	if c.ToolExecutionTimeout > 0 {
		s.ToolExecutionTimeout = c.ToolExecutionTimeout.String()
	}
	return json.Marshal(s)
}

func parseFlexibleDurationField(v any, fieldName string) (time.Duration, error) {
	switch t := v.(type) {
	case string:
		d, err := time.ParseDuration(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("invalid %s duration %q: %w", fieldName, t, err)
		}
		return d, nil
	case json.Number:
		raw := strings.TrimSpace(t.String())
		if raw == "" {
			return 0, fmt.Errorf("invalid %s: empty numeric value", fieldName)
		}
		if strings.Contains(raw, ".") {
			return 0, fmt.Errorf("invalid %s value %q: fractional numeric values are not allowed; use an integer nanosecond value or a duration string like \"10m\"", fieldName, raw)
		}

		const maxSafeJSONInt int64 = 9007199254740991
		const minSafeJSONInt int64 = -9007199254740991

		var ns int64
		if strings.ContainsAny(raw, "eE") {
			rat := new(big.Rat)
			if _, ok := rat.SetString(raw); !ok {
				return 0, fmt.Errorf("invalid %s value %q: expected an integer nanosecond value", fieldName, raw)
			}
			if rat.Denom().Cmp(big.NewInt(1)) != 0 {
				return 0, fmt.Errorf("invalid %s value %q: fractional numeric values are not allowed; use an integer nanosecond value or a duration string like \"10m\"", fieldName, raw)
			}
			if !rat.Num().IsInt64() {
				return 0, fmt.Errorf("invalid %s value %q: out of int64 range for nanoseconds", fieldName, raw)
			}
			ns = rat.Num().Int64()
		} else {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid %s value %q: expected an integer nanosecond value", fieldName, raw)
			}
			ns = parsed
		}

		if ns < minSafeJSONInt || ns > maxSafeJSONInt {
			return 0, fmt.Errorf("invalid %s value %q: exceeds safe integer range", fieldName, raw)
		}
		return time.Duration(ns), nil
	default:
		return 0, fmt.Errorf("invalid %s type %T: expected duration string (e.g. \"10m\") or number", fieldName, v)
	}
}

func (c *MCPOpenAPIConfig) Clone() *MCPOpenAPIConfig {
	if c == nil {
		return nil
	}
	out := *c
	out.SpecURL = cloneStringPtr(c.SpecURL)
	out.SpecFile = cloneStringPtr(c.SpecFile)
	out.BaseURL = cloneStringPtr(c.BaseURL)
	if c.SecurityCredentials != nil {
		out.SecurityCredentials = make(map[string]MCPOpenAPICredential, len(c.SecurityCredentials))
		for name, cred := range c.SecurityCredentials {
			out.SecurityCredentials[name] = MCPOpenAPICredential{
				Value:    cloneSecretVarPtr(cred.Value),
				Username: cloneSecretVarPtr(cred.Username),
				Password: cloneSecretVarPtr(cred.Password),
			}
		}
	}
	return &out
}

func cloneStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func cloneSecretVarPtr(s *SecretVar) *SecretVar {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func (t *MCPTLSConfig) Clone() *MCPTLSConfig {
	if t == nil {
		return nil
	}
	return &MCPTLSConfig{InsecureSkipVerify: t.InsecureSkipVerify, CACertPEM: cloneSecretVarPtr(t.CACertPEM)}
}
