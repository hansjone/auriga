package network

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"net"
	"net/http"
	"time"
)

func (r *projectionRuntime) serverTLS() (*tls.Config, error) {
	return r.serverTLSFor(r.snapshot.Load())
}
func (r *projectionRuntime) serverTLSFor(snap *projection.Snapshot) (*tls.Config, error) {
	if snap == nil || r.reader.Withdrawn(time.Now()) {
		return nil, errors.New("network: TLS projection pending")
	}
	if e := r.checkLocalSnapshot(snap); e != nil {
		return nil, e
	}
	cert, ok := snap.TLS.Leaves["submit"]
	if !ok || cert.Leaf == nil || !cert.Leaf.NotAfter.After(time.Now()) {
		return nil, errors.New("network: submit TLS leaf unavailable")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientCAs: snap.TLS.ClientRoots, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
func (r *Runtime) startProjectedSubmit(ctx context.Context, c *SubmitHTTPConfig) error {
	if c.Listen == "" {
		return errors.New("network: submit address required")
	}
	if _, e := r.projection.serverTLS(); e != nil {
		return e
	}
	l, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return e
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		snap := r.projection.snapshot.Load()
		config, e := r.projection.serverTLSFor(snap)
		if e == nil && snap != nil {
			r.projection.handshakeConns.Store(h.Conn, snap.Selector)
		}
		return config, e
	}}
	budget := r.budget
	if budget.Operation == 0 {
		budget, _ = networkTimeoutBudget(0)
	}
	r.submitHTTP = &http.Server{Handler: r.SubmitHandler(), TLSConfig: cfg, ReadHeaderTimeout: budget.Authority, ReadTimeout: budget.Operation, WriteTimeout: budget.Operation, IdleTimeout: 2 * budget.Operation, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
	r.submitHTTP.ConnState = func(c net.Conn, state http.ConnState) {
		tc, ok := c.(*tls.Conn)
		if !ok {
			return
		}
		raw := tc.NetConn()
		switch state {
		case http.StateActive:
			if value, ok := r.projection.handshakeConns.LoadAndDelete(raw); ok {
				cs := tc.ConnectionState()
				if cs.HandshakeComplete && len(cs.VerifiedChains) > 0 {
					select {
					case r.projection.handshakes <- handshakeObservation{Selector: value.(api.LocalProjectionSelector), Role: "submit"}:
					default:
					}
				}
			}
		case http.StateClosed, http.StateHijacked:
			r.projection.handshakeConns.Delete(raw)
		}
	}
	r.done.Go(func() {
		if e := r.submitHTTP.ServeTLS(l, "", ""); e != nil && !errors.Is(e, http.ErrServerClosed) {
			select {
			case r.Errors <- errors.New("network: projected submit listener stopped"):
			default:
			}
		}
	})
	return nil
}
