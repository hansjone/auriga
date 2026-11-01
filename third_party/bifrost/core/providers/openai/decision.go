// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const openAIDecisionsPath = "/v1/decisions"

func ToOpenAIDecisionRequest(request *schemas.BifrostDecisionRequest) (*OpenAIDecisionRequest, error) {
	if request == nil {
		return nil, providerUtils.InvalidRequestErrorf("decision request is nil")
	}
	if len(request.Questions) == 0 {
		return nil, providerUtils.InvalidRequestErrorf("decision request requires at least one question")
	}
	if err := request.Input.Validate(); err != nil {
		return nil, providerUtils.InvalidRequestErrorf("%s", err.Error())
	}
	if request.Input.IsEmpty() {
		return nil, providerUtils.InvalidRequestErrorf("OpenAI decisions require an input, and this request has none")
	}
	if partType := unsupportedOpenAIPartType(request.Input); partType != "" {
		return nil, providerUtils.InvalidRequestErrorf("OpenAI decisions read text, JSON, image, output text, and refusal parts, not %q", partType)
	}

	input := request.Input
	if input.Structured != nil {
		text, err := providerUtils.MarshalSorted(input.Structured)
		if err != nil {
			return nil, providerUtils.InvalidRequestErrorf("decision input could not be encoded as text: %v", err)
		}
		input = schemas.DecisionInput{Text: schemas.Ptr(string(text))}
	}
	input.Messages = openAIMessageItems(openAITextParts(input.Messages))

	questions := make([]OpenAIDecisionQuestion, len(request.Questions))
	for i, question := range request.Questions {
		converted, err := toOpenAIDecisionQuestion(i, question)
		if err != nil {
			return nil, err
		}
		questions[i] = converted
	}

	return &OpenAIDecisionRequest{
		Model:              request.Model,
		Input:              input,
		Questions:          questions,
		SafetyIdentifier:   request.SafetyIdentifier,
		ExtraParams:        request.ExtraParams,
		PromptCacheKey:     request.PromptCacheKey,
		PromptCacheOptions: request.PromptCacheOptions,
	}, nil
}

func unsupportedOpenAIPartType(input schemas.DecisionInput) string {
	for _, message := range input.Messages {
		for _, part := range message.Content.Parts {
			switch {
			case part.Type == "":
				return "untyped"
			case !part.IsTextual() && part.Type != schemas.DecisionInputPartTypeImage:
				return part.Type
			}
		}
	}
	return ""
}

func openAIMessageItems(messages []schemas.DecisionInputMessage) []schemas.DecisionInputMessage {
	var converted []schemas.DecisionInputMessage
	for i, message := range messages {
		if (message.Type != nil && *message.Type != schemas.DecisionInputItemTypeMessage) || (message.ID == nil && message.Status == nil) {
			continue
		}
		if converted == nil {
			converted = slices.Clone(messages)
		}
		converted[i].ID, converted[i].Status = nil, nil
	}
	if converted == nil {
		return messages
	}
	return converted
}

func openAIInstructionParts(instructions *schemas.DecisionText) []interface{} {
	if instructions == nil {
		return nil
	}
	parts, ok := instructions.Structured.([]interface{})
	if !ok || len(parts) == 0 {
		return nil
	}
	for _, part := range parts {
		object, ok := part.(map[string]interface{})
		if !ok {
			return nil
		}
		switch object["type"] {
		case schemas.DecisionInputPartTypeText, schemas.DecisionInputPartTypeImage, schemas.DecisionInputPartTypeJSON:
		default:
			return nil
		}
	}
	return parts
}

