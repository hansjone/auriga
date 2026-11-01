package schemas

import (
	"context"

	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var pluginLogStorePool = sync.Pool{
	New: func() any {
		return &pluginLogStore{logs: make([]PluginLogEntry, 0, 8)}
	},
}
var reservedKeys = map[BifrostContextKey]struct{}{
	BifrostContextKeyVirtualKey:              {},
	BifrostContextKeyAPIKeyName:              {},
	BifrostContextKeyAPIKeyID:                {},
	BifrostContextKeyDirectKey:               {},
	BifrostContextKeyRequestID:               {},
	BifrostContextKeyFallbackRequestID:       {},
	BifrostContextKeyBillingNonce:            {},
	BifrostContextKeySelectedKeyID:           {},
	BifrostContextKeySelectedKeyName:         {},
	BifrostContextKeyNumberOfRetries:         {},
	BifrostContextKeyFallbackIndex:           {},
	BifrostContextKeySkipKeySelection:        {},
	BifrostContextKeyPassthroughHeaders:      {},
	BifrostContextKeySkipBudgetAndRateLimits: {},
	BifrostContextKeySkipProviderCheck:       {},
	BifrostContextKeySkipModelCheck:          {},
	BifrostContextKeyAdmitUngrantedUser:      {},
	BifrostContextKeyURLPath:                 {},
	BifrostContextKeyDeferTraceCompletion:    {},
	BifrostContextKeyAttemptTrail:            {},
	BifrostContextKeyStreamGated:             {},
	BifrostContextKeyStreamAttemptAbort:      {},
	BifrostContextKeyStreamCommitWatch:       {},
	BifrostContextKeyMCPHealthCheckRequest:   {},
	BifrostContextKeyMCPUnattendedExecution:  {},
	BifrostContextKeyInjectedToolsExecuted:   {},
	BifrostContextKeyUpstreamLatency:         {},
	BifrostContextKeyStreamOverhead:          {},
	BifrostContextKeyRoutingInfo:             {},
	BifrostContextKeyRequestedProvider:       {},
	BifrostContextKeyRequestedModel:          {},
	BifrostContextKeyProviderProxyConfig:     {},
	BifrostContextKeyMCPInboundBearer:        {},
	BifrostContextKeyAuthCredential:          {},
	BifrostContextKeyMCPAgentServedRoute:     {},
	BifrostContextKeyMCPInstructionsText:     {},
}

type pluginLogStore struct {
	mu   sync.Mutex
	logs []PluginLogEntry
}
type BifrostContext struct {
	parent                context.Context
	deadline              time.Time
	hasDeadline           bool
	done                  chan struct{}
	doneOnce              sync.Once
	err                   error
	errMu                 sync.RWMutex
	userValues            map[any]any
	realtimeTurnKeys      map[any]struct{}
	trustedValues         map[any]any
	valuesMu              sync.RWMutex
	blockRestrictedWrites atomic.Bool
	grant                 atomic.Pointer[Grant]

	pluginScope   *string
	pluginLogs    atomic.Pointer[pluginLogStore]
	valueDelegate *BifrostContext
	scopeSpanID   string
}

var NoDeadline time.Time

type bifrostContextSelfKey struct{}

func isReservedKey(key any) bool {
	contextKey, ok := key.(BifrostContextKey)
	if !ok {
		return false
	}
	_, ok = reservedKeys[contextKey]
	return ok
}

func NewBifrostContext(parent context.Context, deadline time.Time) *BifrostContext {
	if parent == nil {
		parent = context.Background()
	}

	for {
		bc, ok := parent.(*BifrostContext)
		if !ok || bc.valueDelegate == nil {
			break
		}
		parent = bc.valueDelegate
	}
	ctx := &BifrostContext{
		parent:                parent,
		deadline:              deadline,
		hasDeadline:           !deadline.IsZero(),
		done:                  make(chan struct{}),
		blockRestrictedWrites: atomic.Bool{},
	}
	ctx.blockRestrictedWrites.Store(false)

	_, parentHasDeadline := parent.Deadline()
	parentCanCancel := parent.Done() != nil && !isNonCancellingContext(parent)
	if ctx.hasDeadline || parentCanCancel || parentHasDeadline {
		go ctx.watchCancellation()
	}
	return ctx
}

func NewBifrostContextWithCancel(parent context.Context) (*BifrostContext, context.CancelFunc) {
	ctx := NewBifrostContext(parent, NoDeadline)
	return ctx, func() { ctx.Cancel() }
}

func (bc *BifrostContext) Root() *BifrostContext {

	for bc != nil && bc.valueDelegate != nil {
		bc = bc.valueDelegate
	}
	return bc
}

func (bc *BifrostContext) Grant() Grant {
	if bc == nil {
		return nil
	}
	root := bc.Root()
	if g := root.grant.Load(); g != nil {
		return *g
	}
	ancestor := root.inheritedGrant()
	if ancestor == nil {
		return nil
	}

	root.grant.CompareAndSwap(nil, &ancestor)
	if g := root.grant.Load(); g != nil {
		return *g
	}
	return nil
}

func (bc *BifrostContext) inheritedGrant() Grant {
	parent := bc.parent
	for parent != nil {
		ancestor, ok := parent.(*BifrostContext)
		if !ok {
			ancestor, ok = parent.Value(bifrostContextSelfKey{}).(*BifrostContext)
			if !ok {
				return nil
			}
		}
		ancestor = ancestor.Root()
		if g := ancestor.grant.Load(); g != nil {
			return *g
		}
		parent = ancestor.parent
	}
	return nil
}

func (bc *BifrostContext) BlockRestrictedWrites() {
	bc.blockRestrictedWrites.Store(true)
}

func (bc *BifrostContext) UnblockRestrictedWrites() {
	bc.blockRestrictedWrites.Store(false)
}

func (bc *BifrostContext) Cancel() {
	bc.cancel(context.Canceled)
}

func (bc *BifrostContext) watchCancellation() {
	var timer <-chan time.Time

	if effectiveDeadline, hasDeadline := bc.Deadline(); hasDeadline {
		duration := time.Until(effectiveDeadline)
		if duration <= 0 {

			bc.cancel(context.DeadlineExceeded)
			return
		}
		t := time.NewTimer(duration)
		defer t.Stop()
		timer = t.C
	}

	if isNonCancellingContext(bc.parent) {
		select {
		case <-timer:
			bc.cancel(context.DeadlineExceeded)
		case <-bc.done:

		}
		return
	}

	select {
	case <-bc.parent.Done():
		bc.cancel(bc.parent.Err())
	case <-timer:
		bc.cancel(context.DeadlineExceeded)
	case <-bc.done:

	}
}

func (bc *BifrostContext) cancel(err error) {
	bc.doneOnce.Do(func() {
		bc.errMu.Lock()
		bc.err = err
		bc.errMu.Unlock()
		close(bc.done)
	})
}

func (bc *BifrostContext) Deadline() (time.Time, bool) {
	if bc.valueDelegate != nil {
		return bc.valueDelegate.Deadline()
	}
	parentDeadline, parentHasDeadline := bc.parent.Deadline()

	if !bc.hasDeadline && !parentHasDeadline {
		return time.Time{}, false
	}

	if !bc.hasDeadline {
		return parentDeadline, true
	}

	if !parentHasDeadline {
		return bc.deadline, true
	}

	if bc.deadline.Before(parentDeadline) {
		return bc.deadline, true
	}
	return parentDeadline, true
}

func (bc *BifrostContext) Done() <-chan struct{} {
	return bc.done
}

func (bc *BifrostContext) Err() error {
	if bc.valueDelegate != nil {
		return bc.valueDelegate.Err()
	}
	bc.errMu.RLock()
	err := bc.err
	bc.errMu.RUnlock()

	if err == nil && bc.parent != nil && !isNonCancellingContext(bc.parent) {
		if perr := bc.parent.Err(); perr != nil {
			bc.cancel(perr)
			return perr
		}
	}
	return err
}

func (bc *BifrostContext) Value(key any) any {
	if bc.valueDelegate != nil {
		if bc.scopeSpanID != "" && key == BifrostContextKeySpanID {
			return bc.scopeSpanID
		}
		return bc.valueDelegate.Value(key)
	}

	if _, ok := key.(bifrostContextSelfKey); ok {
		return bc
	}
	bc.valuesMu.RLock()
	if val, ok := bc.userValues[key]; ok {
		bc.valuesMu.RUnlock()
		return val
	}
	bc.valuesMu.RUnlock()

	if bc.parent == nil {
		return nil
	}

	if isNonCancellingContext(bc.parent) {
		return nil
	}

	return bc.parent.Value(key)
}

func (bc *BifrostContext) SetValue(key, value any) {
	if bc.valueDelegate != nil {
		bc.valueDelegate.SetValue(key, value)
		return
	}

	if bc.blockRestrictedWrites.Load() && isReservedKey(key) {

		return
	}
	bc.valuesMu.Lock()
	defer bc.valuesMu.Unlock()
	if bc.userValues == nil {
		bc.userValues = make(map[any]any, 16)
	}
	bc.userValues[key] = value
}

func (bc *BifrostContext) setReservedValue(key, value any) {
	if bc.valueDelegate != nil {
		bc.valueDelegate.setReservedValue(key, value)
		return
	}
	bc.valuesMu.Lock()
	defer bc.valuesMu.Unlock()
	if bc.userValues == nil {
		bc.userValues = make(map[any]any, 16)
	}
	bc.userValues[key] = value
}

func (bc *BifrostContext) ReadyStreamCommitWatch() {
	bc.setReservedValue(BifrostContextKeyStreamCommitWatch, true)
}

func (bc *BifrostContext) SetRoutingInfoSnapshot(ri RoutingInfo) {
	bc.setReservedValue(BifrostContextKeyRoutingInfo, ri)
}

func (bc *BifrostContext) SetFallbackPinnedAPIKeyID(keyID string) {
	bc.setReservedValue(BifrostContextKeyAPIKeyID, keyID)
}

func (bc *BifrostContext) ClearValue(key any) {
	if bc.valueDelegate != nil {
		bc.valueDelegate.ClearValue(key)
		return
	}

	if bc.blockRestrictedWrites.Load() && isReservedKey(key) {

		return
	}
	bc.valuesMu.Lock()
	defer bc.valuesMu.Unlock()
	if bc.userValues == nil {

		if key != nil && !reflect.ValueOf(key).Comparable() {
			return
		}
		bc.userValues = make(map[any]any, 16)
	}

	bc.userValues[key] = nil
}

func (bc *BifrostContext) GetAndSetValue(key any, value any) any {
	if bc.valueDelegate != nil {
		return bc.valueDelegate.GetAndSetValue(key, value)
	}
	bc.valuesMu.Lock()
	defer bc.valuesMu.Unlock()

	if bc.blockRestrictedWrites.Load() && isReservedKey(key) {

		return bc.userValues[key]
	}
	if bc.userValues == nil {
		bc.userValues = make(map[any]any, 16)
	}
	oldValue := bc.userValues[key]
	bc.userValues[key] = value
	return oldValue
}

func (bc *BifrostContext) PauseStream() {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return
	}
	bc.setReservedValue(BifrostContextKeyStreamGated, true)
	tr.PauseStream(tid)
}

