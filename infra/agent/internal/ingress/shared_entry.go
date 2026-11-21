package ingress

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
		s.cfg.Logger.Warn("shared identity step failed", "step", "header-count", "count", len(values))
		return nil, nil, errSharedInvalid
	}
	c, err := sharedVerify(values[0], e.Keys, true, s.cfg.Now().Unix())
	if err != nil {
		s.cfg.Logger.Warn("shared identity step failed", "step", "verify", "reason", err.Error())
		return nil, nil, err
	}
	b := c["binding"].(map[string]any)
	if b["physicalInstanceId"] != id || (b["mode"] != "shared" && b["mode"] != "dedicated") || c["method"] != r.Method || c["canonicalPath"] != path {
		s.cfg.Logger.Warn("shared identity step failed", "step", "binding", "instance", b["physicalInstanceId"] == id, "method", c["method"] == r.Method, "path", c["canonicalPath"] == path)
		return nil, nil, errSharedInvalid
	}
	if !sharedBrowserRequest(r, path) || (b["mode"] == "dedicated" && strings.HasPrefix(path, "/auriga/shared/")) || (b["mode"] == "shared" && strings.HasPrefix(path, "/auriga/dedicated/")) {
		return nil, nil, errSharedInvalid
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop, err := e.Authority.Watch(ctx, b, sharedString(c["platformSessionId"]), cancel)
	if err != nil || stop == nil {
		cancel()
		return nil, nil, errors.New("common.unavailable")
	}
	cleanup := func() { stop(); cancel() }
	if err = e.Authority.Check(ctx, b, sharedString(c["platformSessionId"])); err != nil || ctx.Err() != nil {
		s.cfg.Logger.Warn("shared identity step failed", "step", "authority", "error", fmt.Sprint(err))
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
	checked := r.WithContext(ctx)
	checked.Header = r.Header.Clone()
	// Both managed modes use the current E/W binding, never owner credentials.
	for key := range checked.Header {
		if strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "Authorization") {
			delete(checked.Header, key)
		}
	}
	return checked, cleanup, nil
}

func sharedBrowserRequest(r *http.Request, canonical string) bool {
	decoded, err := url.PathUnescape(canonical)
	if err != nil {
		return false
	}
	path := strings.ToLower(decoded)
	// Native Connection upgrade admission is owner based and has no public
	// override. Managed browsers use their explicit HTTP RPC/NDJSON carriers.
	if strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/connect") || r.Header.Get("Upgrade") != "" {
		return false
	}
	for _, line := range r.Header.Values("Connection") {
		for _, token := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return false
			}
		}
	}
	return true
}
