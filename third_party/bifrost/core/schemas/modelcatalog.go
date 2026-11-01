// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type ModelInfoProvider interface {
	GetModelInfo(provider ModelProvider, model string) *Model

	CalculateRequestCost(ctx *BifrostContext, resp *BifrostResponse) float64

	CalculateRequestCostBreakdown(ctx *BifrostContext, resp *BifrostResponse) *BifrostCost
}