func (bc *BifrostContext) ResumeStream() {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return
	}
	bc.setReservedValue(BifrostContextKeyStreamGated, true)
	tr.ResumeStream(tid)
}

func (bc *BifrostContext) EndStream(err *BifrostError) {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return
	}
	bc.setReservedValue(BifrostContextKeyStreamGated, true)
	tr.EndStream(tid, err)
}

func (bc *BifrostContext) IsStreamEnded() bool {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return false
	}
	return tr.IsStreamEnded(tid)
}

func (bc *BifrostContext) IsStreamPaused() bool {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return false
	}
	return tr.IsStreamPaused(tid)
}

func (bc *BifrostContext) GetAccumulatedResponse() *BifrostResponse {
	tr, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	tid, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if tr == nil || tid == "" {
		return nil
	}
	return tr.GetAccumulatedResponse(tid)
}

func (bc *BifrostContext) GetModelInfo(provider ModelProvider, model string) *Model {
	catalog, _ := bc.Value(BifrostContextKeyModelCatalog).(ModelInfoProvider)
	if catalog == nil || model == "" {
		return nil
	}
	return catalog.GetModelInfo(provider, model)
}

func (bc *BifrostContext) AppendRoutingEngineLog(engineName string, level LogLevel, message string) {
	entry := RoutingEngineLogEntry{
		Engine:    engineName,
		Level:     level,
		Message:   message,
		Timestamp: time.Now().UnixMilli(),
	}
	AppendToContextList(bc, BifrostContextKeyRoutingEngineLogs, entry)
}

