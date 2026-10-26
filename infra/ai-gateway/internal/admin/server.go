// Package admin implements the AI control plane, without any model dispatch.
package admin

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type failure string

func (e failure) Error() string { return string(e) }

// GrantChecker must authoritatively verify tenant membership and grant ceiling.
type GrantChecker func(context.Context, *Principal, api.ModelGrants) error
type Server struct {
	store            *store.Store
	cipher           *Cipher
	authenticate     Authenticate
	checkGrants      GrantChecker
	validateEndpoint func(context.Context, string) error
}

func New(s *store.Store, c *Cipher, a Authenticate, g GrantChecker, endpoint func(context.Context, string) error) (*Server, error) {
	if s == nil || c == nil || a == nil || g == nil || endpoint == nil {
		return nil, errors.New("AI admin requires store, encryption, authentication and grant authority")
	}
	return &Server{store: s, cipher: c, authenticate: a, checkGrants: g, validateEndpoint: endpoint}, nil
}

type operation struct {
	name, permission, provider, credential, model string
	write                                         bool
}

const prefix = "/api/ai/v1/admin/"

func route(method, path string) (operation, bool) {
	if !strings.HasPrefix(path, prefix) || strings.ContainsAny(path, "%\\") {
		return operation{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	o := operation{}
	switch {
	case len(parts) == 1 && parts[0] == "usage" && method == "GET":
		o.name = "listTenantAiUsage"
	case len(parts) == 1 && parts[0] == "providers" && method == "GET":
		o.name = "listProviders"
	case len(parts) == 1 && parts[0] == "providers" && method == "POST":
		o.name = "createProvider"
	case len(parts) == 2 && parts[0] == "providers" && uuidPattern.MatchString(parts[1]) && (method == "GET" || method == "PATCH"):
		o.provider = parts[1]
		o.name = "getProvider"
		if method == "PATCH" {
			o.name = "patchProvider"
		}
	case len(parts) == 3 && parts[0] == "providers" && uuidPattern.MatchString(parts[1]) && parts[2] == "credentials" && method == "POST":
		o.name = "createCredential"
		o.provider = parts[1]
	case len(parts) == 4 && parts[0] == "providers" && uuidPattern.MatchString(parts[1]) && parts[2] == "credentials" && uuidPattern.MatchString(parts[3]) && method == "GET":
		o.name = "getCredential"
		o.provider = parts[1]
		o.credential = parts[3]
	case len(parts) == 5 && parts[0] == "providers" && uuidPattern.MatchString(parts[1]) && parts[2] == "credentials" && uuidPattern.MatchString(parts[3]) && (parts[4] == "activate" || parts[4] == "revoke") && method == "POST":
		o.name = parts[4]
		o.provider = parts[1]
		o.credential = parts[3]
	case len(parts) == 1 && parts[0] == "models" && method == "GET":
		o.name = "listModels"
	case len(parts) == 1 && parts[0] == "models" && method == "POST":
		o.name = "createModel"
	case len(parts) == 2 && parts[0] == "models" && modelPattern.MatchString(parts[1]) && (method == "PATCH" || method == "GET"):
		o.name = "getModel"
		if method == "PATCH" {
			o.name = "patchModel"
		}
		o.model = parts[1]
	case len(parts) == 3 && parts[0] == "models" && modelPattern.MatchString(parts[1]) && parts[2] == "grants" && (method == "PUT" || method == "GET"):
		o.name = "getGrants"
		if method == "PUT" {
			o.name = "grants"
		}
		o.model = parts[1]
	default:
		return o, false
	}
	o.write = method != "GET"
	switch o.name {
	case "listTenantAiUsage":
		o.permission = "ai.usage.admin"
	case "createCredential", "activate", "revoke":
		o.permission = "ai.credentials.rotate"
	case "grants":
		o.permission = "ai.models.grant"
	default:
		o.permission = "ai.providers.read"
		if o.write {
			o.permission = "ai.providers.write"
		}
	}
	return o, true
}

type result struct {
	status int
	body   []byte
	etag   string
}

// document keeps list item ETags identical to the strong single-resource header.
func document(value any, revision int64) ([]byte, error) {
	switch v := value.(type) {
	case api.Provider:
		v.Etag = etag(revision)
		value = v
	case api.Model:
		v.Etag = etag(revision)
		value = v
	}
	return json.Marshal(value)
}
func response(status int, value any, revision int64) (result, error) {
	b, err := document(value, revision)
	r := result{status: status, body: b}
	if revision > 0 {
		r.etag = etag(revision)
	}
	return r, err
}
func etag(revision int64) string { return `"` + strconv.FormatInt(revision, 10) + `"` }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	o, ok := route(r.Method, r.URL.EscapedPath())
	if !ok {
		s.problem(w, r, failure("common.not_found"))
		return
	}
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		s.problem(w, r, failure("common.unauthenticated"))
		return
	}
	p, err := s.authenticate(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if p == nil || !uuidPattern.MatchString(p.Tenant) || !uuidPattern.MatchString(p.Subject) {
		s.problem(w, r, failure("common.unauthenticated"))
		return
	}
	if p.Context != "admin" || !slices.Contains(p.Permissions, o.permission) {
		s.problem(w, r, failure("common.forbidden"))
		return
	}
	if o.write && !recentStepUp(p.StepUp, time.Now().Unix()) {
		s.problem(w, r, failure("iam.step_up_required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw := []byte(`{}`)
	if o.write {
		if ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]; ct != "application/json" {
			s.problem(w, r, failure("common.validation_failed"))
			return
		}
		raw, err = io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 {
			s.problem(w, r, failure("common.validation_failed"))
			return
		}
		raw, err = canonicalJSON(raw)
		if err != nil {
			s.problem(w, r, failure("common.validation_failed"))
			return
		}
	}
	if err = validateQuery(r, o); err != nil {
		s.problem(w, r, err)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) > 1 || len(r.Header.Get("Idempotency-Key")) > 128 || len(r.Header.Values("If-Match")) > 1 {
		s.problem(w, r, failure("common.validation_failed"))
		return
	}
	var res result
	err = s.store.Transaction(ctx, p.Tenant, func(tx pgx.Tx) error {
		key := r.Header.Get("Idempotency-Key")
		fp := s.cipher.fingerprint(raw)
		if r.Method == "POST" && key != "" {
			// The advisory lock and record belong to the same transaction as the mutation.
			scope, _ := json.Marshal([]string{p.Tenant, p.Subject, r.Method, r.URL.Path, key})
			if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", string(scope)); e != nil {
				return e
			}
			var old []byte
			e := tx.QueryRow(ctx, `SELECT fingerprint,status,body,etag FROM ai.idempotency WHERE tenant_id=$1 AND subject=$2 AND method=$3 AND path=$4 AND key=$5 AND expires_at>now()`, p.Tenant, p.Subject, r.Method, r.URL.Path, key).Scan(&old, &res.status, &res.body, &res.etag)
			if e == nil {
				if subtle.ConstantTimeCompare(old, fp) != 1 {
					return failure("common.conflict")
				}
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}
		var e error
		res, e = s.execute(ctx, tx, p, o, raw, r)
		if e != nil {
			return e
		}
		if r.Method == "POST" && key != "" {
			_, e = tx.Exec(ctx, `INSERT INTO ai.idempotency(tenant_id,subject,method,path,key,fingerprint,status,body,etag) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,subject,method,path,key) DO UPDATE SET fingerprint=EXCLUDED.fingerprint,status=EXCLUDED.status,body=EXCLUDED.body,etag=EXCLUDED.etag,expires_at=now()+interval '24 hours'`, p.Tenant, p.Subject, r.Method, r.URL.Path, key, fp, res.status, res.body, res.etag)
		}
		return e
	})
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if res.etag != "" {
		w.Header().Set("ETag", res.etag)
	}
	w.WriteHeader(res.status)
	_, _ = w.Write(res.body)
}

func recentStepUp(stepup, now int64) bool { return stepup > 0 && stepup <= now+30 && stepup >= now-600 }

func validateQuery(r *http.Request, o operation) error {
	if o.name == "listTenantAiUsage" {
		return validateUsageQuery(r)
	}
	q, err := baseQuery(r)
	if err != nil {
		return err
	}
	for key, values := range q {
		if (o.name != "listProviders" && o.name != "listModels") || (key != "limit" && key != "cursor") || len(values) != 1 {
			return failure("common.validation_failed")
		}
	}
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 200 {
			return failure("common.validation_failed")
		}
	}
	if v := q.Get("cursor"); v != "" {
		id, e := base64.RawURLEncoding.DecodeString(v)
		if e != nil || len(id) > 128 || (o.name == "listProviders" && !uuidPattern.Match(id)) || (o.name == "listModels" && !modelPattern.Match(id)) {
			return failure("common.validation_failed")
		}
	}
	return nil
}

// canonicalJSON rejects duplicates and nulls before generated contract decoding;
// generated primitive decoders otherwise accept null as a zero value.
func canonicalJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := jsonValue(d)
	if err != nil {
		return nil, err
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, errors.New("object required")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return json.Marshal(v)
}
func jsonValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("null not allowed")
	}
	if n, ok := t.(json.Number); ok {
		if len(n) > 32 {
			return nil, errors.New("JSON number exceeds supported range")
		}
		if i := strings.IndexAny(string(n), "eE"); i >= 0 {
			exponent, e := strconv.Atoi(string(n)[i+1:])
			if e != nil || exponent < -20 || exponent > 20 {
				return nil, errors.New("JSON exponent exceeds supported range")
			}
		}
		value, valid := new(big.Rat).SetString(string(n))
		if !valid {
			return nil, errors.New("invalid JSON number")
		}
		if value.IsInt() {
			return json.Number(value.Num().String()), nil
		}
		return n, nil
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	if delim == '{' {
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, exists := m[key]; exists {
				return nil, errors.New("duplicate key")
			}
			m[key], e = jsonValue(d)
			if e != nil {
				return nil, e
			}
		}
		_, err = d.Token()
		return m, err
	}
	if delim == '[' {
		a := []any{}
		for d.More() {
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return nil, errors.New("invalid JSON delimiter")
}

func (s *Server) problem(w http.ResponseWriter, r *http.Request, err error) {
	code := "common.internal"
	var f failure
	if errors.As(err, &f) {
		code = string(f)
	} else if errors.Is(err, pgx.ErrNoRows) {
		code = "common.not_found"
	} else {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
			code = "common.conflict"
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "common.unavailable"
		}
	}
	statuses := map[string]int{"common.internal": 500, "common.not_found": 404, "common.conflict": 409, "common.unauthenticated": 401, "common.forbidden": 403, "common.validation_failed": 400, "common.precondition_failed": 412, "common.unavailable": 503, "iam.step_up_required": 403}
	status := statuses[code]
	if status == 0 {
		status = 500
		code = "common.internal"
	}
	detail := http.StatusText(status)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "zh") {
		detail = map[int]string{400: "请求参数无效", 401: "请先登录", 403: "无权执行此操作", 404: "资源不存在", 409: "资源冲突", 412: "资源版本不匹配", 500: "内部错误", 503: "服务暂不可用"}[status]
	}
	p := api.NewProblem(http.StatusText(status), int32(status), code)
	p.Detail = &detail
	p.Instance = &r.URL.Path
	w.Header().Set("Content-Type", "application/problem+json")
	if status == 401 {
		w.Header().Set("WWW-Authenticate", `Bearer`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}
