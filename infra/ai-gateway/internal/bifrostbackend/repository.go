package bifrostbackend

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/audit"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/identifiers"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const maxInteger int64 = 9007199254740991

var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type fault string

func (e fault) Error() string { return string(e) }

const invalid = fault("common.validation_failed")
const denied = fault("common.forbidden")
const missing = fault("common.not_found")

type Backend struct {
	tenant         string
	db             *store.Store
	ledger         *budget.Ledger
	aead           cipher.AEAD
	signing        []byte
	password       [32]byte
	memberPassword [32]byte
	allowPrivate   bool
}

func New(db *store.Store, masterHex, password, memberPassword string, allowPrivate bool) (*Backend, error) {
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) != 32 || len(password) < 32 || len(memberPassword) < 32 || password == memberPassword || db == nil {
		return nil, invalid
	}
	defer clear(master)
	encryption, err := hkdf.Key(sha256.New, master, nil, "auriga/relay/encryption/v1", 32)
	if err != nil {
		return nil, err
	}
	defer clear(encryption)
	signing, err := hkdf.Key(sha256.New, master, nil, "auriga/relay/signing/v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encryption)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	flight := int64(128 << 20)
	ledger, err := budget.New(db, "USD", &api.AdmissionPolicy{MaxActiveRequestsPerSubject: 32, MaxInFlightPercent: 100, MaxInFlightTokens: &flight})
	if err != nil {
		return nil, err
	}
	return &Backend{db: db, ledger: ledger, aead: aead, signing: signing, password: sha256.Sum256([]byte(password)), memberPassword: sha256.Sum256([]byte(memberPassword)), allowPrivate: allowPrivate}, nil
}

func validUUID(s string) bool {
	return identifiers.ValidUUID(s)
}
func newID() (string, error) { u, e := uuid.NewV7(); return u.String(), e }
func (b *Backend) token(tenant, id string) string {
	value := tenant + "." + id
	mac := hmac.New(sha256.New, b.signing)
	_, _ = mac.Write([]byte(value))
	return "sk-bf-" + value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (b *Backend) parseToken(raw string) (string, string, error) {
	parts := strings.Split(strings.TrimPrefix(raw, "sk-bf-"), ".")
	if !strings.HasPrefix(raw, "sk-bf-") || len(parts) != 3 || !validUUID(parts[0]) || !validUUID(parts[1]) || !hmac.Equal([]byte(raw), []byte(b.token(parts[0], parts[1]))) {
		return "", "", fault("common.unauthenticated")
	}
	if b.tenant != "" && parts[0] != b.tenant {
		return "", "", denied
	}
	return parts[0], parts[1], nil
}
func memberName(tenant, account string) string {
	raw, _ := json.Marshal([]string{tenant, account})
	sum := sha256.Sum256(raw)
	return "auriga-member:" + hex.EncodeToString(sum[:])
}
func (b *Backend) encrypt(tenant, id, secret string) ([]byte, error) {
	nonce := make([]byte, 1+b.aead.NonceSize())
	nonce[0] = 1
	if _, e := rand.Read(nonce[1:]); e != nil {
		return nil, e
	}
	return b.aead.Seal(nonce, nonce[1:], []byte(secret), []byte("v1:"+tenant+":"+id)), nil
}
func (b *Backend) decrypt(tenant, id string, sealed []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != 1 {
		return "", denied
	}
	raw, e := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte("v1:"+tenant+":"+id))
	if e != nil {
		return "", denied
	}
	defer clear(raw)
	return string(raw), nil
}