func AppendToContextList[T comparable](ctx *BifrostContext, key BifrostContextKey, value T) {
	if ctx == nil {
		return
	}
	existingValues, ok := ctx.Value(key).([]T)
	if !ok {
		existingValues = []T{}
	}
	if slices.Contains(existingValues, value) {
		return
	}
	ctx.SetValue(key, append(existingValues, value))
}

func (bc *BifrostContext) WithPluginScope(name *string) *BifrostContext {

	if bc.pluginLogs.Load() == nil {
		newStore := pluginLogStorePool.Get().(*pluginLogStore)
		if !bc.pluginLogs.CompareAndSwap(nil, newStore) {

			pluginLogStorePool.Put(newStore)
		}
	}

	scoped := &BifrostContext{
		parent:        bc.parent,
		done:          bc.done,
		pluginScope:   name,
		valueDelegate: bc,
	}
	scoped.pluginLogs.Store(bc.pluginLogs.Load())
	return scoped
}

func (bc *BifrostContext) WithPluginScopeSpan(name *string, spanID string) *BifrostContext {
	scoped := bc.WithPluginScope(name)
	scoped.scopeSpanID = spanID
	return scoped
}

func (bc *BifrostContext) ReleasePluginScope() {
	if bc.valueDelegate == nil {
		return
	}
	bc.pluginLogs.Store(nil)
}

func (bc *BifrostContext) HasPluginLogs() bool {
	if bc.valueDelegate != nil {
		return false
	}
	store := bc.pluginLogs.Load()
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.logs) > 0
}

func (bc *BifrostContext) DrainPluginLogs() []PluginLogEntry {
	if bc.valueDelegate != nil {
		return nil
	}
	store := bc.pluginLogs.Load()
	if store == nil {
		return nil
	}
	bc.pluginLogs.Store(nil)

	store.mu.Lock()
	logs := store.logs

	store.logs = make([]PluginLogEntry, 0, 8)
	store.mu.Unlock()

	pluginLogStorePool.Put(store)

	if len(logs) == 0 {
		return nil
	}
	return logs
}
