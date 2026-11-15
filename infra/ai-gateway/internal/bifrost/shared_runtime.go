package bifrost

import (
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
)

// SharedRuntime composes the production relay and persistent attribution ledger
// with the supplied online W/P authority gate. The caller owns Store lifecycle.
type SharedRuntime struct {
	*sharedmodel.SharedProvider
	relay *SharedRelay
}

func NewSharedRuntime(gate *sharedauth.Gate, database *store.Store, origin, keyFile string, policy MemberPolicy) (*SharedRuntime, error) {
	ledger, err := sharedmodel.NewPostgresSharedLedger(database)
	if err != nil {
		return nil, err
	}
	relay, err := NewSharedRelay(origin, keyFile, policy)
	if err != nil {
		return nil, err
	}
	provider, err := sharedmodel.NewSharedProvider(gate, relay, ledger)
	if err != nil {
		relay.Close()
		return nil, err
	}
	return &SharedRuntime{SharedProvider: provider, relay: relay}, nil
}

func (r *SharedRuntime) Close() { r.relay.Close() }