func keyInputValid(k api.RelayKeyInput) bool {
	if _, _, e := limits(k.MaxConcurrency, k.MaxInFlightTokens, false); e != nil {
		return false
	}
	return strings.TrimSpace(k.Name) == k.Name && len(k.Name) > 0 && len(k.Name) <= 128 && k.MonthlyBudgetMicros >= 0 && k.MonthlyBudgetMicros <= maxInteger && k.RequestsPerMinute >= 0 && k.RequestsPerMinute <= 1000000 && modelsValid(k.Models)
}
func modelsValid(models []string) bool {
	if len(models) < 1 || len(models) > 128 {
		return false
	}
	for _, m := range models {
		if m != "*" && !modelID.MatchString(m) {
			return false
		}
	}
	return !slices.Contains(models, "*") || len(models) == 1
}
func (b *Backend) readKey(ctx context.Context, tenant, id string, rpm bool) (api.RelayKey, error) {
	var k api.RelayKey
	err := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var raw []byte
		var minute time.Time
		var count int64
		query := `SELECT document,minute,requests FROM ai.relay_key WHERE tenant_id=$1 AND id=$2`
		if rpm {
			query += " FOR UPDATE"
		}
		if e := tx.QueryRow(ctx, query, tenant, id).Scan(&raw, &minute, &count); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return missing
			}
			return e
		}
		if json.Unmarshal(raw, &k) != nil || k.Id != id {
			return denied
		}
		normalizeLimits(&k)
		if k.GetMeteringOnly() && k.Source != "member" {
			return denied
		}
		if !k.Active {
			return denied
		}
		if !rpm || k.GetMeteringOnly() || k.RequestsPerMinute == 0 {
			return nil
		}
		var now time.Time
		if e := tx.QueryRow(ctx, `SELECT date_trunc('minute',clock_timestamp())`).Scan(&now); e != nil {
			return e
		}
		if !now.Equal(minute) {
			count = 0
		}
		if count >= k.RequestsPerMinute {
			return fault("common.rate_limited")
		}
		_, e := tx.Exec(ctx, `UPDATE ai.relay_key SET minute=$3,requests=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, now, count+1)
		return e
	})
	return k, err
}
func (b *Backend) EnsureMember(ctx context.Context, m api.RelayMemberEnsure) (api.RelayKeyIssued, error) {
	var out api.RelayKeyIssued
	if b.tenant != "" && m.TenantId != b.tenant {
		return out, denied
	}
	if !validUUID(m.TenantId) || !validUUID(m.AccountId) || m.MonthlyBudgetMicros < 0 || m.MonthlyBudgetMicros > maxInteger || m.RequestsPerMinute < 0 || m.RequestsPerMinute > 1000000 {
		return out, invalid
	}
	c, n, err := limits(m.MaxConcurrency, m.MaxInFlightTokens, true)
	if err != nil {
		return out, err
	}
	k := api.RelayKey{MaxConcurrency: &c, MaxInFlightTokens: &n, Id: m.AccountId, Name: memberName(m.TenantId, m.AccountId), Source: "member", AccountId: m.AccountId, Active: true, MonthlyBudgetMicros: m.MonthlyBudgetMicros, RequestsPerMinute: m.RequestsPerMinute, Models: []string{"*"}}
	origin := "default"
	k.PolicyOrigin = &origin
	k.MeteringOnly = m.MeteringOnly
	raw, e := json.Marshal(k)
	if e != nil {
		return out, e
	}
	if e = b.db.Transaction(ctx, m.TenantId, func(tx pgx.Tx) error {
		var created bool
		e := tx.QueryRow(ctx, `INSERT INTO ai.relay_key(tenant_id,id,name,document) VALUES($1,$2,$3,$4) ON CONFLICT (tenant_id,id) DO UPDATE SET document=ai.relay_key.document || jsonb_build_object('meteringOnly',EXCLUDED.document->'meteringOnly','monthlyBudgetMicros',EXCLUDED.document->'monthlyBudgetMicros','requestsPerMinute',EXCLUDED.document->'requestsPerMinute','maxConcurrency',EXCLUDED.document->'maxConcurrency','maxInFlightTokens',EXCLUDED.document->'maxInFlightTokens') WHERE ai.relay_key.document->>'policyOrigin'='default' AND ROW(ai.relay_key.document->'meteringOnly',ai.relay_key.document->'monthlyBudgetMicros',ai.relay_key.document->'requestsPerMinute',ai.relay_key.document->'maxConcurrency',ai.relay_key.document->'maxInFlightTokens') IS DISTINCT FROM ROW(EXCLUDED.document->'meteringOnly',EXCLUDED.document->'monthlyBudgetMicros',EXCLUDED.document->'requestsPerMinute',EXCLUDED.document->'maxConcurrency',EXCLUDED.document->'maxInFlightTokens') RETURNING (xmax=0)`, m.TenantId, m.AccountId, k.Name, raw).Scan(&created)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		action := "ai.relay_key.budget.update"
		if created {
			action = "ai.relay_key.create"
		}
		return audit.Record(ctx, tx, m.TenantId, action, "relay_key", m.AccountId)

	}); e != nil {
		return out, e
	}
	k, e = b.readKey(ctx, m.TenantId, m.AccountId, false)
	if e != nil {
		return out, e
	}
	if k.Source != "member" || k.AccountId != m.AccountId || k.Name != memberName(m.TenantId, m.AccountId) {
		return out, denied
	}
	return api.RelayKeyIssued{Key: k, Secret: b.token(m.TenantId, k.Id)}, nil
}
func (b *Backend) SaveKey(ctx context.Context, tenant, id string, input api.RelayKeyInput) (api.RelayKeyIssued, error) {
	var out api.RelayKeyIssued
	if !keyInputValid(input) {
		return out, invalid
	}
	create := id == ""
	if create && strings.HasPrefix(input.Name, "auriga-member:") {
		return out, invalid
	}
	var e error
	if create {
		id, e = newID()
		if e != nil {
			return out, e
		}
	}
	e = b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		k := api.RelayKey{Id: id, Source: "external"}
		if !create {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT document FROM ai.relay_key WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&raw); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return missing
				}
				return err
			}
			if json.Unmarshal(raw, &k) != nil {
				return denied
			}
		}
		previous := k
		if k.Source != "member" && strings.HasPrefix(input.Name, "auriga-member:") {
			return invalid
		}
		if k.Source != "member" && input.GetMeteringOnly() {
			return invalid
		}
		if k.Source == "member" { // Member ownership/name is immutable; only policy/active state changes.
			input.Name = k.Name
		}
		k.Name = input.Name
		k.Active = input.Active
		k.MonthlyBudgetMicros = input.MonthlyBudgetMicros
		k.RequestsPerMinute = input.RequestsPerMinute
		k.Models = slices.Clone(input.Models)
		k.MeteringOnly = input.MeteringOnly
		origin := "administrator"
		k.PolicyOrigin = &origin
		c, n, e := limits(input.MaxConcurrency, input.MaxInFlightTokens, k.Source == "member")
		if e != nil {
			return e
		}
		k.MaxConcurrency = &c
		k.MaxInFlightTokens = &n
		raw, err := json.Marshal(k)
		if err != nil {
			return err
		}
		out.Key = k
		if !create && reflect.DeepEqual(k, previous) {
			return nil
		}
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO ai.relay_key(tenant_id,id,name,document) VALUES($1,$2,$3,$4)`, tenant, id, k.Name, raw)
		} else {
			_, err = tx.Exec(ctx, `UPDATE ai.relay_key SET name=$3,document=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, k.Name, raw)
		}
		if err != nil {
			return err
		}
		action := "ai.relay_key.update"
		if create {
			action = "ai.relay_key.create"
		} else if previous.Active && !k.Active {
			action = "ai.relay_key.revoke"
		}
		if err = audit.Record(ctx, tx, tenant, action, "relay_key", id); err != nil {
			return err
		}
		if !create && (previous.MonthlyBudgetMicros != k.MonthlyBudgetMicros || previous.GetMeteringOnly() != k.GetMeteringOnly()) {
			return audit.Record(ctx, tx, tenant, "ai.relay_key.budget.update", "relay_key", id)
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	if create {
		out.Secret = b.token(tenant, id)
	}
	return out, nil
}
func (b *Backend) ListKeys(ctx context.Context, tenant string, pages ...listPage) (api.RelayKeyPage, error) {
	page := pageFor(pages)
	out := api.RelayKeyPage{Items: []api.RelayKey{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT document FROM ai.relay_key WHERE tenant_id=$1 AND id>$2::uuid ORDER BY id LIMIT $3`, tenant, afterUUID(page.After), page.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Items) == page.Limit {
				cursor := listCursor(tenant, "virtual-keys", out.Items[len(out.Items)-1].Id)
				out.NextCursor = &cursor
				break
			}
			var raw []byte
			var k api.RelayKey
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if json.Unmarshal(raw, &k) != nil {
				return denied
			}
			normalizeLimits(&k)
			out.Items = append(out.Items, k)
		}
		return rows.Err()
	})
	return out, e
}

