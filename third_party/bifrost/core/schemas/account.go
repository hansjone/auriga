package schemas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/bytedance/sonic"
)

type KeyStatusType string
type SGLKeyConfig struct {
	URL SecretVar `json:"url"`
}
type Key struct {
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	Value                  SecretVar               `json:"value"`
	Models                 WhiteList               `json:"models"`
	BlacklistedModels      BlackList               `json:"blacklisted_models"`
	Weight                 float64                 `json:"weight"`
	Aliases                KeyAliases              `json:"aliases,omitempty"`
	AzureKeyConfig         *AzureKeyConfig         `json:"azure_key_config,omitempty"`
	VertexKeyConfig        *VertexKeyConfig        `json:"vertex_key_config,omitempty"`
	BedrockKeyConfig       *BedrockKeyConfig       `json:"bedrock_key_config,omitempty"`
	BedrockMantleKeyConfig *BedrockMantleKeyConfig `json:"bedrock_mantle_key_config,omitempty"`
	SageMakerKeyConfig     *SageMakerKeyConfig     `json:"sagemaker_key_config,omitempty"`
	VLLMKeyConfig          *VLLMKeyConfig          `json:"vllm_key_config,omitempty"`
	ReplicateKeyConfig     *ReplicateKeyConfig     `json:"replicate_key_config,omitempty"`
	OllamaKeyConfig        *OllamaKeyConfig        `json:"ollama_key_config,omitempty"`
	SGLKeyConfig           *SGLKeyConfig           `json:"sgl_key_config,omitempty"`
	DatabricksKeyConfig    *DatabricksKeyConfig    `json:"databricks_key_config,omitempty"`
	CloudflareKeyConfig    *CloudflareKeyConfig    `json:"cloudflare_key_config,omitempty"`
	OAuthKeyConfig         *OAuthKeyConfig         `json:"oauth_key_config,omitempty"`
	Enabled                *bool                   `json:"enabled,omitempty"`
	UseForBatchAPI         *bool                   `json:"use_for_batch_api,omitempty"`
	UseAnthropicEndpoints  *bool                   `json:"use_anthropic_endpoints,omitempty"`
	UseOpenAIEndpoints     *bool                   `json:"use_openai_endpoints,omitempty"`
	ConfigHash             string                  `json:"config_hash,omitempty"`
	Status                 KeyStatusType           `json:"status,omitempty"`
	Description            string                  `json:"description,omitempty"`
}
type SageMakerEndpoints struct {
	Runtime *SecretVar `json:"runtime,omitempty"`
}
type VertexKeyConfig struct {
	ProjectID       SecretVar `json:"project_id"`
	ProjectNumber   SecretVar `json:"project_number"`
	Region          SecretVar `json:"region"`
	AuthCredentials SecretVar `json:"auth_credentials"`

	ForceSingleRegion bool `json:"force_single_region,omitempty"`

	UseSessionRouting bool `json:"use_session_routing,omitempty"`

	AWSWorkloadIdentity *VertexAWSWorkloadIdentityConfig `json:"aws_workload_identity,omitempty"`
}
type DatabricksKeyConfig struct {
	WorkspaceURL       SecretVar           `json:"workspace_url"`
	APIFormat          DatabricksAPIFormat `json:"api_format,omitempty"`
	ClientID           *SecretVar          `json:"client_id,omitempty"`
	ClientSecret       *SecretVar          `json:"client_secret,omitempty"`
	ForwardGatewayTags bool                `json:"forward_gateway_tags,omitempty"`
}
type BedrockAliasCfg struct {
	InferenceProfileARN *SecretVar `json:"inference_profile_arn,omitempty"`
}
type OAuthGrantType string
type AzureAliasCfg struct {
	APIVersion         *string    `json:"api_version,omitempty"`
	AnthropicVersion   *string    `json:"anthropic_version,omitempty"`
	Endpoint           *SecretVar `json:"endpoint,omitempty"`
	UseSpeechEndpoints *bool      `json:"use_speech_endpoints,omitempty"`

	UseVoiceLiveEndpoints *bool `json:"use_voice_live_endpoints,omitempty"`
}
type BedrockEndpoints struct {
	Runtime      *SecretVar `json:"runtime,omitempty"`
	ControlPlane *SecretVar `json:"control_plane,omitempty"`
	Mantle       *SecretVar `json:"mantle,omitempty"`
	AgentRuntime *SecretVar `json:"agent_runtime,omitempty"`
	S3           *SecretVar `json:"s3,omitempty"`
}
type ModelFamily string
type BatchS3Config struct {
	Buckets []S3BucketConfig `json:"buckets,omitempty"`
}
type AzureKeyConfig struct {
	Endpoint SecretVar `json:"endpoint"`

	ClientID     *SecretVar `json:"client_id,omitempty"`
	ClientSecret *SecretVar `json:"client_secret,omitempty"`
	TenantID     *SecretVar `json:"tenant_id,omitempty"`
	Scopes       []string   `json:"scopes,omitempty"`

	UseSpeechEndpoints bool `json:"use_speech_endpoints,omitempty"`

	UseVoiceLiveEndpoints bool `json:"use_voice_live_endpoints,omitempty"`
}
type ReplicateKeyConfig struct {
	UseDeploymentsEndpoint bool `json:"use_deployments_endpoint"`
}
type VertexEndpoint struct {
	EndpointID string               `json:"endpoint_id"`
	Format     VertexEndpointFormat `json:"format"`
	Method     VertexEndpointMethod `json:"method,omitempty"`
}
type BedrockMantleKeyConfig struct {
	AccessKey    SecretVar  `json:"access_key,omitempty"`
	SecretKey    SecretVar  `json:"secret_key,omitempty"`
	SessionToken *SecretVar `json:"session_token,omitempty"`
	Region       *SecretVar `json:"region,omitempty"`

	RoleARN         *SecretVar `json:"role_arn,omitempty"`
	ExternalID      *SecretVar `json:"external_id,omitempty"`
	RoleSessionName *SecretVar `json:"session_name,omitempty"`

	ProjectID *SecretVar `json:"project_id,omitempty"`

	Endpoints *BedrockEndpoints `json:"endpoints,omitempty"`
}
type SageMakerKeyConfig struct {
	AccessKey       SecretVar  `json:"access_key,omitempty"`
	SecretKey       SecretVar  `json:"secret_key,omitempty"`
	SessionToken    *SecretVar `json:"session_token,omitempty"`
	Region          *SecretVar `json:"region,omitempty"`
	RoleARN         *SecretVar `json:"role_arn,omitempty"`
	ExternalID      *SecretVar `json:"external_id,omitempty"`
	RoleSessionName *SecretVar `json:"session_name,omitempty"`

	Endpoints *SageMakerEndpoints `json:"endpoints,omitempty"`
}
type VLLMKeyConfig struct {
	URL       SecretVar `json:"url"`
	ModelName string    `json:"model_name"`
}
type ReplicateAliasCfg struct {
	UseDeploymentsEndpoint *bool `json:"use_deployments_endpoint,omitempty"`
}
type VertexAliasCfg struct {
	ProjectID         *SecretVar      `json:"-"`
	ProjectNumber     *SecretVar      `json:"project_number,omitempty"`
	ForceSingleRegion *bool           `json:"force_single_region,omitempty"`
	VertexEndpoint    *VertexEndpoint `json:"vertex_endpoint,omitempty"`
}
type KeyAliases map[string]AliasConfig
type OllamaKeyConfig struct {
	URL SecretVar `json:"url"`
}
type BlackList []string
type S3BucketConfig struct {
	BucketName string `json:"bucket_name"`
	Prefix     string `json:"prefix,omitempty"`
	IsDefault  bool   `json:"is_default,omitempty"`
}

