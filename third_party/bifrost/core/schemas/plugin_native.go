// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type LLMPluginShortCircuit struct {
	Response *BifrostResponse
	Stream   chan *BifrostStreamChunk
	Error    *BifrostError
}
