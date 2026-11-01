package bifrostbackend

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
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
	db           *store.Store
	ledger       *budget.Ledger
	aead         cipher.AEAD
	signing      []byte
	password     [32]byte
	allowPrivate bool
}

func New(db *store.Store, masterHex, password string, allowPrivate bool) (*Backend, error) {
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) != 32 || len(password) < 32 || db == nil {
		return nil, invalid
	}
	defer clear(master)
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("auriga/relay/virtual-key/v1"))
	flight := int64(8 << 20)
	ledger, err := budget.New(db, "USD", &api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 100, MaxInFlightTokens: &flight})
	if err != nil {
		return nil, err
	}
	return &Backend{db: db, ledger: ledger, aead: aead, signing: mac.Sum(nil), password: sha256.Sum256([]byte(password)), allowPrivate: allowPrivate}, nil
}

func validUUID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u.String() == s && u != uuid.Nil
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
	return parts[0], parts[1], nil
}
func memberName(tenant, account string) string {
	raw, _ := json.Marshal([]string{tenant, account})
	sum := sha256.Sum256(raw)
	return "auriga-member:" + hex.EncodeToString(sum[:])
}
func (b *Backend) encrypt(tenant, id, secret string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return b.aead.Seal(nonce, nonce, []byte(secret), []byte(tenant+":"+id)), nil
}
func (b *Backend) decrypt(tenant, id string, sealed []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return "", denied
	}
	raw, e := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(tenant+":"+id))
	if e != nil {
		return "", denied
	}
	defer clear(raw)
	return string(raw), nil
}

func keyInputValid(k api.RelayKeyInput) bool {
	return strings.TrimSpace(k.Name) == k.Name && len(k.Name) > 0 && len(k.Name) <= 128 && k.MonthlyBudgetMicros >= 0 && k.MonthlyBudgetMicros <= maxInteger && k.RequestsPerMinute > 0 && k.RequestsPerMinute <= 1000000 && modelsValid(k.Models)
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
		if !k.Active {
			return denied
		}
		if !rpm {
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
	if !validUUID(m.TenantId) || !validUUID(m.AccountId) || m.MonthlyBudgetMicros < 0 || m.MonthlyBudgetMicros > maxInteger || m.RequestsPerMinute < 1 || m.RequestsPerMinute > 1000000 {
		return out, invalid
	}
	k := api.RelayKey{Id: m.AccountId, Name: memberName(m.TenantId, m.AccountId), Source: "member", AccountId: m.AccountId, Active: true, MonthlyBudgetMicros: m.MonthlyBudgetMicros, RequestsPerMinute: m.RequestsPerMinute, Models: []string{"*"}}
	raw, e := json.Marshal(k)
	if e != nil {
		return out, e
	}
	if e = b.db.Transaction(ctx, m.TenantId, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ai.relay_key(tenant_id,id,name,document) VALUES($1,$2,$3,$4) ON CONFLICT (tenant_id,id) DO NOTHING`, m.TenantId, m.AccountId, k.Name, raw)
		return e
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
		if k.Source == "member" { // Member ownership/name is immutable; only policy/active state changes.
			input.Name = k.Name
		}
		k.Name = input.Name
		k.Active = input.Active
		k.MonthlyBudgetMicros = input.MonthlyBudgetMicros
		k.RequestsPerMinute = input.RequestsPerMinute
		k.Models = slices.Clone(input.Models)
		raw, err := json.Marshal(k)
		if err != nil {
			return err
		}
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO ai.relay_key(tenant_id,id,name,document) VALUES($1,$2,$3,$4)`, tenant, id, k.Name, raw)
		} else {
			_, err = tx.Exec(ctx, `UPDATE ai.relay_key SET name=$3,document=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, k.Name, raw)
		}
		out.Key = k
		return err
	})
	if e != nil {
		return out, e
	}
	if create {
		out.Secret = b.token(tenant, id)
	}
	return out, nil
}
func (b *Backend) ListKeys(ctx context.Context, tenant string) (api.RelayKeyPage, error) {
	out := api.RelayKeyPage{Items: []api.RelayKey{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT document FROM ai.relay_key WHERE tenant_id=$1 ORDER BY name LIMIT 200`, tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var k api.RelayKey
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if json.Unmarshal(raw, &k) != nil {
				return denied
			}
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
		if !create {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT document,secret FROM ai.relay_provider WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&raw, &sealed); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return missing
				}
				return err
			}
			if secret != nil && json.Unmarshal(raw, &p) != nil {
				return denied
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
		return err
	})
	return out, e
}
func (b *Backend) ListProviders(ctx context.Context, tenant string) (api.RelayProviderPage, error) {
	out := api.RelayProviderPage{Items: []api.RelayProvider{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id::text,document,secret IS NOT NULL FROM ai.relay_provider WHERE tenant_id=$1 ORDER BY id LIMIT 200`, tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
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
	if !modelID.MatchString(p.Id) || len(p.Routes) < 1 || len(p.Routes) > 8 || p.ContextWindow < 2 || p.ContextWindow > 1048576 || p.MaxOutputTokens < 1 || p.MaxOutputTokens > 32768 || p.MaxOutputTokens >= p.ContextWindow || p.Price.Currency != "USD" || p.Price.Version == "" || p.Price.EffectiveAt.IsZero() || p.Price.EffectiveAt.After(time.Now()) || p.Price.InputMicrosPerMillionTokens <= 0 || p.Price.OutputMicrosPerMillionTokens <= 0 || p.Price.InputMicrosPerMillionTokens > maxInteger || p.Price.OutputMicrosPerMillionTokens > maxInteger {
		return invalid
	}
	weight := 0.0
	seen := map[string]bool{}
	for _, r := range p.Routes {
		if !validUUID(r.ProviderId) || seen[r.ProviderId] || len(r.Model) < 1 || len(r.Model) > 256 || strings.ContainsAny(r.Model, "\r\n") || r.Weight < 0 || r.Weight > 1000 || math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) {
			return invalid
		}
		seen[r.ProviderId] = true
		weight += r.Weight
	}
	if weight <= 0 {
		return invalid
	}
	return b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		for id := range seen {
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
		_, e = tx.Exec(ctx, `INSERT INTO ai.relay_pool(tenant_id,id,document) VALUES($1,$2,$3) ON CONFLICT(tenant_id,id) DO UPDATE SET document=EXCLUDED.document`, tenant, p.Id, raw)
		return e
	})
}
func (b *Backend) ListPools(ctx context.Context, tenant string) (api.RelayPoolPage, error) {
	out := api.RelayPoolPage{Items: []api.RelayPool{}}
	e := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT document FROM ai.relay_pool WHERE tenant_id=$1 ORDER BY id LIMIT 200`, tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var p api.RelayPool
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if json.Unmarshal(raw, &p) != nil {
				return denied
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
			routes = append(routes, Route{ID: r.ProviderId, Kind: providerKind(cfg.Kind), URL: cfg.BaseUrl, Secret: key, Model: r.Model, Weight: r.Weight})
		}
		return nil
	})
	if e == nil && len(routes) == 0 {
		e = fault("common.unavailable")
	}
	return p, routes, e
}
