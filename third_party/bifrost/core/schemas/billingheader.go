// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "slices"

type billingHeaderBlock struct {
	index int
	block ResponsesMessageContentBlock
}
type anthropicBillingHeader struct {
	system *ResponsesMessageContent
	text   *string
	blocks []billingHeaderBlock
}

func (r *BifrostResponsesRequest) WithAnthropicBillingHeader() *BifrostResponsesRequest {
	if r == nil || r.anthropicBillingHeader == nil {
		return r
	}
	header := r.anthropicBillingHeader
	index := -1
	var blocks []ResponsesMessageContentBlock
	if header.system != nil {
		index = slices.IndexFunc(r.Input, func(m ResponsesMessage) bool { return m.Content == header.system })
		if index < 0 {
			return r
		}
		blocks = r.Input[index].Content.ContentBlocks
	}
	content := &ResponsesMessageContent{ContentStr: header.text}
	if header.text == nil {
		content.ContentBlocks = make([]ResponsesMessageContentBlock, 0, len(blocks)+len(header.blocks))
		start := 0
		for i, saved := range header.blocks {
			end := min(saved.index-i, len(blocks))
			content.ContentBlocks = append(content.ContentBlocks, blocks[start:end]...)
			content.ContentBlocks = append(content.ContentBlocks, saved.block)
			start = end
		}
		content.ContentBlocks = append(content.ContentBlocks, blocks[start:]...)
	}
	copy := *r
	copy.anthropicBillingHeader = nil
	if index >= 0 {
		copy.Input = slices.Clone(r.Input)
		copy.Input[index].Content = content
	} else {
		at := firstContentIndex(r.Input)
		copy.Input = make([]ResponsesMessage, 0, len(r.Input)+1)
		copy.Input = append(copy.Input, r.Input[:at]...)
		copy.Input = append(copy.Input, ResponsesMessage{Role: Ptr(ResponsesInputMessageRoleSystem), Content: content})
		copy.Input = append(copy.Input, r.Input[at:]...)
	}
	return &copy
}

func firstContentIndex(input []ResponsesMessage) int {
	i := 0
	for i < len(input) && input[i].IsEffortOnlySystemItem() {
		i++
	}
	return i
}
