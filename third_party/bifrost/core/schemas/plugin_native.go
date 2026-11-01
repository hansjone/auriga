package schemas

type LLMPluginShortCircuit struct {
	Response *BifrostResponse
	Stream   chan *BifrostStreamChunk
	Error    *BifrostError
}
