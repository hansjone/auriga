package admin

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

// RelayReader is the member-only read side. It never calls a control plane in a
// tenant transaction. Capture is preparation, not permission to submit upstream.
type RelayReader struct {
	db       *store.Store
	currency string
	now      func() time.Time
}

func (r *RelayReader) Currency() string { return r.currency }

func NewRelayReader(db *store.Store, currency string, now func() time.Time) (*RelayReader, error) {
	if db == nil || !currencyPattern.MatchString(currency) || now == nil {
		return nil, failure("common.validation_failed")
	}
	return &RelayReader{db: db, currency: currency, now: now}, nil
}

type RelayVersions struct {
	ModelRevision, ProviderRevision, CredentialRevision int64
	ProviderID, CredentialID                            string
	CipherVersion                                       int
}
type RelayConstraints struct {
	ContextWindow, MaxOutputTokens int64
	Price                          api.Price
}

// RelayTarget contains only the fixed internal dispatch configuration; it must
// never be serialized as a public model. Dispatch still needs trusted policy,
// metering, durable budget admission and DNS/redirect enforcement in the handler.
type RelayTarget struct {
	BaseURL, UpstreamModel                                                                                  string
	ConnectTimeoutSeconds, FirstByteTimeoutSeconds, IdleTimeoutSeconds, TotalTimeoutSeconds, MaxConcurrency int64
}

// All reference-bearing fields stay private; accessors return independent values.
type RelaySnapshot struct {
	owner             *RelayReader
	tenant, account   string
	model             api.Model
	provider          api.Provider
	versions          RelayVersions
	ciphertext        []byte
	captured, created time.Time
}

func (s RelaySnapshot) Versions() RelayVersions { return s.versions }
func (s RelaySnapshot) Constraints() RelayConstraints {
	return RelayConstraints{ContextWindow: s.model.ContextWindow, MaxOutputTokens: s.model.MaxOutputTokens, Price: s.model.Price}
}
func (s RelaySnapshot) ModelID() string  { return s.model.Id }
func (s RelaySnapshot) String() string   { return "AI relay snapshot" }
func (s RelaySnapshot) GoString() string { return s.String() }

func relayError(err error) error {
	if err == nil {
		return nil
	}
	var e failure
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return failure("ai.model_not_allowed")
	}
	return failure("ai.budget_unavailable")
}
func relaySubject(tenant, account string) bool {
	return uuidPattern.MatchString(tenant) && tenant != "00000000-0000-0000-0000-000000000000" && uuidPattern.MatchString(account)
}
func (r *RelayReader) Capture(ctx context.Context, tenant, account, model string) (RelaySnapshot, error) {
	if !relaySubject(tenant, account) || !modelPattern.MatchString(model) {
		return RelaySnapshot{}, failure("common.validation_failed")
	}
	var s *RelaySnapshot
	err := r.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		s, e = r.capture(ctx, tx, tenant, account, model)
		return e
	})
	if err != nil {
		return RelaySnapshot{}, relayError(err)
	}
	return *s, nil
}

func memberACL(raw []byte, account string) error {
	var grants api.ModelGrants
	if decode(raw, &grants) != nil || validateGrants(grants) != nil {
		return failure("ai.budget_unavailable")
	}
	match := false
	for _, g := range grants.Items {
		// Even a member match cannot establish the meaning of a mixed ACL (L14).
		if g.Kind != "member" {
			return failure("ai.budget_unavailable")
		}
		match = match || g.Id == account
	}
	if !match {
		return failure("ai.model_not_allowed")
	}
	return nil
}

