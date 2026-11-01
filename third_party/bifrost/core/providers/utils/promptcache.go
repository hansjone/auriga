// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"slices"

	"github.com/maximhq/bifrost/core/schemas"
)

type injectionTarget struct {
	msg        int
	block      int
	promoteStr bool
	toolOutput bool
}

const MaxInjectedCacheBreakpoints = 4

func PromptCacheInjectionEnabled(cfg *schemas.PromptCacheConfig, provider schemas.ModelProvider, model string) bool {
	if cfg == nil || (!cfg.AutoInject && len(cfg.InjectionPoints) == 0) {
		return false
	}
	caps := schemas.ResolveModelCaps(provider, model)
	return caps.SupportsPromptCaching(schemas.ModelSupportsPromptCaching(provider, model))
}

func ResolvePromptCacheConfig(ctx *schemas.BifrostContext, cfg *schemas.PromptCacheConfig) *schemas.PromptCacheConfig {
	if ctx == nil || cfg == nil {
		return cfg
	}
	override, ok := ctx.Value(schemas.BifrostContextKeyPromptCacheAutoInject).(bool)
	if !ok || cfg.AutoInject == override {
		return cfg
	}
	cp := *cfg
	cp.AutoInject = override
	return &cp
}

func InjectResponsesCacheBreakpointsSkipping(cfg *schemas.PromptCacheConfig, input []schemas.ResponsesMessage, skip int) []schemas.ResponsesMessage {
	if cfg == nil || len(input) == 0 || responsesHasCacheMarker(input) {
		return input
	}

	var targets []injectionTarget
	if skip < 0 || skip >= len(input) {
		targets = responsesInjectionTargets(cfg, input)
	} else {
		targets = shiftTargetsPast(responsesInjectionTargets(cfg, withoutIndex(input, skip)), skip)
	}
	if len(targets) == 0 {
		return input
	}

	marker := &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral, TTL: cfg.TTL}
	out := make([]schemas.ResponsesMessage, len(input))
	copy(out, input)
	copied := make(map[int]bool, len(targets))
	for _, t := range targets {
		if !copied[t.msg] {
			out[t.msg] = schemas.DeepCopyResponsesMessage(input[t.msg])
			copied[t.msg] = true
		}
		msg := &out[t.msg]
		if t.toolOutput {
			msg.CacheControl = marker
			continue
		}
		if t.promoteStr {

			text := *msg.Content.ContentStr
			msg.Content.ContentStr = nil
			msg.Content.ContentBlocks = []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesInputMessageContentBlockTypeText,
				Text: &text,
			}}
			msg.Content.ContentBlocks[0].CacheControl = marker
			continue
		}
		msg.Content.ContentBlocks[t.block].CacheControl = marker
	}
	return out
}

func InjectChatCacheBreakpointsSkipping(cfg *schemas.PromptCacheConfig, input []schemas.ChatMessage, skip int) []schemas.ChatMessage {
	if cfg == nil || len(input) == 0 || chatHasCacheMarker(input) {
		return input
	}

	var targets []injectionTarget
	if skip < 0 || skip >= len(input) {
		targets = chatInjectionTargets(cfg, input)
	} else {
		targets = shiftTargetsPast(chatInjectionTargets(cfg, withoutIndex(input, skip)), skip)
	}
	if len(targets) == 0 {
		return input
	}

	marker := &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral, TTL: cfg.TTL}
	out := make([]schemas.ChatMessage, len(input))
	copy(out, input)
	copied := make(map[int]bool, len(targets))
	for _, t := range targets {
		if !copied[t.msg] {
			out[t.msg] = schemas.DeepCopyChatMessage(input[t.msg])
			copied[t.msg] = true
		}
		msg := &out[t.msg]
		if t.promoteStr {
			text := *msg.Content.ContentStr
			msg.Content.ContentStr = nil
			msg.Content.ContentBlocks = []schemas.ChatContentBlock{{
				Type: schemas.ChatContentBlockTypeText,
				Text: &text,
			}}
			msg.Content.ContentBlocks[0].CacheControl = marker
			continue
		}
		msg.Content.ContentBlocks[t.block].CacheControl = marker
	}
	return out
}

