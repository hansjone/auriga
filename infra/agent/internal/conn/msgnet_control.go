package conn

import "context"

// MsgnetControl is the local durable verifier/installer port. Only public signed
// bytes cross NodeControl; projection secrets must use the sealed bootstrap or
// component-scoped mTLS material endpoint. The implementation owns root trust,
// anti-rollback persistence and message-channel isolation, not this transport.
type MsgnetControl interface {
	InstallControl(context.Context, []byte) ([]byte, error)
	InstallTransport(context.Context, []byte) error
}
