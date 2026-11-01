package bifrost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

type sessionResolution struct {
	route    string
	keyRoute schemas.Route
	keyID    string
}
type sessionAffinity struct {
	kv     schemas.KVStore
	logger schemas.Logger
}

const (
	sessionStateKeyPrefix = "session:v2:"

	SessionStateKindRoute = "route"

	SessionStateKindKey = "key"

	sessionAffinityResolvedKey schemas.BifrostContextKey = "bifrost-session-affinity-resolved"
)

func NewSessionAffinity(kv schemas.KVStore, logger schemas.Logger) schemas.SessionAffinity {
	return &sessionAffinity{kv: kv, logger: logger}
}

func (a *sessionAffinity) ResolveRoute(ctx *schemas.BifrostContext, requested schemas.Route, chain []schemas.Route) []schemas.Route {
	if a == nil || a.kv == nil || ctx == nil {
		return chain
	}
	if len(chain) == 0 {
		return chain
	}

	if requested.Provider != "" {
		return chain
	}
	key := SessionStateKey(ctx, SessionStateKindRoute, string(requested.Provider), requested.Model)
	bound, found := SessionStateString(a.kv, key)
	if !found {
		return chain
	}
	boundRoute, ok := ParseRouteState(bound)
	if !ok {

		if _, err := a.kv.Delete(key); err != nil {
			a.logger.Warn("error dropping unreadable session route binding: %s", err.Error())
		}
		return chain
	}

	at := slices.IndexFunc(chain, func(route schemas.Route) bool {
		return route.Provider == boundRoute.Provider && route.Model == boundRoute.Model
	})
	if at < 0 {

		if _, err := a.kv.Delete(key); err != nil {
			a.logger.Warn("error dropping session route binding to %s: %s", bound, err.Error())
		}
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Session was last served by %s for %s, which this request cannot use, so the routing decision stands and the session rebinds on its next served request", bound, requested.Model))

		schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineSessionAffinity)
		return chain
	}
	a.remember(ctx, func(r *sessionResolution) { r.route = RouteStateValue(chain[at]) })
	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineSessionAffinity)
	if at == 0 {

		ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Session stays on %s for %s, which routing also proposed", bound, requested.Model))
		return chain
	}
	resolved := make([]schemas.Route, 0, len(chain))
	resolved = append(resolved, chain[at])
	resolved = append(resolved, chain[:at]...)
	resolved = append(resolved, chain[at+1:]...)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Session stays on %s for %s; routing proposed %s", bound, requested.Model, RouteStateValue(chain[0])))
	return resolved
}

func (a *sessionAffinity) ResolveKey(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string, eligible []schemas.Key) (schemas.Key, bool) {
	if a == nil || a.kv == nil || ctx == nil || len(eligible) == 0 {
		return schemas.Key{}, false
	}
	if fallbackIndex, _ := ctx.Value(schemas.BifrostContextKeyFallbackIndex).(int); fallbackIndex > 0 {
		return schemas.Key{}, false
	}
	key := SessionStateKey(ctx, SessionStateKindKey, string(provider), model)
	boundID, found := SessionStateString(a.kv, key)
	if !found || (len(eligible) == 1 && eligible[0].ID == boundID) {
		return schemas.Key{}, false
	}
	for _, candidate := range eligible {
		if candidate.ID != boundID {
			continue
		}
		a.remember(ctx, func(r *sessionResolution) {
			r.keyRoute = schemas.Route{Provider: provider, Model: model}
			r.keyID = candidate.ID
		})
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Session reused key %s for %s/%s", candidate.Name, provider, model))
		schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineSessionAffinity)
		return candidate, true
	}
	if _, err := a.kv.Delete(key); err != nil {
		a.logger.Warn("error deleting stale session key binding for provider=%s: %s", provider, err.Error())
	}
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("The key this session last used for %s/%s is no longer eligible, so one is being picked", provider, model))
	return schemas.Key{}, false
}