func ChatCachePointsSupported(provider schemas.ModelProvider, model string) bool {
	if provider != schemas.Bedrock {
		return false
	}
	return schemas.ResolveModelCaps(provider, model).SupportsCachePoint(schemas.BedrockModelSupportsCachePoints(model))
}

func StripChatCachePoints(input []schemas.ChatMessage) ([]schemas.ChatMessage, bool) {
	var out []schemas.ChatMessage
	for i := range input {
		content := input[i].Content
		if content == nil || !slices.ContainsFunc(content.ContentBlocks, hasChatCachePoint) {
			continue
		}
		if out == nil {
			out = slices.Clone(input)
		}
		blocks := make([]schemas.ChatContentBlock, 0, len(content.ContentBlocks))
		for _, block := range content.ContentBlocks {
			if block.CachePoint != nil {

				if block.Type == "" {
					continue
				}
				block.CachePoint = nil
			}
			blocks = append(blocks, block)
		}
		contentCopy := *content
		contentCopy.ContentBlocks = blocks
		out[i].Content = &contentCopy
	}
	if out == nil {
		return input, false
	}
	return out, true
}

func hasChatCachePoint(block schemas.ChatContentBlock) bool {
	return block.CachePoint != nil
}

func withoutIndex[T any](input []T, skip int) []T {
	view := make([]T, 0, len(input)-1)
	view = append(view, input[:skip]...)
	return append(view, input[skip+1:]...)
}

func shiftTargetsPast(targets []injectionTarget, skip int) []injectionTarget {
	for i := range targets {
		if targets[i].msg >= skip {
			targets[i].msg++
		}
	}
	return targets
}

func isResponsesToolOutput(msg *schemas.ResponsesMessage) bool {
	return msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeFunctionCallOutput &&
		msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.Output != nil
}

func responsesRoleAt(msg *schemas.ResponsesMessage) string {
	if isResponsesToolOutput(msg) {
		return string(schemas.ResponsesInputMessageRoleUser)
	}
	if msg.Role == nil {
		return ""
	}
	return string(*msg.Role)
}

func responsesHasCacheMarker(input []schemas.ResponsesMessage) bool {
	for i := range input {
		if input[i].CacheControl != nil {
			return true
		}
		if input[i].Content == nil {
			continue
		}
		for j := range input[i].Content.ContentBlocks {
			b := &input[i].Content.ContentBlocks[j]
			if b.CacheControl != nil || b.PromptCacheBreakpoint != nil {
				return true
			}
		}
	}
	return false
}

func chatHasCacheMarker(input []schemas.ChatMessage) bool {
	for i := range input {
		if input[i].Content == nil {
			continue
		}
		for j := range input[i].Content.ContentBlocks {
			b := &input[i].Content.ContentBlocks[j]
			if b.CacheControl != nil || b.PromptCacheBreakpoint != nil {
				return true
			}
		}
	}
	return false
}

func responsesInjectionTargets(cfg *schemas.PromptCacheConfig, input []schemas.ResponsesMessage) []injectionTarget {
	if len(cfg.InjectionPoints) > 0 {
		return responsesPointTargets(cfg.InjectionPoints, input)
	}
	if !cfg.AutoInject {
		return nil
	}

	for i := range input {
		if input[i].Content == nil {
			continue
		}
		if s := input[i].Content.ContentStr; s != nil && *s != "" {
			return []injectionTarget{{msg: i, promoteStr: true}}
		}
		for j := range input[i].Content.ContentBlocks {
			if isCacheableResponsesBlock(input[i].Content.ContentBlocks[j].Type) {
				return []injectionTarget{{msg: i, block: j}}
			}
		}
	}
	return nil
}

func chatInjectionTargets(cfg *schemas.PromptCacheConfig, input []schemas.ChatMessage) []injectionTarget {
	if len(cfg.InjectionPoints) > 0 {
		return chatPointTargets(cfg.InjectionPoints, input)
	}
	if !cfg.AutoInject {
		return nil
	}
	for i := range input {
		if input[i].Content == nil {
			continue
		}
		if s := input[i].Content.ContentStr; s != nil && *s != "" {
			return []injectionTarget{{msg: i, promoteStr: true}}
		}
		for j := range input[i].Content.ContentBlocks {
			if isCacheableChatBlock(input[i].Content.ContentBlocks[j].Type) {
				return []injectionTarget{{msg: i, block: j}}
			}
		}
	}
	return nil
}

