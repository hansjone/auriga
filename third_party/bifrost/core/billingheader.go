// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package bifrost

import "github.com/maximhq/bifrost/core/schemas"

func restoreResponsesBillingHeader(ctx *schemas.BifrostContext, provider schemas.ModelProvider, r *schemas.BifrostResponsesRequest) *schemas.BifrostResponsesRequest {
	if r == nil {
		return r
	}
	if ctx != nil && ctx.Value(schemas.BifrostContextKeyUseRawRequestBody) == true && len(r.RawRequestBody) > 0 {
		return r
	}
	family := schemas.ResolveFamily(ctx, r.Model)
	if family == schemas.ModelFamilyAnthropic || (family == "" && schemas.ResolveBaseProvider(ctx, provider) == schemas.Anthropic) {
		return r.WithAnthropicBillingHeader()
	}
	return r
}
