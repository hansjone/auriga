package msgnet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// HTTPSource has a deployment-pinned HTTPS origin and a separate authenticated
// internal listener. It never accepts URLs from a wire object or follows redirects.
type HTTPSource struct {
	origin  *url.URL
	client  *http.Client
	schemas *Schemas
	token   func(context.Context) (string, error)
}

func (s *HTTPSource) Close() { s.client.CloseIdleConnections() }

func NewHTTPSource(origin string, client *http.Client, schemas *Schemas, token func(context.Context) (string, error)) (*HTTPSource, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path != "" && u.Path != "/" || client == nil || schemas == nil || token == nil {
		return nil, Fault("invalid_chain")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.InsecureSkipVerify || tr.Proxy != nil || len(tr.TLSClientConfig.Certificates) == 0 && tr.TLSClientConfig.GetClientCertificate == nil {
		return nil, Fault("scope_denied")
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Timeout = 10 * time.Second
	return &HTTPSource{origin: u, client: &c, schemas: schemas, token: token}, nil
}

func (s *HTTPSource) read(ctx context.Context, method, path, definition string, request any, out any) error {
	u := *s.origin
	u.Path = "/api/collab/v2/network" + path
	var body []byte
	if request != nil {
		var e error
		body, e = Bytes(request)
		if e != nil {
			return e
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if e != nil {
		return e
	}
	// Public root control must continue working when a service JWT expires.
	// Historical/confirmation ledger queries retain their separate JWT boundary.
	if path != "/bootstrap/controls" {
		token, e := s.token(ctx)
		if e != nil {
			return e
		}
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return Fault("scope_denied")
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Cache-Control", "no-store")
	if request != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, e := s.client.Do(r)
	if e != nil {
		return fmt.Errorf("message network ledger unavailable: %w", e)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Fault("projection_pending")
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-store") {
		return Fault("invalid_chain")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil {
		return e
	}
	if len(raw) > 32<<20 {
		return Fault("invalid_chain")
	}
	return s.schemas.Decode(definition, raw, out)
}
func (s *HTTPSource) Predecessor(ctx context.Context, digest string) (api.TransportPredecessor, error) {
	var v api.TransportPredecessor
	if !hash.MatchString(digest) {
		return v, Fault("invalid_chain")
	}
	e := s.read(ctx, http.MethodGet, "/transport/history/"+digest, "TransportPredecessor", nil, &v)
	return v, e
}
func (s *HTTPSource) Transition(ctx context.Context, digest string) (api.TransportTransitionProof, error) {
	var v api.TransportTransitionProof
	if !hash.MatchString(digest) {
		return v, Fault("invalid_chain")
	}
	e := s.read(ctx, http.MethodGet, "/transport/transitions/"+digest, "TransportTransitionProof", nil, &v)
	return v, e
}
func (s *HTTPSource) Confirmation(ctx context.Context, q api.TransportConfirmationQuery) (api.SignedTransportConfirmation, error) {
	var v api.SignedTransportConfirmation
	raw, e := Bytes(q)
	if e != nil {
		return v, e
	}
	var check api.TransportConfirmationQuery
	if e = s.schemas.Decode("TransportConfirmationQuery", raw, &check); e != nil {
		return v, e
	}
	e = s.read(ctx, http.MethodPost, "/transport/confirmation", "SignedTransportConfirmation", q, &v)
	return v, e
}
func (s *HTTPSource) Control(ctx context.Context) (api.SignedRootControl, error) {
	var v api.SignedRootControl
	e := s.read(ctx, http.MethodGet, "/bootstrap/controls", "SignedRootControl", nil, &v)
	return v, e
}
