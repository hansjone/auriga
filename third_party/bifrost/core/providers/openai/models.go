// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func (response *OpenAIListModelsResponse) ToBifrostListModelsResponse(providerKey schemas.ModelProvider, allowedModels schemas.WhiteList, blacklistedModels schemas.BlackList, aliases schemas.KeyAliases, unfiltered bool) *schemas.BifrostListModelsResponse {
	if response == nil {
		return nil
	}

	bifrostResponse := &schemas.BifrostListModelsResponse{
		Data: make([]schemas.Model, 0, len(response.Data)),
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
			entry := schemas.Model{
				ID:            string(providerKey) + "/" + result.ResolvedID,
				Created:       model.Created,
				OwnedBy:       schemas.Ptr(model.OwnedBy),
				ContextLength: model.ContextWindow,
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

func (model *OpenAIModel) ToBifrostModelRetrieveResponse(providerKey schemas.ModelProvider) *schemas.BifrostModelRetrieveResponse {
	if model == nil {
		return nil
	}

	return &schemas.BifrostModelRetrieveResponse{
		Model: schemas.Model{
			ID:            string(providerKey) + "/" + model.ID,
			Created:       model.Created,
			OwnedBy:       schemas.Ptr(model.OwnedBy),
			ContextLength: model.ContextWindow,
			ShutdownDate:  model.ShutdownDate,
		},
	}
}
