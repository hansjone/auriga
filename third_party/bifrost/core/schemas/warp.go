package schemas

import "time"

type WarpModel struct {
	Provider ModelProvider `json:"provider"`
	Model    string        `json:"model"`
	APIKeyID string        `json:"api_key_id,omitempty"`
}
type WarpConfig struct {
	Enabled bool `json:"enabled"`

	Provider ModelProvider `json:"provider"`
	Model    string        `json:"model"`

	APIKeyID string `json:"api_key_id,omitempty"`

	AdditionalModels []WarpModel `json:"additional_models,omitempty"`

	MaxIterations int `json:"max_iterations,omitempty"`

	RequestTimeoutSeconds int `json:"request_timeout_seconds,omitempty"`

	HistoryRetentionDays int `json:"history_retention_days,omitempty"`

	SystemPromptSuffix string `json:"system_prompt_suffix,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	EmbeddingProvider       ModelProvider `json:"embedding_provider"`
	EmbeddingModel          string        `json:"embedding_model"`
	EmbeddingAPIKeyID       string        `json:"embedding_api_key_id,omitempty"`
	EmbeddingDimension      int           `json:"embedding_dimension"`
	LogVectorStoreNamespace string        `json:"log_vector_store_namespace"`
	SemanticSearchThreshold float64       `json:"semantic_search_threshold"`
	SemanticSearchLimit     int           `json:"semantic_search_limit"`

	TopicDiscoveryMinRequests int `json:"topic_discovery_min_requests"`

	RetiredLogVectorStoreNamespaces []string `json:"-"`

	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (c *WarpConfig) Models() []WarpModel {
	if c == nil {
		return nil
	}
	models := make([]WarpModel, 0, 1+len(c.AdditionalModels))
	if c.Provider != "" && c.Model != "" {
		models = append(models, WarpModel{Provider: c.Provider, Model: c.Model, APIKeyID: c.APIKeyID})
	}
	return append(models, c.AdditionalModels...)
}
