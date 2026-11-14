package msgnet

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"slices"
)

// TransportIsolation inspects an actual local message deny/disconnect mechanism;
// a remotely asserted boolean cannot implement this port.
type TransportIsolation interface {
	Isolated(context.Context) (bool, error)
}

func (i *Installer) InstallTransport(ctx context.Context, raw []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == nil || i.options.ResolveAuthority == nil || i.options.Schemas == nil {
		return fmt.Errorf("control and frozen transport verifier required")
	}
	if len(raw) > 32<<20 {
		return fmt.Errorf("transport bundle too large")
	}
	var b api.SignedTransportTrustBundle
	if e := strict(raw, &b); e != nil {
		return e
	}
	a, e := i.options.ResolveAuthority(ctx, b.Payload.AuthorityId, b.Payload.AuthorityDigest)
	if e != nil {
		return e
	}
	if a.Payload.PublicKeySpki == i.options.Pin.SigningSpki {
		return fmt.Errorf("runtime authority reuses node confirmation key")
	}
	root, e := x509.MarshalPKIXPublicKey(i.options.RootKey)
	if e != nil {
		return e
	}
	saved := i.state.Transport
	// A K2 public bundle is a high-water mark, never fresh authorization.
	if saved.Bundle == nil && i.state.Bundle != nil {
		saved.Bundle = i.state.Bundle
		saved.CAs = map[string]api.TransportCa{}
		for _, ca := range i.state.Bundle.Payload.Cas {
			saved.CAs[ca.CaId] = ca
		}
	}
	consumer := trust.TransportConsumer{Schemas: i.options.Schemas, Domain: trust.TransportDomain{RootSPKI: base64.RawURLEncoding.EncodeToString(root), Tenant: i.options.Tenant, Realm: i.options.Realm}, RootKid: i.options.RootKid, State: saved, Now: i.options.Now, Source: i.options.TransportSource}
	consumer.Save = func(state trust.TransportState) error {
		next := *i.state
		next.Transport = state
		next.Bundle = state.Bundle
		if e := i.persist(&next); e != nil {
			return e
		}
		i.state = &next
		return nil
	}
	if e = consumer.UpdateControl(i.state.Package.Control); e != nil {
		return e
	}
	if slices.Contains(i.state.Package.Control.Payload.RevokedInstallationIds, i.options.Pin.IdentityId) {
		return fmt.Errorf("node confirmation identity revoked")
	}
	isolated := false
	if local, ok := i.options.Backend.(TransportIsolation); ok {
		isolated, e = local.Isolated(ctx)
		if e != nil {
			return e
		}
	}
	if e = consumer.Accept(ctx, b, a, isolated); e != nil {
		return e
	}
	// A public relay/cache is never sufficient to claim TLS/new-chain READY.
	return i.options.Backend.ApplyTransport(ctx, b)
}
