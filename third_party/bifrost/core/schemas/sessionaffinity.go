// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type SessionAffinity interface {
	ResolveRoute(ctx *BifrostContext, requested Route, chain []Route) []Route

	ResolveKey(ctx *BifrostContext, provider ModelProvider, model string, eligible []Key) (key Key, ok bool)

	Observe(ctx *BifrostContext, requested Route, outcome RouteOutcome)
}
type Route struct {
	Provider ModelProvider
	Model    string
	KeyID    string
}
type RouteOutcome struct {
	Served *Route

	KeyID string

	Fallback bool

	DirectKey bool

	Err *BifrostError
}

func IsSessionAffinityActive(ctx *BifrostContext) bool {
	if ctx == nil {
		return false
	}
	if enabled, ok := ctx.Value(BifrostContextKeySessionAffinity).(bool); ok && !enabled {
		return false
	}
	sessionID, _ := ctx.Value(BifrostContextKeySessionID).(string)
	return sessionID != ""
}
