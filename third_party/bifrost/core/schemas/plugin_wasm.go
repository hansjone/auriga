package schemas

type LLMPluginShortCircuit struct {
	Response *BifrostResponse
	Error    *BifrostError
}