var OAuthSigningAlgorithms = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"}

type ResolvedAlias struct {
	Key    string
	Config *AliasConfig
}
type OAuthAuthStyle string
type DatabricksAPIFormat string
type OAuthKeyConfig struct {
	GrantType                OAuthGrantType    `json:"grant_type"`
	TokenURL                 SecretVar         `json:"token_url"`
	ClientID                 *SecretVar        `json:"client_id,omitempty"`
	ClientSecret             *SecretVar        `json:"client_secret,omitempty"`
	PrivateKey               *SecretVar        `json:"private_key,omitempty"`
	Scopes                   []string          `json:"scopes,omitempty"`
	Audience                 string            `json:"audience,omitempty"`
	Issuer                   string            `json:"issuer,omitempty"`
	Subject                  string            `json:"subject,omitempty"`
	KeyID                    string            `json:"key_id,omitempty"`
	SigningAlgorithm         string            `json:"signing_algorithm,omitempty"`
	AuthStyle                OAuthAuthStyle    `json:"auth_style,omitempty"`
	ExtraParams              map[string]string `json:"extra_params,omitempty"`
	AssertionLifetimeSeconds int               `json:"assertion_lifetime_seconds,omitempty"`
}
type WhiteList []string
type BedrockKeyConfig struct {
	AccessKey    SecretVar  `json:"access_key,omitempty"`
	SecretKey    SecretVar  `json:"secret_key,omitempty"`
	SessionToken *SecretVar `json:"session_token,omitempty"`
	Region       *SecretVar `json:"region,omitempty"`
	ARN          *SecretVar `json:"arn,omitempty"`

	RoleARN         *SecretVar `json:"role_arn,omitempty"`
	ExternalID      *SecretVar `json:"external_id,omitempty"`
	RoleSessionName *SecretVar `json:"session_name,omitempty"`

	BatchRoleARN *SecretVar `json:"batch_role_arn,omitempty"`

	ProjectID *SecretVar `json:"project_id,omitempty"`

	BatchS3Config *BatchS3Config `json:"batch_s3_config,omitempty"`

	Endpoints *BedrockEndpoints `json:"endpoints,omitempty"`
}
type Account interface {
	GetConfiguredProviders() ([]ModelProvider, error)

	GetKeysForProvider(ctx context.Context, providerKey ModelProvider) ([]Key, error)

	GetConfigForProvider(providerKey ModelProvider) (*ProviderConfig, error)
}
type VertexEndpointFormat string
type AliasConfig struct {
	ModelID     string       `json:"model_id"`
	ModelName   *string      `json:"model_name,omitempty"`
	ModelFamily *ModelFamily `json:"model_family,omitempty"`
	Description string       `json:"description,omitempty"`
	Region      *SecretVar   `json:"region,omitempty"`

	ProjectID             *SecretVar `json:"project_id,omitempty"`
	UseAnthropicEndpoints *bool      `json:"use_anthropic_endpoints,omitempty"`
	UseOpenAIEndpoints    *bool      `json:"use_openai_endpoints,omitempty"`

	*AzureAliasCfg
	*VertexAliasCfg
	*BedrockAliasCfg
	*ReplicateAliasCfg
}
type VertexAWSWorkloadIdentityConfig struct {
	Audience             SecretVar  `json:"audience"`
	ServiceAccountEmail  *SecretVar `json:"service_account_email,omitempty"`
	TokenLifetimeSeconds int        `json:"token_lifetime_seconds,omitempty"`
	AWSRegion            *SecretVar `json:"aws_region,omitempty"`
	AWSRoleARN           *SecretVar `json:"aws_role_arn,omitempty"`
}
type VertexEndpointMethod string
type CloudflareKeyConfig struct {
	AccountID SecretVar `json:"account_id"`
}