func (r *RelayReader) capture(ctx context.Context, tx pgx.Tx, tenant, account, id string) (*RelaySnapshot, error) {
	s := &RelaySnapshot{owner: r, tenant: tenant, account: account}
	var grants []byte
	var providerID, credentialState string
	var validPrice bool
	// One joined statement pins the three rows before using the existing document
	// readers. SHARE blocks every concurrent writer, including revoke/rotation; no
	// remote work or callback runs while these short-lived locks are held. Check
	// nullable JSON rates explicitly: generated numeric fields decode null as 0.
	err := tx.QueryRow(ctx, `SELECT m.provider_id::text,m.grants,m.revision,m.created_at,p.revision,
 jsonb_typeof(m.document->'price'->'inputMicrosPerMillionTokens')='number' AND jsonb_typeof(m.document->'price'->'outputMicrosPerMillionTokens')='number',
 c.id::text,c.state,c.revision,c.cipher_version,c.ciphertext
 FROM ai.model m JOIN ai.provider p ON p.tenant_id=m.tenant_id AND p.id=m.provider_id
 JOIN ai.credential c ON c.tenant_id=p.tenant_id AND c.provider_id=p.id AND c.id::text=p.document->>'activeCredentialId'
 WHERE m.tenant_id=$1 AND m.id=$2 FOR SHARE OF m,p,c`, tenant, id).Scan(
		&providerID, &grants, &s.versions.ModelRevision, &s.created, &s.versions.ProviderRevision, &validPrice,
		&s.versions.CredentialID, &credentialState, &s.versions.CredentialRevision, &s.versions.CipherVersion, &s.ciphertext)
	if err != nil {
		return nil, relayError(err)
	}
	if !validPrice {
		return nil, failure("ai.budget_unavailable")
	}
	if err = memberACL(grants, account); err != nil {
		return nil, err
	}
	s.model, err = loadModel(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, relayError(err)
	}
	s.provider, err = loadProvider(ctx, tx, tenant, providerID, false)
	if err != nil {
		return nil, relayError(err)
	}
	m, p := s.model, s.provider
	if m.Id != id || m.ProviderId != providerID || p.Id != providerID || !m.Enabled || !p.Enabled || credentialState != "active" || p.ActiveCredentialId.Get() == nil || *p.ActiveCredentialId.Get() != s.versions.CredentialID {
		return nil, failure("ai.model_not_allowed")
	}
	s.versions.ProviderID = providerID
	if validateModel(m) != nil || validateProvider(p) != nil || m.Revision != s.versions.ModelRevision || p.Revision != s.versions.ProviderRevision || s.versions.CipherVersion < 1 || len(s.ciphertext) == 0 {
		return nil, failure("ai.budget_unavailable")
	}
	s.captured = r.now()
	if m.Price.Currency != r.currency || m.Price.EffectiveAt.After(s.captured) {
		return nil, failure("ai.budget_unavailable")
	}
	return s, nil
}

// Catalog is a non-reserving view. Each model is read in a short transaction;
// being listed never substitutes for a fresh Capture/Recheck at admission.
func (r *RelayReader) Catalog(ctx context.Context, tenant, account string) (api.ModelList, error) {
	out := api.ModelList{Object: "list", Data: []api.ModelListDataInner{}}
	if !relaySubject(tenant, account) {
		return out, failure("common.validation_failed")
	}
	var ids []string
	err := r.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT id FROM ai.model WHERE tenant_id=$1 ORDER BY id", tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				return e
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return out, relayError(err)
	}
	for _, id := range ids {
		s, e := r.Capture(ctx, tenant, account, id)
		if e != nil {
			if e.Error() == "ai.model_not_allowed" {
				continue
			}
			return api.ModelList{}, e
		}
		out.Data = append(out.Data, api.ModelListDataInner{Id: s.model.Id, Object: "model", Created: s.created.Unix(), OwnedBy: "auriga"})
	}
	return out, nil
}