func toOpenAIDecisionQuestion(index int, question schemas.DecisionQuestion) (OpenAIDecisionQuestion, error) {
	if !question.Type.IsQuestionType() {
		return OpenAIDecisionQuestion{}, providerUtils.InvalidRequestErrorf("question %d has unsupported type %q; expected predicate, choice, or score", index, question.Type)
	}
	converted := OpenAIDecisionQuestion{Type: question.Type, Name: question.Name}
	criteria := providerUtils.DecisionCriteriaText(question.Criteria)
	if parts := openAIInstructionParts(question.Instructions); parts != nil {
		if criteria = strings.TrimSpace(criteria); criteria != "" {
			parts = append(slices.Clip(parts), map[string]interface{}{"type": schemas.DecisionInputPartTypeText, "text": criteria})
		}
		converted.Instructions = &schemas.DecisionText{Structured: parts}
	} else {
		converted.Instructions = schemas.NewDecisionText(strings.TrimSpace(providerUtils.DecisionTextString(question.Instructions) + criteria))
	}

	switch question.Type {
	case schemas.DecisionTypeChoice:
		if len(question.Choices) == 0 {
			return OpenAIDecisionQuestion{}, providerUtils.InvalidRequestErrorf("choice question %d requires choices", index)
		}
		converted.Choices = make([]OpenAIDecisionChoice, len(question.Choices))
		for j, choice := range question.Choices {
			if _, err := choice.Key(); err != nil {
				return OpenAIDecisionQuestion{}, providerUtils.InvalidRequestErrorf("choice %d of question %d: %s", j, index, err.Error())
			}
			converted.Choices[j] = OpenAIDecisionChoice{Value: choice.Value, Description: openAIText(choice.Description)}
		}
	case schemas.DecisionTypeScore:
		if len(question.Levels) == 0 {
			return OpenAIDecisionQuestion{}, providerUtils.InvalidRequestErrorf("score question %d requires levels", index)
		}
		converted.Levels = make([]OpenAIDecisionLevel, len(question.Levels))
		for j, level := range question.Levels {
			label := level.Label
			if label == "" {
				label = strconv.Itoa(j)
			}
			converted.Levels[j] = OpenAIDecisionLevel{Label: label, Description: openAIText(level.Description)}
		}
	}
	return converted, nil
}

func openAITextParts(messages []schemas.DecisionInputMessage) []schemas.DecisionInputMessage {
	if !hasStructuredTextPart(messages) {
		return messages
	}
	converted := make([]schemas.DecisionInputMessage, len(messages))
	for i, message := range messages {
		converted[i] = message
		if message.Content.Parts == nil {
			continue
		}
		parts := make([]schemas.DecisionInputPart, len(message.Content.Parts))
		for j, part := range message.Content.Parts {
			if part.Text != nil && part.Text.Structured != nil {
				part.Text = schemas.NewDecisionText(providerUtils.DecisionTextString(part.Text))
			}
			parts[j] = part
		}
		converted[i].Content.Parts = parts
	}
	return converted
}

func hasStructuredTextPart(messages []schemas.DecisionInputMessage) bool {
	for _, message := range messages {
		for _, part := range message.Content.Parts {
			if part.Text != nil && part.Text.Structured != nil {
				return true
			}
		}
	}
	return false
}

func openAIText(text *schemas.DecisionText) *string {
	if text == nil {
		return nil
	}
	if text.Structured == nil {
		return text.Text
	}
	return schemas.Ptr(providerUtils.DecisionTextString(text))
}

func (response *OpenAIDecisionResponse) ToBifrostDecisionResponse(request *schemas.BifrostDecisionRequest) (*schemas.BifrostDecisionResponse, error) {
	if len(response.Answers) != len(request.Questions) {
		return nil, fmt.Errorf("OpenAI returned %d decision answers for %d questions", len(response.Answers), len(request.Questions))
	}
	answers := make([]schemas.DecisionAnswer, len(response.Answers))
	for i, wire := range response.Answers {
		answer := wire.DecisionAnswer
		question := request.Questions[i]
		if answer.Name != nil && question.Name != nil && *answer.Name != *question.Name {
			return nil, fmt.Errorf("OpenAI decision answer %d is named %q; expected %q", i, *answer.Name, *question.Name)
		}
		if answer.Type != schemas.DecisionTypeRefusal && answer.Type != question.Type {
			return nil, fmt.Errorf("OpenAI answered decision question %d as %q; expected %q", i, answer.Type, question.Type)
		}
		if answer.Name == nil {
			answer.Name = question.Name
		}

		if answer.Type == schemas.DecisionTypeScore && answer.Legend == nil {
			answer.Legend = providerUtils.DecisionScoreLegend(question)
		}
		answers[i] = answer
	}
	return &schemas.BifrostDecisionResponse{
		Model:   response.Model,
		Answers: answers,
		Usage:   response.Usage.ToBifrostLLMUsage(),
	}, nil
}

func HandleOpenAIDecisionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostDecisionRequest,
	authHeaders map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostDecisionResponse, *schemas.BifrostError) {
	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIDecisionRequest(request)
		},
	)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	if !providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, providerName) {
		req.SetBody(jsonData)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, _, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response := &OpenAIDecisionResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	bifrostResponse, err := response.ToBifrostDecisionResponse(request)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(err.Error(), nil), jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if bifrostResponse.Model == "" {
		bifrostResponse.Model = request.Model
	}
	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders
	if sendBackRawRequest {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}
