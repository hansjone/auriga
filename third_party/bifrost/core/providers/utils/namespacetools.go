package utils

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/maximhq/bifrost/core/schemas"
)

var toolNameUnsafeGemini = regexp.MustCompile(`[^A-Za-z0-9_.:-]+`)
var toolNameUnsafeStrict = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type ToolNameLimit struct {
	MaxLength int
	unsafe    *regexp.Regexp
}

const namespaceToolSeparator = "__"

func ResponsesNamespaceToolsSupported(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, model string) bool {
	if wireCannotCarryNamespaceTools(ctx, baseProvider, model) {
		return false
	}
	caps := schemas.ResolveModelCaps(baseProvider, schemas.ResolveCanonicalModel(ctx, model))
	return caps.SupportsNamespaceTools(DefaultNamespaceToolSupport(ctx, baseProvider, model))
}

func wireCannotCarryNamespaceTools(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, model string) bool {
	switch baseProvider {
	case schemas.Anthropic, schemas.Gemini, schemas.Vertex:
		return true
	case schemas.Azure, schemas.BedrockMantle:
		return schemas.IsAnthropicModelFamily(ctx, model)
	default:
		return false
	}
}

func DefaultNamespaceToolSupport(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, model string) bool {
	switch baseProvider {
	case schemas.OpenAI:
		return true
	case schemas.Azure, schemas.BedrockMantle:
		return !schemas.IsAnthropicModelFamily(ctx, model)
	default:
		return false
	}
}

const defaultToolNamespace = "functions"

const namespaceDescriptionSeparator = "\n\n"

func UnwrapDefaultNamespaceTools(req *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesRequest, *schemas.BifrostError) {
	if req == nil || req.Params == nil {
		return req, nil
	}
	found := false
	for _, tool := range req.Params.Tools {
		if isDefaultNamespaceTool(tool) {
			found = true
			break
		}
	}
	if !found {
		return req, nil
	}

	unwrapped := make([]schemas.ResponsesTool, 0, len(req.Params.Tools))
	for _, tool := range req.Params.Tools {
		if !isDefaultNamespaceTool(tool) {
			unwrapped = append(unwrapped, tool)
			continue
		}
		if tool.ResponsesToolNamespace != nil {

			for _, nested := range tool.ResponsesToolNamespace.Tools {
				hoisted := nested
				hoisted.Description = joinNamespaceDescription(tool.Description, nested.Description)
				unwrapped = append(unwrapped, hoisted)
			}
		}
	}
	if bifrostErr := checkUniqueToolNames(unwrapped, nil); bifrostErr != nil {
		return nil, bifrostErr
	}

	cp := *req
	params := *req.Params
	params.Tools = unwrapped
	cp.Params = &params
	return &cp, nil
}

func isDefaultNamespaceTool(tool schemas.ResponsesTool) bool {
	return tool.Type == schemas.ResponsesToolTypeNamespace && tool.Name != nil && *tool.Name == defaultToolNamespace
}

func FlattenResponsesNamespaceTools(ctx *schemas.BifrostContext, req *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesRequest, *schemas.BifrostError) {
	if req == nil || req.Params == nil || !hasNamespaceTool(req.Params.Tools) {
		return req, nil
	}

	limit := ResolveToolNameLimit(ctx, schemas.ResolveBaseProvider(ctx, req.Provider), req.Model)
	aliases := make(map[string]schemas.NamespaceToolAlias)

	byFunction := make(map[string][]string)
	flattened := make([]schemas.ResponsesTool, 0, len(req.Params.Tools))
	for _, tool := range req.Params.Tools {
		if tool.Type != schemas.ResponsesToolTypeNamespace {
			flattened = append(flattened, tool)
			continue
		}
		if tool.Name == nil || *tool.Name == "" || tool.ResponsesToolNamespace == nil || len(tool.ResponsesToolNamespace.Tools) == 0 {
			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyDroppedUnsupportedTools, string(schemas.ResponsesToolTypeNamespace))
			continue
		}
		namespace := *tool.Name
		for _, nested := range tool.ResponsesToolNamespace.Tools {
			if nested.Type != schemas.ResponsesToolTypeFunction || nested.Name == nil || *nested.Name == "" {
				schemas.AppendToContextList(ctx, schemas.BifrostContextKeyDroppedUnsupportedTools, namespace+namespaceToolSeparator+string(nested.Type))
				continue
			}
			alias := namespaceToolAlias(namespace, *nested.Name, limit)
			hoisted := nested
			hoisted.Name = new(alias)
			hoisted.Description = joinNamespaceDescription(tool.Description, nested.Description)
			flattened = append(flattened, hoisted)
			aliases[alias] = schemas.NamespaceToolAlias{Namespace: namespace, Name: *nested.Name}
			byFunction[*nested.Name] = append(byFunction[*nested.Name], alias)
		}
	}

	if bifrostErr := checkUniqueToolNames(flattened, aliases); bifrostErr != nil {
		return nil, bifrostErr
	}

	cp := *req
	params := *req.Params
	params.Tools = flattened
	cp.Params = &params

	if len(aliases) > 0 {
		cp.Input = realiasNamespacedFunctionCalls(req.Input, aliases, limit)
		toolChoice, bifrostErr := realiasToolChoice(req.Params.ToolChoice, flattened, byFunction, aliases)
		if bifrostErr != nil {
			return nil, bifrostErr
		}
		params.ToolChoice = toolChoice

		cp.NamespaceToolAliases = withBareNameFallbacks(aliases, byFunction, flattened)
	}
	return &cp, nil
}

