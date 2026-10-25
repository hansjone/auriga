package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

const maxInteger int64 = 9007199254740991

func baseQuery(r *http.Request) (url.Values, error) {
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, failure("common.validation_failed")
	}
	return q, nil
}
func decode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return failure("common.validation_failed")
	}
	return nil
}
func validLength(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}

func validateProvider(p api.Provider) error {
	if !validLength(p.Name, 1, 128) || p.Kind != "openai_compatible" || p.ConnectTimeoutSeconds < 1 || p.ConnectTimeoutSeconds > 120 || p.FirstByteTimeoutSeconds < 1 || p.FirstByteTimeoutSeconds > 300 || p.IdleTimeoutSeconds < 1 || p.IdleTimeoutSeconds > 300 || p.TotalTimeoutSeconds < 1 || p.TotalTimeoutSeconds > 3600 || p.MaxConcurrency < 1 || p.MaxConcurrency > 10000 {
		return failure("common.validation_failed")
	}
	u, err := url.Parse(p.BaseUrl)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(p.BaseUrl, "\r\n\\") {
		return failure("common.validation_failed")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		return failure("common.validation_failed")
	}
	if addr, e := netip.ParseAddr(host); e == nil && !publicAddress(addr) {
		return failure("common.validation_failed")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return failure("common.validation_failed")
		}
	}
	return nil
}