func (a *sessionAffinity) Observe(ctx *schemas.BifrostContext, requested schemas.Route, outcome schemas.RouteOutcome) {
	if a == nil || a.kv == nil || ctx == nil {
		return
	}
	ttl := sessionTTLFromContext(ctx)
	resolved, _ := ctx.Value(sessionAffinityResolvedKey).(sessionResolution)

	if outcome.Served == nil && cancelledByCaller(outcome.Err) {
		return
	}
	if resolved.keyID != "" && (outcome.Served == nil || *outcome.Served != resolved.keyRoute) {
		a.forget(SessionStateKey(ctx, SessionStateKindKey, string(resolved.keyRoute.Provider), resolved.keyRoute.Model), fmt.Sprintf("key binding to %s on %s/%s", resolved.keyID, resolved.keyRoute.Provider, resolved.keyRoute.Model))
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("The key this session followed for %s/%s failed, so the session forgets it and a key is picked normally next time", resolved.keyRoute.Provider, resolved.keyRoute.Model))
	}
	if outcome.Served == nil {
		if resolved.route != "" && requested.Provider == "" {
			a.forget(SessionStateKey(ctx, SessionStateKindRoute, string(requested.Provider), requested.Model), fmt.Sprintf("route binding to %s", resolved.route))
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("The provider this session followed for %s failed, so the session forgets it and the next request follows the routing decision", requested.Model))
		}
		return
	}
	served := *outcome.Served

	if requested.Provider == "" {
		routeKey := SessionStateKey(ctx, SessionStateKindRoute, string(requested.Provider), requested.Model)
		if outcome.DirectKey && outcome.Fallback {

			if resolved.route != "" {
				a.forget(routeKey, fmt.Sprintf("route binding to %s", resolved.route))
			}
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Fallback %s served %s without the caller's own key, so the session does not bind to it and the next request follows the routing decision", RouteStateValue(served), requested.Model))
			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineSessionAffinity)
		} else {
			a.settle(routeKey, RouteStateValue(served), resolved.route, ttl)
		}
	}
	if outcome.KeyID != "" {
		key := SessionStateKey(ctx, SessionStateKindKey, string(served.Provider), served.Model)
		followed := ""
		if resolved.keyRoute == served {
			followed = resolved.keyID
		}
		if followed == "" && outcome.Fallback {

			a.replace(key, outcome.KeyID, ttl)
		} else {
			a.settle(key, outcome.KeyID, followed, ttl)
		}
	}
}

func (a *sessionAffinity) settle(key, value, followed string, ttl time.Duration) {
	if followed == "" {
		if _, err := a.kv.SetNXWithTTL(key, value, ttl); err != nil {
			a.logger.Warn("error binding session to %s: %s", value, err.Error())
		}
		return
	}
	if followed != value {
		a.logger.Debug("session moved from %s to %s", followed, value)
	}
	a.replace(key, value, ttl)
}

func cancelledByCaller(err *schemas.BifrostError) bool {
	return err != nil && err.Error != nil && err.Error.Type != nil && *err.Error.Type == schemas.RequestCancelled
}

func (a *sessionAffinity) forget(key, what string) {
	if _, err := a.kv.Delete(key); err != nil {
		a.logger.Warn("error dropping failed session %s: %s", what, err.Error())
	}
}

func (a *sessionAffinity) replace(key, value string, ttl time.Duration) {
	if err := a.kv.SetWithTTL(key, value, ttl); err != nil {
		a.logger.Warn("error rebinding session to %s: %s", value, err.Error())
	}
}

func (a *sessionAffinity) remember(ctx *schemas.BifrostContext, update func(*sessionResolution)) {
	resolution, _ := ctx.Value(sessionAffinityResolvedKey).(sessionResolution)
	update(&resolution)
	ctx.SetValue(sessionAffinityResolvedKey, resolution)
}

func RouteStateValue(route schemas.Route) string {
	return string(route.Provider) + "/" + route.Model
}

func ParseRouteState(value string) (schemas.Route, bool) {
	provider, model, found := strings.Cut(value, "/")
	if !found || provider == "" {
		return schemas.Route{}, false
	}
	return schemas.Route{Provider: schemas.ModelProvider(provider), Model: model}, true
}

func (bifrost *Bifrost) resolveSessionRoute(ctx *schemas.BifrostContext, requested schemas.Route, req *schemas.BifrostRequest) {

	ctx.ClearValue(sessionAffinityResolvedKey)
	if !schemas.IsSessionAffinityActive(ctx) {

		if sessionIDFromContext(ctx) != "" {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Request carries a session but asked not to follow it, so the routing decision stands for %s", requested.Model))
		}
		return
	}
	provider, model, fallbacks := req.GetRequestFields()
	if provider == "" {
		return
	}

	if bifrost.callerPinsKeyOf(ctx, provider) {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, fmt.Sprintf("Request pins the caller's key on %s, so the session keeps the routing decision for %s", provider, requested.Model))
		return
	}

	primaryKeyPin := routingKeyPinFromContext(ctx)
	chain := make([]schemas.Route, 0, 1+len(fallbacks))
	chain = append(chain, schemas.Route{Provider: provider, Model: model, KeyID: primaryKeyPin})
	for _, fallback := range fallbacks {
		chain = append(chain, schemas.Route{Provider: fallback.Provider, Model: fallback.Model, KeyID: strings.TrimSpace(fallback.KeyID)})
	}
	resolved := bifrost.sessionAffinity.ResolveRoute(ctx, requested, chain)
	if len(resolved) == 0 || slices.Equal(resolved, chain) {
		return
	}
	req.SetProvider(resolved[0].Provider)
	req.SetModel(resolved[0].Model)

	inferred := len(fallbacks) > 0 && fallbacks[0].Inferred
	next := make([]schemas.Fallback, 0, len(resolved)-1)
	for _, route := range resolved[1:] {
		next = append(next, schemas.Fallback{Provider: route.Provider, Model: route.Model, KeyID: route.KeyID, Inferred: inferred})
	}
	req.SetFallbacks(next)
	if resolved[0].KeyID == primaryKeyPin {
		return
	}
	setRoutingPin(ctx, resolved[0].KeyID)
	moved := fmt.Sprintf("Session moved the request from %s to %s", provider, resolved[0].Provider)
	if resolved[0].KeyID != "" {
		moved += ", which brings the key pinned for it"
	} else {
		moved += ", so a key is picked normally there"
	}
	if primaryKeyPin != "" {
		moved += fmt.Sprintf("; the key pinned for %s applies if %s is tried as a fallback", provider, provider)
	}
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineSessionAffinity, schemas.LogLevelInfo, moved)
}