func joinNamespaceDescription(namespace, member *string) *string {
	nsDesc := ""
	if namespace != nil {
		nsDesc = *namespace
	}
	switch {
	case nsDesc == "":
		return member
	case member == nil || *member == "":
		return new(nsDesc)
	default:
		return new(nsDesc + namespaceDescriptionSeparator + *member)
	}
}

func withBareNameFallbacks(aliases map[string]schemas.NamespaceToolAlias, byFunction map[string][]string, tools []schemas.ResponsesTool) map[string]schemas.NamespaceToolAlias {
	out := make(map[string]schemas.NamespaceToolAlias, len(aliases)+len(byFunction))
	for alias, target := range aliases {
		out[alias] = target
	}
	for bare, candidates := range byFunction {
		if len(candidates) != 1 {
			continue
		}
		if _, taken := out[bare]; taken {
			continue
		}
		if isTopLevelToolName(bare, tools, aliases) {
			continue
		}
		out[bare] = aliases[candidates[0]]
	}
	return out
}

func isTopLevelToolName(name string, tools []schemas.ResponsesTool, aliases map[string]schemas.NamespaceToolAlias) bool {
	for _, tool := range tools {
		if tool.Name == nil || *tool.Name != name {
			continue
		}
		if _, isAlias := aliases[*tool.Name]; !isAlias {
			return true
		}
	}
	return false
}

func (l ToolNameLimit) Sanitize(name string) string {
	return l.unsafe.ReplaceAllString(name, "_")
}

func DefaultToolNameLimit(baseProvider schemas.ModelProvider) ToolNameLimit {
	switch baseProvider {
	case schemas.Anthropic:
		return ToolNameLimit{MaxLength: 128, unsafe: toolNameUnsafeStrict}
	case schemas.Gemini, schemas.Vertex:
		return ToolNameLimit{MaxLength: 128, unsafe: toolNameUnsafeGemini}
	default:
		return ToolNameLimit{MaxLength: 64, unsafe: toolNameUnsafeStrict}
	}
}

func ResolveToolNameLimit(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, model string) ToolNameLimit {
	limit := DefaultToolNameLimit(baseProvider)
	caps := schemas.ResolveModelCaps(baseProvider, schemas.ResolveCanonicalModel(ctx, model))

	if row := caps.ToolNameMaxLength(limit.MaxLength); row >= minToolNameLimit {
		limit.MaxLength = row
	}
	return limit
}

const minToolNameLimit = namespaceAliasHashLength + 2

const namespaceAliasHashLength = 9

func namespaceToolAlias(namespace, function string, limit ToolNameLimit) string {
	full := namespace + namespaceToolSeparator + function
	sanitized := limit.Sanitize(full)
	if len(sanitized) <= limit.MaxLength {
		return sanitized
	}

	hash := fmt.Sprintf("t%08x", uint32(xxhash.Sum64String(full)))
	semantic := strings.Trim(limit.Sanitize(function), "_")
	if semantic == "" {
		semantic = "tool"
	}
	room := limit.MaxLength - len(hash) - 1
	if room <= 0 {

		return hash[:min(len(hash), limit.MaxLength)]
	}
	if len(semantic) > room {
		semantic = semantic[:room]
	}
	return hash + "_" + semantic
}