// Dispatch must resolve and recheck every address, pin the selected address and
// reject redirects. Registration alone cannot prevent later DNS rebinding.
func publicAddress(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "2001:db8::/32", "fc00::/7"} {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func validateModel(m api.Model) error {
	p := m.Price
	if !modelPattern.MatchString(m.Id) || !uuidPattern.MatchString(m.ProviderId) || !validLength(m.UpstreamModel, 1, 128) || m.ContextWindow < 1 || m.ContextWindow > maxInteger || m.MaxOutputTokens < 1 || m.MaxOutputTokens > maxInteger || m.MaxOutputTokens > m.ContextWindow || !currencyPattern.MatchString(p.Currency) || p.InputMicrosPerMillionTokens < 0 || p.InputMicrosPerMillionTokens > maxInteger || p.OutputMicrosPerMillionTokens < 0 || p.OutputMicrosPerMillionTokens > maxInteger || !validLength(p.Version, 1, 128) || p.EffectiveAt.IsZero() {
		return failure("common.validation_failed")
	}
	return nil
}
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	b[6] = (b[6] & 15) | 0x70
	b[8] = (b[8] & 63) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func loadProvider(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (api.Provider, error) {
	var raw []byte
	var revision int64
	sql := "SELECT document,revision FROM ai.provider WHERE tenant_id=$1 AND id=$2"
	if lock {
		sql += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, sql, tenant, id).Scan(&raw, &revision)
	var p api.Provider
	if err == nil {
		err = readDocument(raw, revision, &p)
	}
	return p, err
}
func saveProvider(ctx context.Context, tx pgx.Tx, tenant string, p api.Provider) error {
	b, e := document(p, p.Revision)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "UPDATE ai.provider SET document=$3,revision=$4 WHERE tenant_id=$1 AND id=$2", tenant, p.Id, b, p.Revision)
	return e
}

// Stored documents from 0001 predate the required response etag field. Derive it
// from the row revision without rewriting existing migration SQL or tenant rows.
func readDocument(raw []byte, revision int64, out any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("invalid stored AI document")
	}
	fields["etag"], _ = json.Marshal(etag(revision))
	fields["revision"], _ = json.Marshal(revision)
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func loadModel(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (api.Model, error) {
	var b []byte
	var revision int64
	sql := "SELECT document,revision FROM ai.model WHERE tenant_id=$1 AND id=$2"
	if lock {
		sql += " FOR UPDATE"
	}
	e := tx.QueryRow(ctx, sql, tenant, id).Scan(&b, &revision)
	var m api.Model
	if e == nil {
		e = readDocument(b, revision, &m)
	}
	return m, e
}
func merge(raw []byte, current any, out any) error {
	base, err := json.Marshal(current)
	if err != nil {
		return err
	}
	var fields, patch map[string]json.RawMessage
	if json.Unmarshal(base, &fields) != nil || json.Unmarshal(raw, &patch) != nil || len(patch) == 0 {
		return failure("common.validation_failed")
	}
	for k, v := range patch {
		fields[k] = v
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return decode(b, out)
}
func precondition(r *http.Request, revision int64) error {
	if r.Header.Get("If-Match") != etag(revision) {
		return failure("common.precondition_failed")
	}
	return nil
}

// Grants share the model row revision but have a separate resource validator.
// UUID/model identifiers contain no NUL, making this domain tuple unambiguous.
func grantsETag(tenant, model string, revision int64) string {
	sum := sha256.Sum256([]byte("auriga/ai/model-grants/v1\x00" + tenant + "\x00" + model + "\x00" + strconv.FormatInt(revision, 10)))
	return `"grants-` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
}

func grantsResponse(g api.ModelGrants, tenant, model string, revision int64) (result, error) {
	res, err := response(200, g, 0)
	res.etag = grantsETag(tenant, model, revision)
	return res, err
}

func (s *Server) execute(ctx context.Context, tx pgx.Tx, p *Principal, o operation, raw []byte, r *http.Request) (result, error) {
	switch o.name {
	case "listProviders", "listModels":
		return list(ctx, tx, p.Tenant, o, r)
	case "getProvider":
		v, e := loadProvider(ctx, tx, p.Tenant, o.provider, false)
		if e != nil {
			return result{}, e
		}
		return response(200, v, v.Revision)
	case "getModel":
		v, e := loadModel(ctx, tx, p.Tenant, o.model, false)
		if e != nil {
			return result{}, e
		}
		return response(200, v, v.Revision)
	case "getCredential":
		v := api.Credential{Id: o.credential}
		e := tx.QueryRow(ctx, "SELECT state,created_at,revision FROM ai.credential WHERE tenant_id=$1 AND provider_id=$2 AND id=$3", p.Tenant, o.provider, o.credential).Scan(&v.State, &v.CreatedAt, &v.Revision)
		if e != nil {
			return result{}, e
		}
		return response(200, v, v.Revision)
	case "getGrants":
		var raw []byte
		var revision int64
		e := tx.QueryRow(ctx, "SELECT grants,revision FROM ai.model WHERE tenant_id=$1 AND id=$2", p.Tenant, o.model).Scan(&raw, &revision)
		if e != nil {
			return result{}, e
		}
		var g api.ModelGrants
		if e = json.Unmarshal(raw, &g); e != nil {
			return result{}, e
		}
		return grantsResponse(g, p.Tenant, o.model, revision)
	case "createProvider":
		var in api.ProviderInput
		if e := decode(raw, &in); e != nil {
			return result{}, e
		}
		id, e := newID()
		if e != nil {
			return result{}, e
		}
		v := api.Provider{Id: id, Name: in.Name, Kind: in.Kind, BaseUrl: in.BaseUrl, Enabled: in.Enabled, ConnectTimeoutSeconds: in.ConnectTimeoutSeconds, FirstByteTimeoutSeconds: in.FirstByteTimeoutSeconds, IdleTimeoutSeconds: in.IdleTimeoutSeconds, TotalTimeoutSeconds: in.TotalTimeoutSeconds, MaxConcurrency: in.MaxConcurrency, ActiveCredentialId: *api.NewNullableString(nil), Revision: 1}
		if e = validateProvider(v); e != nil {
			return result{}, e
		}
		if e = s.validateEndpoint(ctx, v.BaseUrl); e != nil {
			return result{}, e
		}
		b, e := document(v, v.Revision)
		if e != nil {
			return result{}, e
		}
		_, e = tx.Exec(ctx, "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES ($1,$2,$3,1)", p.Tenant, id, b)
		if e != nil {
			return result{}, e
		}
		return response(201, v, 1)
	case "patchProvider":
		var patch api.ProviderPatch
		if e := decode(raw, &patch); e != nil {
			return result{}, e
		}
		v, e := loadProvider(ctx, tx, p.Tenant, o.provider, true)
		if e != nil {
			return result{}, e
		}
		if e = precondition(r, v.Revision); e != nil {
			return result{}, e
		}
		if e = merge(raw, v, &v); e != nil {
			return result{}, e
		}
		if e = validateProvider(v); e != nil {
			return result{}, e
		}
		if patch.BaseUrl != nil {
			if e = s.validateEndpoint(ctx, v.BaseUrl); e != nil {
				return result{}, e
			}
		}
		v.Revision++
		if e = saveProvider(ctx, tx, p.Tenant, v); e != nil {
			return result{}, e
		}
		return response(200, v, v.Revision)
	case "createCredential", "activate", "revoke":
		return s.credential(ctx, tx, p, o, raw, r)
	case "createModel":
		var in api.ModelInput
		if e := decode(raw, &in); e != nil {
			return result{}, e
		}
		m := api.Model{Id: in.Id, ProviderId: in.ProviderId, UpstreamModel: in.UpstreamModel, DisplayName: in.DisplayName, Enabled: in.Enabled, ContextWindow: in.ContextWindow, MaxOutputTokens: in.MaxOutputTokens, Price: in.Price, Revision: 1}
		if e := validateModel(m); e != nil {
			return result{}, e
		}
		if _, e := loadProvider(ctx, tx, p.Tenant, m.ProviderId, false); e != nil {
			return result{}, e
		}
		b, e := document(m, m.Revision)
		if e != nil {
			return result{}, e
		}
		_, e = tx.Exec(ctx, "INSERT INTO ai.model(tenant_id,id,provider_id,document,revision) VALUES ($1,$2,$3,$4,1)", p.Tenant, m.Id, m.ProviderId, b)
		if e != nil {
			return result{}, e
		}
		return response(201, m, 1)
	case "patchModel", "grants":
		m, e := loadModel(ctx, tx, p.Tenant, o.model, true)
		if e != nil {
			return result{}, e
		}
		if o.name == "grants" {
			if r.Header.Get("If-Match") != grantsETag(p.Tenant, m.Id, m.Revision) {
				return result{}, failure("common.precondition_failed")
			}
			var g api.ModelGrants
			if e = decode(raw, &g); e != nil {
				return result{}, e
			}
			if e = validateGrants(g); e != nil {
				return result{}, e
			}
			if e = s.checkGrants(ctx, p, g); e != nil {
				return result{}, e
			}
			m.Revision++
			b, marshalErr := document(m, m.Revision)
			if marshalErr != nil {
				return result{}, marshalErr
			}
			_, e = tx.Exec(ctx, "UPDATE ai.model SET grants=$3,document=$4,revision=$5 WHERE tenant_id=$1 AND id=$2", p.Tenant, m.Id, raw, b, m.Revision)
			if e != nil {
				return result{}, e
			}
			return grantsResponse(g, p.Tenant, m.Id, m.Revision)
		}
		if e = precondition(r, m.Revision); e != nil {
			return result{}, e
		}
		var patch api.ModelPatch
		if e = decode(raw, &patch); e != nil {
			return result{}, e
		}
		if e = merge(raw, m, &m); e != nil {
			return result{}, e
		}
		if e = validateModel(m); e != nil {
			return result{}, e
		}
		if _, e = loadProvider(ctx, tx, p.Tenant, m.ProviderId, false); e != nil {
			return result{}, e
		}
		m.Revision++
		b, e := document(m, m.Revision)
		if e != nil {
			return result{}, e
		}
		_, e = tx.Exec(ctx, "UPDATE ai.model SET provider_id=$3,document=$4,revision=$5 WHERE tenant_id=$1 AND id=$2", p.Tenant, m.Id, m.ProviderId, b, m.Revision)
		if e != nil {
			return result{}, e
		}
		return response(200, m, m.Revision)
	}
	return result{}, failure("common.not_found")
}

func validateGrants(g api.ModelGrants) error {
	if len(g.Items) > 1000 {
		return failure("common.validation_failed")
	}
	seen := map[string]bool{}
	for _, v := range g.Items {
		key := v.Kind + ":" + v.Id
		if !uuidPattern.MatchString(v.Id) || (v.Kind != "member" && v.Kind != "group" && v.Kind != "role") || seen[key] {
			return failure("common.validation_failed")
		}
		seen[key] = true
	}
	return nil
}

func list(ctx context.Context, tx pgx.Tx, tenant string, o operation, r *http.Request) (result, error) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	cursor, _ := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("cursor"))
	after := string(cursor)
	table := "ai.model"
	if o.name == "listProviders" {
		table = "ai.provider"
	}
	rows, e := tx.Query(ctx, "SELECT document,revision FROM "+table+" WHERE tenant_id=$1 AND id::text>$2 ORDER BY id::text LIMIT $3", tenant, after, limit+1)
	if e != nil {
		return result{}, e
	}
	defer rows.Close()
	providers := []api.Provider{}
	models := []api.Model{}
	var next *string
	last := ""
	count := 0
	for rows.Next() {
		var b []byte
		var revision int64
		if e = rows.Scan(&b, &revision); e != nil {
			return result{}, e
		}
		if count == limit {
			v := base64.RawURLEncoding.EncodeToString([]byte(last))
			next = &v
			break
		}
		if o.name == "listProviders" {
			var v api.Provider
			if e = readDocument(b, revision, &v); e != nil {
				return result{}, e
			}
			providers = append(providers, v)
			last = v.Id
		} else {
			var v api.Model
			if e = readDocument(b, revision, &v); e != nil {
				return result{}, e
			}
			models = append(models, v)
			last = v.Id
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return result{}, e
	}
	if o.name == "listProviders" {
		return response(200, api.ProviderPage{Items: providers, NextCursor: next}, 0)
	}
	return response(200, api.ModelPage{Items: models, NextCursor: next}, 0)
}

func (s *Server) credential(ctx context.Context, tx pgx.Tx, p *Principal, o operation, raw []byte, r *http.Request) (result, error) {
	provider, e := loadProvider(ctx, tx, p.Tenant, o.provider, true)
	if e != nil {
		return result{}, e
	}
	if o.name == "createCredential" {
		var in api.CredentialInput
		if e = decode(raw, &in); e != nil {
			return result{}, e
		}
		if !validLength(in.Secret, 1, 16384) {
			return result{}, failure("common.validation_failed")
		}
		id, err := newID()
		if err != nil {
			return result{}, err
		}
		encrypted, err := s.cipher.Encrypt([]byte(in.Secret), Binding{p.Tenant, o.provider, id, 1})
		if err != nil {
			return result{}, err
		}
		v := api.Credential{Id: id, State: "staged", Revision: 1}
		e = tx.QueryRow(ctx, "INSERT INTO ai.credential(tenant_id,id,provider_id,state,ciphertext,cipher_version) VALUES ($1,$2,$3,'staged',$4,1) RETURNING created_at", p.Tenant, id, o.provider, encrypted).Scan(&v.CreatedAt)
		if e != nil {
			return result{}, e
		}
		return response(201, v, 1)
	}
	var action api.CredentialAction
	if e = decode(raw, &action); e != nil {
		return result{}, e
	}
	if !validLength(action.Reason, 1, 1024) {
		return result{}, failure("common.validation_failed")
	}
	v := api.Credential{Id: o.credential}
	e = tx.QueryRow(ctx, "SELECT state,created_at,revision FROM ai.credential WHERE tenant_id=$1 AND provider_id=$2 AND id=$3 FOR UPDATE", p.Tenant, o.provider, o.credential).Scan(&v.State, &v.CreatedAt, &v.Revision)
	if e != nil {
		return result{}, e
	}
	if e = precondition(r, v.Revision); e != nil {
		return result{}, e
	}
	if o.name == "activate" {
		if v.State != "staged" {
			return result{}, failure("common.conflict")
		}
		_, e = tx.Exec(ctx, "UPDATE ai.credential SET state='retiring',revision=revision+1 WHERE tenant_id=$1 AND provider_id=$2 AND state='active'", p.Tenant, o.provider)
		if e != nil {
			return result{}, e
		}
		v.State = "active"
		provider.ActiveCredentialId = *api.NewNullableString(&v.Id)
	} else {
		if v.State != "active" && v.State != "retiring" {
			return result{}, failure("common.conflict")
		}
		v.State = "revoked"
		if provider.ActiveCredentialId.Get() != nil && *provider.ActiveCredentialId.Get() == v.Id {
			provider.ActiveCredentialId = *api.NewNullableString(nil)
		}
	}
	v.Revision++
	_, e = tx.Exec(ctx, "UPDATE ai.credential SET state=$3,revision=$4 WHERE tenant_id=$1 AND id=$2", p.Tenant, v.Id, v.State, v.Revision)
	if e != nil {
		return result{}, e
	}
	provider.Revision++
	if e = saveProvider(ctx, tx, p.Tenant, provider); e != nil {
		return result{}, e
	}
	return response(200, v, v.Revision)
}

// ValidateEndpointDNS rejects currently private DNS answers before registration.
// It is intentionally separate so CRUD never contacts a paid upstream endpoint.
func ValidateEndpointDNS(ctx context.Context, base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return failure("common.validation_failed")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addresses) == 0 {
		return failure("common.unavailable")
	}
	for _, a := range addresses {
		if !publicAddress(a) {
			return failure("common.validation_failed")
		}
	}
	return nil
}