func routingKeyPinFromContext(ctx *schemas.BifrostContext) string {
	pin, _ := ctx.Value(schemas.BifrostContextKeyRoutingPinnedAPIKeyID).(string)
	return strings.TrimSpace(pin)
}

func (bifrost *Bifrost) callerPinsKeyOf(ctx *schemas.BifrostContext, provider schemas.ModelProvider) bool {
	if routingKeyPinFromContext(ctx) != "" {
		return false
	}
	id, _ := ctx.Value(schemas.BifrostContextKeyAPIKeyID).(string)
	id = strings.TrimSpace(id)
	name, _ := ctx.Value(schemas.BifrostContextKeyAPIKeyName).(string)
	name = strings.TrimSpace(name)
	if id == "" && name == "" {
		return false
	}
	keys, err := bifrost.account.GetKeysForProvider(ctx, provider)
	if err != nil {
		return false
	}
	for _, key := range keys {

		if (id != "" && key.ID == id) || (id == "" && key.Name == name) {
			return true
		}
	}
	return false
}

func setRoutingPin(ctx *schemas.BifrostContext, keyID string) {
	if keyID == "" {
		ctx.ClearValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID)
		ctx.ClearValue(schemas.BifrostContextKeyAPIKeyID)
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID, keyID)
	ctx.SetValue(schemas.BifrostContextKeyAPIKeyID, keyID)
}

func (bifrost *Bifrost) observeSessionOutcome(ctx *schemas.BifrostContext, requested schemas.Route, served *schemas.Route, fallback, directKey bool, err *schemas.BifrostError) {
	if !schemas.IsSessionAffinityActive(ctx) {
		return
	}
	outcome := schemas.RouteOutcome{Served: served, Fallback: fallback, DirectKey: directKey, Err: err}

	if served != nil && !(directKey && !fallback) {
		outcome.KeyID, _ = ctx.Value(schemas.BifrostContextKeySelectedKeyID).(string)
	}
	bifrost.sessionAffinity.Observe(ctx, requested, outcome)
}

func SessionStateKey(ctx *schemas.BifrostContext, kind string, parts ...string) string {
	virtualKeyID, userID := sessionIdentityParts(ctx)
	h := sha256.New()
	for _, part := range append([]string{virtualKeyID, userID, sessionIDFromContext(ctx)}, parts...) {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return sessionStateKeyPrefix + kind + ":" + hex.EncodeToString(h.Sum(nil))
}

func sessionIdentityParts(ctx *schemas.BifrostContext) (virtualKeyID, userID string) {
	if ctx == nil {
		return "", ""
	}
	grant := ctx.Grant()
	if grant == nil {
		return "", ""
	}
	identity := grant.Identity()
	if identity == nil {
		return "", ""
	}
	if key := identity.VirtualKey(); key != nil {
		virtualKeyID = key.ID
	}
	if user := identity.User(); user != nil {
		userID = user.ID
	}
	return virtualKeyID, userID
}

func sessionIDFromContext(ctx *schemas.BifrostContext) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(schemas.BifrostContextKeySessionID).(string)
	return sessionID
}

func sessionTTLFromContext(ctx *schemas.BifrostContext) time.Duration {
	if ctx != nil {
		if ttl, ok := ctx.Value(schemas.BifrostContextKeySessionTTL).(time.Duration); ok && ttl > 0 {
			return ttl
		}
	}
	return schemas.DefaultSessionStickyTTL
}

func SessionStateString(kv schemas.KVStore, key string) (string, bool) {
	raw, err := kv.Get(key)
	if err != nil {
		return "", false
	}
	var value string
	switch v := raw.(type) {
	case string:
		value = v
	case []byte:
		if err := sonic.Unmarshal(v, &value); err != nil {
			value = string(v)
		}
	}
	if value == "" {
		return "", false
	}
	return value, true
}