func hasNamespaceTool(tools []schemas.ResponsesTool) bool {
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeNamespace {
			return true
		}
	}
	return false
}

func checkUniqueToolNames(tools []schemas.ResponsesTool, aliases map[string]schemas.NamespaceToolAlias) *schemas.BifrostError {
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool.Name == nil || *tool.Name == "" {
			continue
		}
		if tool.Type != schemas.ResponsesToolTypeFunction && tool.Type != schemas.ResponsesToolTypeCustom {
			continue
		}
		name := *tool.Name
		if seen[name] {
			message := fmt.Sprintf("tool name %q is not unique after flattening namespace tools", name)
			if alias, ok := aliases[name]; ok {
				message += fmt.Sprintf("; rename function %q in namespace %q or the top-level tool that shares the name", alias.Name, alias.Namespace)
			}
			return NewBifrostBadRequestError(message)
		}
		seen[name] = true
	}
	return nil
}

func realiasNamespacedFunctionCalls(input []schemas.ResponsesMessage, aliases map[string]schemas.NamespaceToolAlias, limit ToolNameLimit) []schemas.ResponsesMessage {
	var out []schemas.ResponsesMessage
	for i, msg := range input {
		if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeFunctionCall || msg.ResponsesToolMessage == nil {
			continue
		}
		call := msg.ResponsesToolMessage
		if call.Namespace == nil || *call.Namespace == "" || call.Name == nil {
			continue
		}
		alias := namespaceToolAlias(*call.Namespace, *call.Name, limit)

		owner := schemas.NamespaceToolAlias{Namespace: *call.Namespace, Name: *call.Name}
		if mapped, known := aliases[alias]; !known || mapped != owner {
			continue
		}
		if out == nil {
			out = make([]schemas.ResponsesMessage, len(input))
			copy(out, input)
		}
		rewritten := *call
		rewritten.Name = new(alias)
		rewritten.Namespace = nil
		out[i].ResponsesToolMessage = &rewritten
	}
	if out == nil {
		return input
	}
	return out
}

func realiasToolChoice(choice *schemas.ResponsesToolChoice, tools []schemas.ResponsesTool, byFunction map[string][]string, aliases map[string]schemas.NamespaceToolAlias) (*schemas.ResponsesToolChoice, *schemas.BifrostError) {
	if choice == nil || choice.ResponsesToolChoiceStruct == nil {
		return choice, nil
	}
	topLevel := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool.Name != nil {
			if _, isAlias := aliases[*tool.Name]; !isAlias {
				topLevel[*tool.Name] = true
			}
		}
	}
	resolve := func(name *string) (*string, *schemas.BifrostError) {
		if name == nil || *name == "" || topLevel[*name] {
			return name, nil
		}
		candidates := byFunction[*name]
		switch len(candidates) {
		case 0:
			return name, nil
		case 1:
			return new(candidates[0]), nil
		}
		namespaces := make([]string, 0, len(candidates))
		for _, alias := range candidates {
			namespaces = append(namespaces, aliases[alias].Namespace)
		}
		sort.Strings(namespaces)
		return nil, NewBifrostBadRequestError(fmt.Sprintf("tool_choice %q is ambiguous across namespaces [%s]; the target provider does not support namespace tools, so the choice must name a single function", *name, strings.Join(namespaces, ", ")))
	}

	structCopy := *choice.ResponsesToolChoiceStruct
	resolvedName, bifrostErr := resolve(structCopy.Name)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	structCopy.Name = resolvedName
	if len(structCopy.Tools) > 0 {
		allowed := make([]schemas.ResponsesToolChoiceAllowedToolDef, len(structCopy.Tools))
		copy(allowed, structCopy.Tools)
		for i := range allowed {
			resolved, bifrostErr := resolve(allowed[i].Name)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			allowed[i].Name = resolved
		}
		structCopy.Tools = allowed
	}
	return &schemas.ResponsesToolChoice{
		ResponsesToolChoiceStr:    choice.ResponsesToolChoiceStr,
		ResponsesToolChoiceStruct: &structCopy,
	}, nil
}
