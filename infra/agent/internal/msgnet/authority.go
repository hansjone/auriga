package msgnet

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// PublicAuthorityResolver uses the fixed public HTTPS bootstrap, without a
// service JWT or projection credentials. The installer independently checks
// the returned root signature, scope and latest root-control authorization.
func PublicAuthorityResolver(origin string, transport *http.Transport, schemas *trust.Schemas) (func(context.Context, string, string) (api.SignedRuntimeAuthority, error), error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || transport == nil || transport.Proxy != nil || transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify || schemas == nil {
		return nil, fmt.Errorf("fixed verified public HTTPS bootstrap required")
	}
	tr := transport.Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		tr.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u.Path = "/api/collab/v2/network/bootstrap/keys"
	return func(ctx context.Context, id, digest string) (api.SignedRuntimeAuthority, error) {
		var p api.Publication
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return p.Authority, e
		}
		r.Header.Set("Cache-Control", "no-store")
		resp, e := client.Do(r)
		if e != nil {
			return p.Authority, e
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return p.Authority, fmt.Errorf("public bootstrap unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
		if e != nil {
			return p.Authority, e
		}
		if len(raw) > 32<<20 {
			return p.Authority, fmt.Errorf("bootstrap response too large")
		}
		if e = schemas.Decode("Publication", raw, &p); e != nil {
			return p.Authority, e
		}
		actual, e := trust.ObjectDigest(p.Authority)
		if e != nil || p.Authority.Payload.AuthorityId != id || actual != digest {
			return api.SignedRuntimeAuthority{}, fmt.Errorf("bootstrap authority mismatch")
		}
		return p.Authority, nil
	}, nil
}