func providerValid(p api.RelayProviderInput, private bool) bool {
	if !slices.Contains([]string{"openai", "anthropic", "gemini", "minimax", "deepseek"}, providerKind(p.Kind)) || len(p.Name) < 1 || len(p.Name) > 128 {
		return false
	}
	u, e := url.Parse(p.BaseUrl)
	return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && (u.Scheme == "https" || private && u.Scheme == "http") && strings.TrimRight(u.Path, "/") == ""
}
func (b *Backend) SaveProvider(ctx context.Context, tenant, id string, p api.RelayProviderInput, secret *string) (api.RelayProvider, error) {
	var out api.RelayProvider
	if secret == nil && !providerValid(p, b.allowPrivate) {
		return out, invalid
	}
	var e error
	create := id == ""
	if create {
		id, e = newID()
		if e != nil {
			return out, e
		}
	} else if !validUUID(id) {
		return out, invalid
	}
	e = b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var sealed []byte
		revokedCredential := false
		if !create {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT document,secret FROM ai.relay_provider WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&raw, &sealed); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return missing
				}
				return err
			}
			var previous api.RelayProviderInput
			if json.Unmarshal(raw, &previous) != nil {
				return denied
			}
			if secret != nil {
				p = previous
			} else if p.BaseUrl != previous.BaseUrl || p.Kind != previous.Kind {
				revokedCredential = len(sealed) > 0
				sealed = nil
			}
		}
		if secret != nil {
			if len(*secret) < 1 || len(*secret) > 16384 || strings.ContainsAny(*secret, "\r\n") {
				return invalid
			}
			var err error
			sealed, err = b.encrypt(tenant, id, *secret)
			if err != nil {
				return err
			}
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO ai.relay_provider(tenant_id,id,document,secret) VALUES($1,$2,$3,$4)`, tenant, id, raw, sealed)
		} else {
			_, err = tx.Exec(ctx, `UPDATE ai.relay_provider SET document=$3,secret=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, raw, sealed)
		}
		out = api.RelayProvider{Id: id, Configuration: p, HasCredential: len(sealed) > 0}
		if err != nil {
			return err
		}
		if secret != nil {
			return audit.Record(ctx, tx, tenant, "ai.credential.rotate", "provider", id)
		}
		if revokedCredential {
			return audit.Record(ctx, tx, tenant, "ai.credential.revoke", "provider", id)
		}
		return nil
	})
	return out, e
}
func (b *Backend) ListProviders(ctx context.Context, tenant string, pages ...listPage) (api.RelayProviderPage, error) {
	page := pageFor(pages)
	out := api.RelayProviderPage{Items: []api.RelayProvider{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id::text,document,secret IS NOT NULL FROM ai.relay_provider WHERE tenant_id=$1 AND id>$2::uuid ORDER BY id LIMIT $3`, tenant, afterUUID(page.After), page.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Items) == page.Limit {
				cursor := listCursor(tenant, "providers", out.Items[len(out.Items)-1].Id)
				out.NextCursor = &cursor
				break
			}
			var raw []byte
			var p api.RelayProvider
			if e = rows.Scan(&p.Id, &raw, &p.HasCredential); e != nil {
				return e
			}
			if json.Unmarshal(raw, &p.Configuration) != nil {
				return denied
			}
			out.Items = append(out.Items, p)
		}
		return rows.Err()
	})
	return out, e
}
func (b *Backend) SavePool(ctx context.Context, tenant string, p api.RelayPool) error {
	return b.savePool(ctx, tenant, p, false)
}

// CreatePool atomically refuses an existing tenant-scoped public model ID.
func (b *Backend) CreatePool(ctx context.Context, tenant string, p api.RelayPool) error {
	return b.savePool(ctx, tenant, p, true)
}

func (b *Backend) savePool(ctx context.Context, tenant string, p api.RelayPool, createOnly bool) error {
	var err error
	p, err = resolvePoolCapabilities(p)
	if err != nil {
		return err
	}
	if !modelID.MatchString(p.Id) || len(p.Routes) < 1 || len(p.Routes) > 8 || !validPoolCapabilities(p.ContextWindow, p.MaxOutputTokens) || p.Price.Currency != "USD" || p.Price.Version == "" || p.Price.EffectiveAt.IsZero() || p.Price.EffectiveAt.After(time.Now()) || p.Price.InputMicrosPerMillionTokens <= 0 || p.Price.OutputMicrosPerMillionTokens <= 0 || p.Price.InputMicrosPerMillionTokens > maxInteger || p.Price.OutputMicrosPerMillionTokens > maxInteger {
		return invalid
	}
	providers, err := routeProviders(p.Routes)
	if err != nil {
		return err
	}
	return b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		for id := range providers {
			var exists bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai.relay_provider WHERE tenant_id=$1 AND id=$2)`, tenant, id).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return missing
			}
		}
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		if createOnly {
			tag, err := tx.Exec(ctx, `INSERT INTO ai.relay_pool(tenant_id,id,document) VALUES($1,$2,$3) ON CONFLICT(tenant_id,id) DO NOTHING`, tenant, p.Id, raw)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fault("common.precondition_failed")
			}
			return nil
		}
		_, e = tx.Exec(ctx, `INSERT INTO ai.relay_pool(tenant_id,id,document) VALUES($1,$2,$3) ON CONFLICT(tenant_id,id) DO UPDATE SET document=EXCLUDED.document`, tenant, p.Id, raw)
		return e
	})
}

