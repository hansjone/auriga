package anthropic

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
)

var anthropicStreamDeltaPromptUsageLedgerSeq atomic.Uint64
var anthropicStreamDeltaPromptUsageLastSweep atomic.Int64

type anthropicStreamDeltaPromptUsageLedger struct {
	owner  *schemas.BifrostContext
	mu     sync.Mutex
	absent map[int]anthropicPromptCounterBit
}

var anthropicStreamDeltaPromptUsageLedgers sync.Map
var anthropicPromptCounterKeys = []struct {
	bit anthropicPromptCounterBit
	key string
}{
	{anthropicPromptCounterInputTokens, "input_tokens"},
	{anthropicPromptCounterCacheCreationInputTokens, "cache_creation_input_tokens"},
	{anthropicPromptCounterCacheReadInputTokens, "cache_read_input_tokens"},
	{anthropicPromptCounterCacheCreation, "cache_creation"},
}

type anthropicUsageWire AnthropicUsage
type anthropicPromptCounterBit = uint8

const (
	anthropicPromptCounterInputTokens anthropicPromptCounterBit = 1 << iota
	anthropicPromptCounterCacheCreationInputTokens
	anthropicPromptCounterCacheReadInputTokens
	anthropicPromptCounterCacheCreation
)

func openAnthropicStreamDeltaPromptUsageLedger(ctx *schemas.BifrostContext) *anthropicStreamDeltaPromptUsageLedger {
	if ctx == nil {
		return nil
	}
	closeAnthropicStreamDeltaPromptUsageLedger(ctx)
	ledger := &anthropicStreamDeltaPromptUsageLedger{
		owner:  ctx.Root(),
		absent: make(map[int]anthropicPromptCounterBit, 1),
	}
	id := "anthropic-stream-delta-prompt-usage-" + strconv.FormatUint(anthropicStreamDeltaPromptUsageLedgerSeq.Add(1), 10)
	anthropicStreamDeltaPromptUsageLedgers.Store(id, ledger)
	ctx.SetValue(schemas.BifrostContextKeyAnthropicStreamDeltaPromptUsage, id)

	sweepFinishedAnthropicStreamDeltaPromptUsageLedgers()
	return ledger
}

const anthropicStreamDeltaPromptUsageSweepInterval = time.Second

func sweepFinishedAnthropicStreamDeltaPromptUsageLedgers() {
	now := time.Now().UnixNano()
	last := anthropicStreamDeltaPromptUsageLastSweep.Load()
	if now-last < int64(anthropicStreamDeltaPromptUsageSweepInterval) ||
		!anthropicStreamDeltaPromptUsageLastSweep.CompareAndSwap(last, now) {
		return
	}
	anthropicStreamDeltaPromptUsageLedgers.Range(func(key, value any) bool {
		ledger, _ := value.(*anthropicStreamDeltaPromptUsageLedger)
		if ledger == nil || ledger.owner == nil || ledger.owner.Err() != nil {
			anthropicStreamDeltaPromptUsageLedgers.Delete(key)
		}
		return true
	})
}

func closeAnthropicStreamDeltaPromptUsageLedger(ctx *schemas.BifrostContext) {
	if ctx == nil {
		return
	}
	id, _ := ctx.Value(schemas.BifrostContextKeyAnthropicStreamDeltaPromptUsage).(string)
	if id == "" {
		return
	}
	if ledger := loadAnthropicStreamDeltaPromptUsageLedger(id); ledger == nil || ledger.owner != ctx.Root() {
		return
	}
	anthropicStreamDeltaPromptUsageLedgers.Delete(id)
}

func loadAnthropicStreamDeltaPromptUsageLedger(id string) *anthropicStreamDeltaPromptUsageLedger {
	entry, ok := anthropicStreamDeltaPromptUsageLedgers.Load(id)
	if !ok {
		return nil
	}
	ledger, _ := entry.(*anthropicStreamDeltaPromptUsageLedger)
	return ledger
}

func ownedAnthropicStreamDeltaPromptUsageLedger(ctx *schemas.BifrostContext) *anthropicStreamDeltaPromptUsageLedger {
	if ctx == nil {
		return nil
	}
	id, _ := ctx.Value(schemas.BifrostContextKeyAnthropicStreamDeltaPromptUsage).(string)
	if id == "" {
		return nil
	}
	ledger := loadAnthropicStreamDeltaPromptUsageLedger(id)
	if ledger == nil || ledger.owner == nil || ledger.owner != ctx.Root() {
		return nil
	}
	return ledger
}

func (l *anthropicStreamDeltaPromptUsageLedger) record(sequenceNumber int, absent anthropicPromptCounterBit) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.absent[sequenceNumber] = absent
}

func absentAnthropicPromptCounters(event []byte) anthropicPromptCounterBit {
	usage := providerUtils.GetJSONField(event, "usage")
	if !usage.Exists() || !usage.IsObject() {
		return 0
	}
	var absent anthropicPromptCounterBit
	for _, counter := range anthropicPromptCounterKeys {
		if reported := usage.Get(counter.key); !reported.Exists() || reported.Type == gjson.Null {
			absent |= counter.bit
		}
	}
	return absent
}

func recordAnthropicStreamDeltaPromptUsage(ctx *schemas.BifrostContext, sequenceNumber int, event []byte) {
	if ctx == nil {
		return
	}
	ledger := ownedAnthropicStreamDeltaPromptUsageLedger(ctx)
	if ledger == nil {
		if ledger = openAnthropicStreamDeltaPromptUsageLedger(ctx); ledger == nil {
			return
		}
	}
	ledger.record(sequenceNumber, absentAnthropicPromptCounters(event))
}

func (u AnthropicUsage) MarshalJSON() ([]byte, error) {
	data, err := sonic.Marshal(anthropicUsageWire(u))
	if err != nil {
		return nil, err
	}
	if u.absentPromptCounters == 0 {
		return data, nil
	}
	for _, counter := range anthropicPromptCounterKeys {
		if u.absentPromptCounters&counter.bit == 0 {
			continue
		}
		if data, err = providerUtils.DeleteJSONField(data, counter.key); err != nil {
			return nil, err
		}
	}
	return data, nil
}
