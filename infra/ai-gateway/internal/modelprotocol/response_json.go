package modelprotocol

var usageRule = object("prompt_tokens completion_tokens total_tokens", map[string]rule{
	"prompt_tokens": integer(0, safeInteger), "completion_tokens": integer(0, safeInteger), "total_tokens": integer(0, safeInteger),
})
var finishRule = enum("stop", "length", "tool_calls", "content_filter")
var completionRule = object("id object created model choices usage", map[string]rule{
	"id": text(1, 1024), "object": enum("chat.completion"), "created": integer(0, safeInteger), "model": text(1, 128), "usage": usageRule,
	"choices": array(1, 1, object("index message finish_reason", map[string]rule{
		"index": integer(0, 0), "finish_reason": finishRule, "message": object("role content", map[string]rule{
			"role": enum("assistant"), "content": nullable(stringRule), "tool_calls": array(0, 128, callRule),
		}),
	})),
})
var chunkRule = object("id object created model choices", map[string]rule{
	"id": text(1, 1024), "object": enum("chat.completion.chunk"), "created": integer(0, safeInteger), "model": text(1, 128), "usage": usageRule,
	"choices": array(0, 1, object("index delta finish_reason", map[string]rule{
		"index": integer(0, 0), "finish_reason": nullable(finishRule), "delta": object("", map[string]rule{
			"role": enum("assistant"), "content": nullable(stringRule), "tool_calls": array(0, 128, object("index", map[string]rule{
				"index": integer(0, 127), "id": text(1, 128), "type": enum("function"), "function": object("", map[string]rule{"name": text(0, 64), "arguments": text(0, 262144)}),
			})),
		}),
	})),
})