func responsesPointTargets(points []schemas.CacheControlInjectionPoint, input []schemas.ResponsesMessage) []injectionTarget {
	var out []injectionTarget
	seen := make(map[int]bool)
	for _, p := range points {
		for _, idx := range matchMessageIndices(p, len(input), func(i int) string {
			return responsesRoleAt(&input[i])
		}) {
			if seen[idx] || len(out) >= MaxInjectedCacheBreakpoints {
				continue
			}
			msg := input[idx]
			if isResponsesToolOutput(&msg) {
				out = append(out, injectionTarget{msg: idx, toolOutput: true})
				seen[idx] = true
				continue
			}
			if msg.Content == nil {
				continue
			}
			if s := msg.Content.ContentStr; s != nil && *s != "" {
				out = append(out, injectionTarget{msg: idx, promoteStr: true})
				seen[idx] = true
				continue
			}
			if b := lastCacheableResponsesBlock(msg.Content.ContentBlocks); b >= 0 {
				out = append(out, injectionTarget{msg: idx, block: b})
				seen[idx] = true
			}
		}
	}
	return out
}

func chatPointTargets(points []schemas.CacheControlInjectionPoint, input []schemas.ChatMessage) []injectionTarget {
	var out []injectionTarget
	seen := make(map[int]bool)
	for _, p := range points {
		for _, idx := range matchMessageIndices(p, len(input), func(i int) string {

			if input[i].Role == schemas.ChatMessageRoleTool {
				return string(schemas.ChatMessageRoleUser)
			}
			return string(input[i].Role)
		}) {
			if seen[idx] || len(out) >= MaxInjectedCacheBreakpoints {
				continue
			}
			msg := input[idx]
			if msg.Content == nil {
				continue
			}
			if s := msg.Content.ContentStr; s != nil && *s != "" {
				out = append(out, injectionTarget{msg: idx, promoteStr: true})
				seen[idx] = true
				continue
			}
			if b := lastCacheableChatBlock(msg.Content.ContentBlocks); b >= 0 {
				out = append(out, injectionTarget{msg: idx, block: b})
				seen[idx] = true
			}
		}
	}
	return out
}

func matchMessageIndices(p schemas.CacheControlInjectionPoint, n int, roleAt func(int) string) []int {
	if p.Location != "" && p.Location != schemas.CacheControlInjectionLocationMessage {
		return nil
	}
	if p.Index != nil {
		idx := *p.Index
		if idx < 0 {
			idx += n
		}
		if idx < 0 || idx >= n {
			return nil
		}
		if p.Role != nil && roleAt(idx) != *p.Role {
			return nil
		}
		return []int{idx}
	}
	if p.Role == nil {
		return nil
	}
	var out []int
	for i := 0; i < n; i++ {
		if roleAt(i) == *p.Role {
			out = append(out, i)
		}
	}
	return out
}

func isCacheableResponsesBlock(t schemas.ResponsesMessageContentBlockType) bool {
	switch t {
	case schemas.ResponsesInputMessageContentBlockTypeText,
		schemas.ResponsesInputMessageContentBlockTypeImage,
		schemas.ResponsesInputMessageContentBlockTypeFile,
		schemas.ResponsesOutputMessageContentTypeText:
		return true
	default:
		return false
	}
}

func isCacheableChatBlock(t schemas.ChatContentBlockType) bool {
	switch t {
	case schemas.ChatContentBlockTypeText, schemas.ChatContentBlockTypeImage:
		return true
	default:
		return false
	}
}

func lastCacheableResponsesBlock(blocks []schemas.ResponsesMessageContentBlock) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if isCacheableResponsesBlock(blocks[i].Type) {
			return i
		}
	}
	return -1
}

func lastCacheableChatBlock(blocks []schemas.ChatContentBlock) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if isCacheableChatBlock(blocks[i].Type) {
			return i
		}
	}
	return -1
}
