package ingress

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
)

// SharedIngressAuthority uses W Resolve and IAM; Watch cancels streams on revoke or outage.
type SharedIngressAuthority interface {
	Check(context.Context, map[string]any, string) error
	Watch(context.Context, map[string]any, string, context.CancelFunc) (func(), error)
}
type SharedIngress struct {
	Keys      map[string]ed25519.PublicKey
	Authority SharedIngressAuthority
	Replay    *SharedIdentityReplayStore
}

func (s *Server) sharedRequest(r *http.Request, id, path string) (*http.Request, func(), error) {
	e := s.cfg.Shared
	if e == nil || e.Authority == nil {
		return nil, nil, errors.New("common.unavailable")
	}
	var values []string
	for k, v := range r.Header {
		if strings.EqualFold(k, identityHeader) {
			values = append(values, v...)
		}
	}
	if len(values) != 1 {
		return nil, nil, sharedInvalid
	}
	c, err := sharedVerify(values[0], e.Keys, true, s.cfg.Now().Unix())
	if err != nil {
		return nil, nil, err
	}
	b := c["binding"].(map[string]any)
	if b["physicalInstanceId"] != id || b["mode"] != "shared" || c["method"] != r.Method || c["canonicalPath"] != path {
		return nil, nil, sharedInvalid
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop, err := e.Authority.Watch(ctx, b, sharedString(c["platformSessionId"]), cancel)
	if err != nil || stop == nil {
		cancel()
		return nil, nil, errors.New("common.unavailable")
	}
	cleanup := func() { stop(); cancel() }
	if err = e.Authority.Check(ctx, b, sharedString(c["platformSessionId"])); err != nil || ctx.Err() != nil {
		cleanup()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("shared.revoked")
	}
	if err := e.Replay.consume(ctx, sharedString(c["jti"]), sharedInt(c["expiresAt"]), s.cfg.Now().Unix()); err != nil {
		cleanup()
		return nil, nil, err
	}
	return r.WithContext(ctx), cleanup, nil
}