// Recheck(false) rejects ANY revision change before new admission. For an
// existing stream, true permits only normal key rotation, never key replacement
// in this snapshot. Its conservative overlap ends 60s after initial capture
// (hence no later than 60s after rotation). The handler also enforces token expiry
// and cancels on failed recheck/emergency revoke; this is not a stream manager.
func (r *RelayReader) Recheck(ctx context.Context, s RelaySnapshot, inFlight bool) error {
	if s.owner != r {
		return failure("common.validation_failed")
	}
	if s.model.Price.EffectiveAt.After(r.now()) {
		return failure("ai.budget_unavailable")
	}
	err := r.db.Transaction(ctx, s.tenant, func(tx pgx.Tx) error {
		var modelRev, providerRev, keyRev int64
		var grants, raw, ciphertext []byte
		var providerID, state string
		var cv int
		err := tx.QueryRow(ctx, `SELECT m.revision,m.provider_id::text,m.grants,p.document,p.revision,c.state,c.revision,c.cipher_version,c.ciphertext
 FROM ai.model m JOIN ai.provider p ON p.tenant_id=m.tenant_id AND p.id=m.provider_id
 JOIN ai.credential c ON c.tenant_id=p.tenant_id AND c.provider_id=p.id AND c.id=$3
 JOIN ai.credential active ON active.tenant_id=p.tenant_id AND active.provider_id=p.id AND active.id::text=p.document->>'activeCredentialId' AND active.state='active'
 WHERE m.tenant_id=$1 AND m.id=$2 FOR SHARE OF m,p,c,active`, s.tenant, s.model.Id, s.versions.CredentialID).Scan(&modelRev, &providerID, &grants, &raw, &providerRev, &state, &keyRev, &cv, &ciphertext)
		if err != nil {
			return err
		}
		if err = memberACL(grants, s.account); err != nil {
			return err
		}
		if modelRev != s.versions.ModelRevision || providerID != s.versions.ProviderID || cv != s.versions.CipherVersion || !bytes.Equal(ciphertext, s.ciphertext) {
			return failure("ai.model_not_allowed")
		}
		var p api.Provider
		if err = readDocument(raw, providerRev, &p); err != nil {
			return err
		}
		if !p.Enabled {
			return failure("ai.model_not_allowed")
		}
		if providerRev == s.versions.ProviderRevision && keyRev == s.versions.CredentialRevision && state == "active" && p.ActiveCredentialId.Get() != nil && *p.ActiveCredentialId.Get() == s.versions.CredentialID {
			return nil
		}
		if !inFlight || state != "retiring" || providerRev != s.versions.ProviderRevision+1 || keyRev != s.versions.CredentialRevision+1 || !r.now().Before(s.captured.Add(60*time.Second)) || p.ActiveCredentialId.Get() == nil || *p.ActiveCredentialId.Get() == s.versions.CredentialID {
			return failure("ai.model_not_allowed")
		}
		// Ignore only the fields a normal activation changes. All endpoint/policy
		// changes still invalidate the stream, even if its credential is retiring.
		p.ActiveCredentialId = s.provider.ActiveCredentialId
		p.Revision = s.provider.Revision
		p.Etag = s.provider.Etag
		a, e := p.MarshalJSON()
		if e != nil {
			return e
		}
		b, e := s.provider.MarshalJSON()
		if e != nil {
			return e
		}
		if !bytes.Equal(a, b) {
			return failure("ai.model_not_allowed")
		}
		return nil
	})
	return relayError(err)
}

// WithCredential is an internal outbound seam, not a secret-reading endpoint.
// Call only after trusted model policy/metering and durable budget authorization.
// The callback runs AFTER PostgreSQL commit; plaintext exists only for its call.
func (r *RelayReader) WithCredential(ctx context.Context, s RelaySnapshot, c *Cipher, send func(RelayTarget, []byte) error) error {
	if c == nil || send == nil {
		return failure("common.validation_failed")
	}
	if err := r.Recheck(ctx, s, false); err != nil {
		return err
	}
	secret, err := c.Decrypt(s.ciphertext, Binding{s.tenant, s.versions.ProviderID, s.versions.CredentialID, s.versions.CipherVersion})
	if err != nil {
		return failure("ai.budget_unavailable")
	}
	defer clear(secret)
	p := s.provider
	return send(RelayTarget{BaseURL: p.BaseUrl, UpstreamModel: s.model.UpstreamModel, ConnectTimeoutSeconds: p.ConnectTimeoutSeconds, FirstByteTimeoutSeconds: p.FirstByteTimeoutSeconds, IdleTimeoutSeconds: p.IdleTimeoutSeconds, TotalTimeoutSeconds: p.TotalTimeoutSeconds, MaxConcurrency: p.MaxConcurrency}, secret)
}
