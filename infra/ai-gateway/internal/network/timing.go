package network

import (
	"errors"
	"time"
)

// One operation envelope, shared with the plugin and deployment projection.
// Earlier parent cancellation always wins over any derived deadline.
type timeoutBudget struct{ Operation, RPC, Authority, Auth, Redeem, Grace time.Duration }

func networkTimeoutBudget(ms int) (timeoutBudget, error) {
	if ms == 0 {
		ms = 30000
	}
	if ms < 10000 || ms > 120000 {
		return timeoutBudget{}, errors.New("network: operation budget must be in [10000,120000] ms")
	}
	operation := time.Duration(ms) * time.Millisecond
	auth := operation / 3
	return timeoutBudget{Operation: operation, RPC: operation * 2 / 3, Authority: operation / 6, Auth: auth, Redeem: auth - 2*time.Second, Grace: operation / 2}, nil
}
