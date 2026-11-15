package modelgate

import "github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"

// Preserve the original N adapter API while the concrete shared model runtime
// lives outside device authentication (which itself consumes the Bifrost client).
type SharedRequest = sharedmodel.SharedRequest
type SharedCall = sharedmodel.SharedCall
type SharedUsage = sharedmodel.SharedUsage
type SharedRelay = sharedmodel.SharedRelay
type SharedLedger = sharedmodel.SharedLedger
type SharedProvider = sharedmodel.SharedProvider

var NewSharedProvider = sharedmodel.NewSharedProvider
