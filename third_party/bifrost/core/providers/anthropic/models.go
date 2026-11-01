package anthropic

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func (response *AnthropicListModelsResponse) ToBifrostListModelsResponse(providerKey schemas.ModelProvider, allowedModels schemas.WhiteList, blacklistedModels schemas.BlackList, aliases schemas.KeyAliases, unfiltered bool) *schemas.BifrostListModelsResponse {
	if response == nil {
		return nil
	}

	bifrostResponse := &schemas.BifrostListModelsResponse{
		Data:    make([]schemas.Model, 0, len(response.Data)),
		FirstID: response.FirstID,
		LastID:  response.LastID,
		HasMore: schemas.Ptr(response.HasMore),
	}

	if response.HasMore && response.LastID != nil {
		bifrostResponse.NextPageToken = *response.LastID
	}

	pipeline := &providerUtils.ListModelsPipeline{
		AllowedModels:     allowedModels,
		BlacklistedModels: blacklistedModels,
		Aliases:           aliases,
		Unfiltered:        unfiltered,
		ProviderKey:       providerKey,
		MatchFns:          providerUtils.DefaultMatchFns(),
	}
	if pipeline.ShouldEarlyExit() {
		return bifrostResponse
	}

	included := make(map[string]bool)

	for _, model := range response.Data {
		for _, result := range pipeline.FilterModel(model.ID) {
			resolvedKey := strings.ToLower(result.ResolvedID)
			if included[resolvedKey] {
				continue
			}
			entry := schemas.Model{
				ID:              string(providerKey) + "/" + result.ResolvedID,
				Name:            schemas.Ptr(model.DisplayName),
				Created:         schemas.Ptr(model.CreatedAt.Unix()),
				MaxInputTokens:  model.MaxInputTokens,
				MaxOutputTokens: model.MaxTokens,
				ProviderExtra:   model.Capabilities,
			}
			if result.AliasValue != "" {
				entry.Alias = schemas.Ptr(result.AliasValue)
			}
			bifrostResponse.Data = append(bifrostResponse.Data, entry)
			included[resolvedKey] = true
		}
	}

	bifrostResponse.Data = append(bifrostResponse.Data,
		pipeline.BackfillModels(included)...)

	return bifrostResponse
}

func (model *AnthropicModel) ToBifrostModelRetrieveResponse(providerKey schemas.ModelProvider) *schemas.BifrostModelRetrieveResponse {
	if model == nil {
		return nil
	}

	return &schemas.BifrostModelRetrieveResponse{
		Model: schemas.Model{
			ID:              string(providerKey) + "/" + model.ID,
			Name:            schemas.Ptr(model.DisplayName),
			Created:         schemas.Ptr(model.CreatedAt.Unix()),
			MaxInputTokens:  model.MaxInputTokens,
			MaxOutputTokens: model.MaxTokens,
			ProviderExtra:   model.Capabilities,
		},
	}
}
