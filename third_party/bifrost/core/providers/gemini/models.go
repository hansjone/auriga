// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func (response *GeminiListModelsResponse) ToBifrostListModelsResponse(providerKey schemas.ModelProvider, allowedModels schemas.WhiteList, blacklistedModels schemas.BlackList, aliases schemas.KeyAliases, unfiltered bool) *schemas.BifrostListModelsResponse {
	if response == nil {
		return nil
	}

	bifrostResponse := &schemas.BifrostListModelsResponse{
		Data: make([]schemas.Model, 0, len(response.Models)),
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

	for _, model := range response.Models {
		contextLength := model.InputTokenLimit + model.OutputTokenLimit

		modelName := strings.TrimPrefix(model.Name, "models/")

		for _, result := range pipeline.FilterModel(modelName) {
			entry := schemas.Model{
				ID:               string(providerKey) + "/" + result.ResolvedID,
				Name:             schemas.Ptr(model.DisplayName),
				Description:      schemas.Ptr(model.Description),
				ContextLength:    schemas.Ptr(int(contextLength)),
				MaxInputTokens:   schemas.Ptr(model.InputTokenLimit),
				MaxOutputTokens:  schemas.Ptr(model.OutputTokenLimit),
				SupportedMethods: model.SupportedGenerationMethods,
			}
			if result.AliasValue != "" {
				entry.Alias = schemas.Ptr(result.AliasValue)
			}
			bifrostResponse.Data = append(bifrostResponse.Data, entry)
			included[strings.ToLower(result.ResolvedID)] = true
		}
	}

	bifrostResponse.Data = append(bifrostResponse.Data,
		pipeline.BackfillModels(included)...)

	return bifrostResponse
}

func (model *GeminiModel) ToBifrostModelRetrieveResponse(providerKey schemas.ModelProvider) *schemas.BifrostModelRetrieveResponse {
	if model == nil {
		return nil
	}

	return &schemas.BifrostModelRetrieveResponse{
		Model: schemas.Model{
			ID:               string(providerKey) + "/" + strings.TrimPrefix(model.Name, "models/"),
			Name:             schemas.Ptr(model.DisplayName),
			Description:      schemas.Ptr(model.Description),
			ContextLength:    schemas.Ptr(model.InputTokenLimit),
			MaxInputTokens:   schemas.Ptr(model.InputTokenLimit),
			MaxOutputTokens:  schemas.Ptr(model.OutputTokenLimit),
			SupportedMethods: model.SupportedGenerationMethods,
		},
	}
}