const (
	KeyStatusSuccess          KeyStatusType = "success"
	KeyStatusListModelsFailed KeyStatusType = "list_models_failed"
)

func (wl WhiteList) Contains(value string) bool {
	return slices.ContainsFunc(wl, func(s string) bool {
		return MatchEntry(s, value)
	})
}

func (wl WhiteList) IsAllowed(value string) bool {
	return wl.IsUnrestricted() || wl.Contains(value)
}

func (wl WhiteList) IsEmpty() bool {
	return len(wl) == 0
}

func (wl WhiteList) IsUnrestricted() bool {
	return len(wl) == 1 && wl[0] == "*"
}

func (wl WhiteList) IsRestricted() bool {
	return !wl.IsUnrestricted()
}

func (wl WhiteList) Validate() error {
	if err := ValidateRegexEntries(wl); err != nil {
		return err
	}
	if slices.Contains(wl, "*") && len(wl) > 1 {
		return fmt.Errorf("wildcard '*' cannot be used with other values in the whitelist")
	}
	seen := make(map[string]struct{}, len(wl))
	for _, v := range wl {
		normalized := strings.ToLower(v)
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate value '%s' in whitelist", v)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func (bl BlackList) Contains(value string) bool {
	return slices.ContainsFunc(bl, func(s string) bool {
		return MatchEntry(s, value)
	})
}

func (bl BlackList) IsBlocked(value string) bool {
	return bl.IsBlockAll() || bl.Contains(value)
}

func (bl BlackList) IsEmpty() bool {
	return len(bl) == 0
}

func (bl BlackList) IsBlockAll() bool {
	return len(bl) == 1 && bl[0] == "*"
}

func (bl BlackList) Validate() error {
	if err := ValidateRegexEntries(bl); err != nil {
		return err
	}
	if slices.Contains(bl, "*") && len(bl) > 1 {
		return fmt.Errorf("wildcard '*' cannot be used with other values in the blacklist")
	}
	seen := make(map[string]struct{}, len(bl))
	for _, v := range bl {
		normalized := strings.ToLower(v)
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate value '%s' in blacklist", v)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

const (
	ModelFamilyAnthropic ModelFamily = "anthropic"
	ModelFamilyOpenAI    ModelFamily = "openai"
	ModelFamilyMistral   ModelFamily = "mistral"
	ModelFamilyCohere    ModelFamily = "cohere"
	ModelFamilyGemini    ModelFamily = "gemini"
	ModelFamilyGemma     ModelFamily = "gemma"
	ModelFamilyLlama     ModelFamily = "llama"
	ModelFamilyImagen    ModelFamily = "imagen"
	ModelFamilyVeo       ModelFamily = "veo"
	ModelFamilyNova      ModelFamily = "nova"
	ModelFamilyTitan     ModelFamily = "titan"
)

func (mf *ModelFamily) IsValid() bool {
	if mf == nil {
		return false
	}
	switch *mf {
	case ModelFamilyAnthropic, ModelFamilyOpenAI, ModelFamilyMistral,
		ModelFamilyCohere, ModelFamilyGemini, ModelFamilyGemma,
		ModelFamilyLlama, ModelFamilyImagen, ModelFamilyVeo,
		ModelFamilyNova, ModelFamilyTitan:
		return true
	}
	return false
}

const (
	VertexEndpointOpenAIChat VertexEndpointFormat = "openai_chat"
)

const (
	VertexEndpointRawPredict      VertexEndpointMethod = "raw_predict"
	VertexEndpointChatCompletions VertexEndpointMethod = "chat_completions"
)

func (e *VertexEndpoint) Validate() error {
	if e == nil {
		return nil
	}
	if e.EndpointID == "" || strings.IndexFunc(e.EndpointID, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-')
	}) >= 0 {
		return fmt.Errorf("vertex_endpoint.endpoint_id must contain only letters, digits, underscores or hyphens")
	}
	switch e.Format {
	case VertexEndpointOpenAIChat:
		switch e.Method {
		case "", VertexEndpointRawPredict, VertexEndpointChatCompletions:
			return nil
		default:
			return fmt.Errorf("invalid vertex_endpoint.method %q for format %q: expected raw_predict or chat_completions", e.Method, e.Format)
		}
	default:
		return fmt.Errorf("invalid vertex_endpoint.format %q: expected openai_chat", e.Format)
	}
}

func (ac AliasConfig) isLegacyShape() bool {
	return ac.ModelID != "" &&
		ac.ModelName == nil &&
		ac.ModelFamily == nil &&
		ac.Description == "" &&
		ac.Region == nil &&
		ac.ProjectID == nil &&
		ac.UseAnthropicEndpoints == nil &&
		ac.UseOpenAIEndpoints == nil &&
		ac.AzureAliasCfg == nil &&
		ac.VertexAliasCfg == nil &&
		ac.BedrockAliasCfg == nil &&
		ac.ReplicateAliasCfg == nil
}

func (ac AliasConfig) MarshalJSON() ([]byte, error) {

	if ac.ProjectID == nil && ac.VertexAliasCfg != nil && ac.VertexAliasCfg.ProjectID != nil {
		ac.ProjectID = ac.VertexAliasCfg.ProjectID
	}
	if ac.isLegacyShape() {
		return Marshal(ac.ModelID)
	}
	type aliasConfigJSON AliasConfig
	return Marshal(aliasConfigJSON(ac))
}

func (ka KeyAliases) Validate(providerKey ModelProvider) error {
	seen := make(map[string]struct{}, len(ka))
	for from, ac := range ka {
		if strings.TrimSpace(from) == "" {
			return fmt.Errorf("alias source cannot be empty")
		}
		if strings.TrimSpace(ac.ModelID) == "" {
			return fmt.Errorf("alias %q: model_id cannot be empty", from)
		}
		if strings.TrimSpace(from) != from {
			return fmt.Errorf("alias source %q cannot have leading or trailing whitespace", from)
		}
		if strings.TrimSpace(ac.ModelID) != ac.ModelID {
			return fmt.Errorf("alias %q: model_id cannot have leading or trailing whitespace", from)
		}
		if ac.ModelName != nil && strings.TrimSpace(*ac.ModelName) != *ac.ModelName {
			return fmt.Errorf("alias %q: model_name cannot have leading or trailing whitespace", from)
		}
		if ac.ModelFamily != nil && !ac.ModelFamily.IsValid() {
			return fmt.Errorf("alias %q: invalid model_family %q", from, *ac.ModelFamily)
		}
		if ac.AzureAliasCfg != nil && providerKey != Azure {
			return fmt.Errorf("alias %q: azure sub-config is only valid on Azure keys (got provider %q)", from, providerKey)
		}
		if ac.VertexAliasCfg != nil && providerKey != Vertex {
			return fmt.Errorf("alias %q: vertex sub-config is only valid on Vertex keys (got provider %q)", from, providerKey)
		}
		if ac.VertexAliasCfg != nil {
			if err := ac.VertexEndpoint.Validate(); err != nil {
				return fmt.Errorf("alias %q: %w", from, err)
			}
		}
		if ac.BedrockAliasCfg != nil && providerKey != Bedrock {
			return fmt.Errorf("alias %q: bedrock sub-config is only valid on Bedrock keys (got provider %q)", from, providerKey)
		}
		if ac.ReplicateAliasCfg != nil && providerKey != Replicate {
			return fmt.Errorf("alias %q: replicate sub-config is only valid on Replicate keys (got provider %q)", from, providerKey)
		}
		normalized := strings.ToLower(from)
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate alias source %q (case-insensitive)", from)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func (ka KeyAliases) Resolve(model string) string {
	if ac := ka.ResolveConfig(model); ac != nil {
		return ac.ModelID
	}
	return model
}

func GetResolvedAlias(ctx *BifrostContext) *ResolvedAlias {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(BifrostContextKeyResolvedAlias)
	if v == nil {
		return nil
	}
	ra, _ := v.(*ResolvedAlias)
	return ra
}

func GetVertexEndpoint(ctx *BifrostContext) *VertexEndpoint {
	if ra := GetResolvedAlias(ctx); ra != nil && ra.Config != nil && ra.Config.VertexAliasCfg != nil {
		return ra.Config.VertexEndpoint
	}
	return nil
}

func ResolveFamily(ctx *BifrostContext, fallbackModel string) ModelFamily {
	ra := GetResolvedAlias(ctx)
	var candidates []string
	if ra != nil && ra.Config != nil {
		if ra.Config.ModelFamily != nil && *ra.Config.ModelFamily != "" {
			return *ra.Config.ModelFamily
		}
		if ra.Config.ModelName != nil {
			candidates = append(candidates, *ra.Config.ModelName)
		}
		candidates = append(candidates, ra.Config.ModelID, ra.Key)
	} else {
		candidates = append(candidates, fallbackModel)
	}
	for _, s := range candidates {
		switch {
		case IsAnthropicModel(s):
			return ModelFamilyAnthropic
		case IsOpenAIModel(s):
			return ModelFamilyOpenAI
		case IsMistralModel(s):
			return ModelFamilyMistral

		case IsImagenModel(s):
			return ModelFamilyImagen
		case IsVeoModel(s):
			return ModelFamilyVeo
		case IsGeminiModel(s):
			return ModelFamilyGemini
		case IsGemmaModel(s):
			return ModelFamilyGemma
		case IsLlamaModel(s):
			return ModelFamilyLlama
		case IsNovaModel(s):
			return ModelFamilyNova
		case IsTitanModel(s):
			return ModelFamilyTitan
		case IsCohereModel(s):
			return ModelFamilyCohere
		}
	}
	return ""
}

func ResolveCanonicalModel(ctx *BifrostContext, fallbackModel string) string {
	if ra := GetResolvedAlias(ctx); ra != nil && ra.Config != nil {
		if ra.Config.ModelName != nil && *ra.Config.ModelName != "" {
			return *ra.Config.ModelName
		}
		if ra.Config.ModelID != "" {
			return ra.Config.ModelID
		}
	}
	return fallbackModel
}

func ResolveBaseProvider(ctx *BifrostContext, provider ModelProvider) ModelProvider {
	if ctx != nil {
		if base, ok := ctx.Value(BifrostContextKeyBaseProviderType).(ModelProvider); ok && base != "" {
			return base
		}
	}
	return provider
}

func ChatReasoningModeRequiresResponses(base ModelProvider) bool {
	return base == OpenAI || base == Azure
}

func IsAnthropicModelFamily(ctx *BifrostContext, model string) bool {
	return ResolveFamily(ctx, model) == ModelFamilyAnthropic
}

func IsGeminiModelFamily(ctx *BifrostContext, model string) bool {
	return ResolveFamily(ctx, model) == ModelFamilyGemini
}

func IsGemmaModelFamily(ctx *BifrostContext, model string) bool {
	return ResolveFamily(ctx, model) == ModelFamilyGemma
}

func IsImagenModelFamily(ctx *BifrostContext, model string) bool {
	return ResolveFamily(ctx, model) == ModelFamilyImagen
}

func IsVeoModelFamily(ctx *BifrostContext, model string) bool {
	return ResolveFamily(ctx, model) == ModelFamilyVeo
}

func BuildRoutingInfo(ctx *BifrostContext, attemptProvider ModelProvider, attemptModel string, attemptKey Key) RoutingInfo {
	info := RoutingInfo{
		Provider: attemptProvider,
		Model:    attemptModel,
		Key:      attemptKey.Name,
	}
	info.ApplyRequestRouting(ctx)
	if ra := GetResolvedAlias(ctx); ra != nil && ra.Config != nil {
		rka := &ResolvedKeyAlias{
			ModelID: ra.Config.ModelID,
		}
		if ra.Config.ModelName != nil {
			mn := *ra.Config.ModelName
			rka.ModelName = &mn
		}
		if ra.Config.ModelFamily != nil {
			f := *ra.Config.ModelFamily
			rka.ModelFamily = &f
		}
		info.ResolvedKeyAlias = rka
	}
	return info
}

func (ri *RoutingInfo) ApplyRequestRouting(ctx *BifrostContext) {
	if ri == nil || ctx == nil {
		return
	}
	if provider, ok := ctx.Value(BifrostContextKeyRequestedProvider).(ModelProvider); ok {
		ri.RequestedProvider = provider
	}
	if model, ok := ctx.Value(BifrostContextKeyRequestedModel).(string); ok {
		ri.RequestedModel = model
	}
}

func (ka KeyAliases) ResolveConfig(model string) *AliasConfig {
	if ka == nil {
		return nil
	}
	if ac, ok := ka[model]; ok {
		return &ac
	}
	for k, v := range ka {
		if strings.EqualFold(k, model) {
			return &v
		}
	}
	return nil
}

func (ka *KeyAliases) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*ka = nil
		return nil
	}
	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(KeyAliases, len(raw))
	for k, entry := range raw {
		entryTrim := bytes.TrimSpace(entry)
		if len(entryTrim) == 0 {
			return fmt.Errorf("alias %q: empty value", k)
		}
		switch entryTrim[0] {
		case '"':

			var modelID string
			if err := sonic.Unmarshal(entry, &modelID); err != nil {
				return fmt.Errorf("alias %q: %w", k, err)
			}
			result[k] = AliasConfig{ModelID: modelID}
		case '{':
			var ac AliasConfig
			if err := sonic.Unmarshal(entry, &ac); err != nil {
				return fmt.Errorf("alias %q: %w", k, err)
			}
			result[k] = ac
		default:
			return fmt.Errorf("alias %q: value must be a string (legacy) or object", k)
		}
	}
	*ka = result
	return nil
}

const (
	OAuthGrantClientCredentials OAuthGrantType = "client_credentials"

	OAuthGrantJWTBearer OAuthGrantType = "jwt_bearer"
)

const (
	OAuthAuthStyleHeader OAuthAuthStyle = "header"

	OAuthAuthStyleBody OAuthAuthStyle = "body"
)

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *OAuthKeyConfig) Validate() error {
	if c == nil {
		return nil
	}

	has := func(v *SecretVar) bool {
		return v != nil && (v.IsFromSecret() || strings.TrimSpace(v.GetValue()) != "")
	}
	if !has(&c.TokenURL) {
		return fmt.Errorf("oauth_key_config.token_url is required")
	}
	if !c.TokenURL.IsFromSecret() {

		u, err := url.Parse(strings.TrimSpace(c.TokenURL.GetValue()))
		if err != nil || !u.IsAbs() || u.Host == "" || !(u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
			return fmt.Errorf("oauth_key_config.token_url must be https (http is allowed for loopback hosts only)")
		}

		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("oauth_key_config.token_url must not carry userinfo, a query, or a fragment; send extra parameters through extra_params")
		}
	}
	switch c.GrantType {
	case OAuthGrantClientCredentials:
		if !has(c.ClientID) || !has(c.ClientSecret) {
			return fmt.Errorf("oauth_key_config.client_id and oauth_key_config.client_secret are required for the client_credentials grant")
		}
		switch c.AuthStyle {
		case "", OAuthAuthStyleHeader, OAuthAuthStyleBody:
		default:
			return fmt.Errorf("oauth_key_config.auth_style must be header or body")
		}
	case OAuthGrantJWTBearer:
		if !has(c.PrivateKey) {
			return fmt.Errorf("oauth_key_config.private_key is required for the jwt_bearer grant")
		}
		if strings.TrimSpace(c.Issuer) == "" {
			return fmt.Errorf("oauth_key_config.issuer is required for the jwt_bearer grant")
		}
		if strings.TrimSpace(c.Audience) == "" {
			return fmt.Errorf("oauth_key_config.audience is required for the jwt_bearer grant")
		}
		if c.SigningAlgorithm != "" && !slices.Contains(OAuthSigningAlgorithms, c.SigningAlgorithm) {
			return fmt.Errorf("oauth_key_config.signing_algorithm must be one of %s", strings.Join(OAuthSigningAlgorithms, ", "))
		}
		if c.AssertionLifetimeSeconds < 0 || c.AssertionLifetimeSeconds > 3600 {
			return fmt.Errorf("oauth_key_config.assertion_lifetime_seconds must be between 0 (default) and 3600")
		}
	case "":
		return fmt.Errorf("oauth_key_config.grant_type is required")
	default:
		return fmt.Errorf("oauth_key_config.grant_type must be client_credentials or jwt_bearer")
	}
	return nil
}