func routeProviders(routes []api.RelayRoute) (map[string]bool, error) {
	if len(routes) < 1 || len(routes) > 8 {
		return nil, invalid
	}
	weight := 0.0
	seen := map[[2]string]bool{}
	providers := map[string]bool{}
	for _, r := range routes {
		identity := [2]string{r.ProviderId, r.Model}
		if !validUUID(r.ProviderId) || seen[identity] || len(r.Model) < 1 || len(r.Model) > 256 || strings.TrimSpace(r.Model) != r.Model || strings.ContainsAny(r.Model, "\r\n") || r.Weight < 0 || r.Weight > 1000 || math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) {
			return nil, invalid
		}
		seen[identity] = true
		providers[r.ProviderId] = true
		weight += r.Weight
	}
	if weight <= 0 {
		return nil, invalid
	}
	return providers, nil
}
func (b *Backend) ListPools(ctx context.Context, tenant string, pages ...listPage) (api.RelayPoolPage, error) {
	page := pageFor(pages)
	out := api.RelayPoolPage{Items: []api.RelayPool{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT document FROM ai.relay_pool WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, tenant, page.After, page.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Items) == page.Limit {
				cursor := listCursor(tenant, "pools", out.Items[len(out.Items)-1].Id)
				out.NextCursor = &cursor
				break
			}
			var raw []byte
			var p api.RelayPool
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if json.Unmarshal(raw, &p) != nil {
				return denied
			}
			p, e = resolvePoolCapabilities(p)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, p)
		}
		return rows.Err()
	})
	return out, e
}
func (b *Backend) poolRoutes(ctx context.Context, tenant, id string) (api.RelayPool, []Route, error) {
	var p api.RelayPool
	var routes []Route
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var raw []byte
		if e := tx.QueryRow(ctx, `SELECT document FROM ai.relay_pool WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&raw); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return missing
			}
			return e
		}
		if json.Unmarshal(raw, &p) != nil || !p.Enabled {
			return denied
		}
		for _, r := range p.Routes {
			var cfg api.RelayProviderInput
			var secret []byte
			if e := tx.QueryRow(ctx, `SELECT document,secret FROM ai.relay_provider WHERE tenant_id=$1 AND id=$2`, tenant, r.ProviderId).Scan(&raw, &secret); e != nil {
				return e
			}
			if json.Unmarshal(raw, &cfg) != nil {
				return denied
			}
			if !cfg.Enabled || len(secret) == 0 {
				continue
			}
			key, e := b.decrypt(tenant, r.ProviderId, secret)
			if e != nil {
				return e
			}
			routes = append(routes, Route{ID: r.ProviderId, Kind: cfg.Kind, URL: cfg.BaseUrl, Secret: key, Model: r.Model, Weight: r.Weight})
		}
		return nil
	})
	if e == nil && len(routes) == 0 {
		e = fault("common.unavailable")
	}
	return p, routes, e
}

func (b *Backend) pseudonym(tenant, id string) string {
	mac := hmac.New(sha256.New, b.signing)
	_, _ = mac.Write([]byte("provider-user/v1:" + tenant + ":" + id))
	return "auriga-" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
