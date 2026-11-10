package bifrostbackend

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaylimits"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func reject(w http.ResponseWriter, err error) {
	status, code := 503, "common.unavailable"
	var f fault
	var be *budget.Error
	if errors.As(err, &f) {
		code = string(f)
	} else if errors.As(err, &be) {
		code = be.Code
	}
	switch code {
	case "common.unauthenticated":
		status = 401
	case "common.forbidden":
		status = 403
	case "common.not_found":
		status = 404
	case "common.validation_failed":
		status = 400
	case "common.rate_limited", "ai.budget_exceeded":
		status = 429
	case "common.conflict", "ai.request_in_progress", "ai.replay_unavailable":
		status = 409
	case "common.precondition_failed":
		status = 412
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "title": http.StatusText(status)})
}
func decode(w http.ResponseWriter, r *http.Request, value any) error {
	limit := int64(1 << 20)
	if r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions" {
		limit = relaylimits.ChatBodyBytes
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}
func (b *Backend) basic(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	u, p, ok := r.BasicAuth()
	sum := sha256.Sum256([]byte(p))
	return ok && u == "auriga" && hmac.Equal(sum[:], b.password[:])
}
func (b *Backend) memberBasic(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	u, p, ok := r.BasicAuth()
	sum := sha256.Sum256([]byte(p))
	return ok && u == "auriga-member" && hmac.Equal(sum[:], b.memberPassword[:])
}
func bearer(r *http.Request) string {
	if len(r.Header.Values("X-Bf-Vk")) == 1 {
		return r.Header.Get("X-Bf-Vk")
	}
	if len(r.Header.Values("Authorization")) != 1 {
		return ""
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}
func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
		reject(w, denied)
		return
	}
	if r.URL.Path == "/health" && r.Method == http.MethodGet {
		respond(w, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path == "/internal/members/ensure" {
		if r.Method != http.MethodPost || !b.memberBasic(r) || r.URL.RawQuery != "" {
			reject(w, denied)
			return
		}
		var m api.RelayMemberEnsure
		if e := decode(w, r, &m); e != nil {
			reject(w, e)
			return
		}
		out, e := b.EnsureMember(r.Context(), m)
		if e != nil {
			reject(w, e)
			return
		}
		respond(w, out)
		return
	}
	if r.URL.Path == "/internal/keys/check" && r.Method == http.MethodGet {
		tenant, id, e := b.parseToken(bearer(r))
		if e != nil {
			reject(w, e)
			return
		}
		k, e := b.readKey(r.Context(), tenant, id, false)
		if e != nil {
			reject(w, e)
			return
		}
		respond(w, map[string]any{"name": k.Name, "active": k.Active, "source": k.Source})
		return
	}
	if r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/models" {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			reject(w, invalid)
			return
		}
		tenant, id, e := b.parseToken(bearer(r))
		if e != nil {
			reject(w, e)
			return
		}
		k, e := b.readKey(r.Context(), tenant, id, false)
		if e != nil {
			reject(w, e)
			return
		}
		// Member keys are internal capabilities. A leaked value never bypasses IAM/org.
		if k.Source == "member" && !b.memberBasic(r) {
			reject(w, denied)
			return
		}
		if r.URL.Path == "/v1/models" {
			if r.Method != http.MethodGet {
				reject(w, invalid)
				return
			}
			data := []map[string]any{}
			page := listPage{Limit: 200}
			for {
				pools, e := b.ListPools(r.Context(), tenant, page)
				if e != nil {
					reject(w, e)
					return
				}
				for _, p := range pools.Items {
					if p.Enabled && (slices.Contains(k.Models, "*") || slices.Contains(k.Models, p.Id)) {
						data = append(data, publicModel(p))
					}
					page.After = p.Id
				}
				if pools.NextCursor == nil {
					break
				}
			}
			respond(w, map[string]any{"object": "list", "data": data})
			return
		}
		if r.Method != http.MethodPost {
			reject(w, invalid)
			return
		}
		b.chat(w, r, tenant, k)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		reject(w, missing)
		return
	}
	if !b.basic(r) {
		reject(w, fault("common.unauthenticated"))
		return
	}
	tenants := r.Header.Values("X-Auriga-Tenant")
	if len(tenants) != 1 || !validUUID(tenants[0]) || (b.tenant != "" && tenants[0] != b.tenant) {
		reject(w, denied)
		return
	}
	if r.Method == http.MethodPost {
		b.idempotentManagement(w, r, tenants[0])
		return
	}
	if e := b.management(w, r, tenants[0]); e != nil {
		reject(w, e)
	}
}

func (b *Backend) management(w http.ResponseWriter, r *http.Request, tenant string) error {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	list := len(parts) == 1 && r.Method == http.MethodGet && (parts[0] == "providers" || parts[0] == "pools" || parts[0] == "virtual-keys")
	var page listPage
	if list {
		var err error
		page, err = parseListPage(r, tenant, parts[0])
		if err != nil {
			return err
		}
	}
	if !list && parts[0] != "logs" && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		return invalid
	}
	switch {
	case len(parts) == 1 && parts[0] == "providers" && r.Method == http.MethodGet:
		out, e := b.ListProviders(r.Context(), tenant, page)
		if e != nil {
			return e
		}
		respond(w, out)
	case len(parts) == 3 && parts[0] == "providers" && parts[2] == "models" && r.Method == http.MethodGet:
		out, e := b.ProviderModels(r.Context(), tenant, parts[1])
		if e != nil {
			rejectCatalog(w, e)
			return nil
		}
		respond(w, out)
	case parts[0] == "providers" && ((len(parts) == 1 && r.Method == http.MethodPost) || (len(parts) == 2 && r.Method == http.MethodPut)):
		var p api.RelayProviderInput
		if e := decode(w, r, &p); e != nil {
			return e
		}
		id := ""
		if len(parts) == 2 {
			id = parts[1]
		}
		out, e := b.SaveProvider(r.Context(), tenant, id, p, nil)
		if e != nil {
			return e
		}
		respond(w, out)
	case parts[0] == "providers" && len(parts) == 3 && parts[2] == "keys" && r.Method == http.MethodPost:
		var input api.CredentialInput
		if e := decode(w, r, &input); e != nil {
			return e
		}
		out, e := b.SaveProvider(r.Context(), tenant, parts[1], api.RelayProviderInput{}, &input.Secret)
		if e != nil {
			return e
		}
		respond(w, out)
	case len(parts) == 1 && parts[0] == "pools" && r.Method == http.MethodGet:
		out, e := b.ListPools(r.Context(), tenant, page)
		if e != nil {
			return e
		}
		respond(w, out)
	case len(parts) == 1 && parts[0] == "pools" && r.Method == http.MethodPost:
		createOnly, err := poolCreateOnly(r)
		if err != nil {
			return err
		}
		var p api.RelayPool
		if e := decode(w, r, &p); e != nil {
			return e
		}
		p, err = resolvePoolCapabilities(p)
		if err != nil {
			return err
		}
		if createOnly {
			err = b.CreatePool(r.Context(), tenant, p)
		} else {
			err = b.SavePool(r.Context(), tenant, p)
		}
		if err != nil {
			return err
		}
		respond(w, p)
	case len(parts) == 1 && parts[0] == "virtual-keys" && r.Method == http.MethodGet:
		out, e := b.ListKeys(r.Context(), tenant, page)
		if e != nil {
			return e
		}
		respond(w, out)
	case parts[0] == "virtual-keys" && ((len(parts) == 1 && r.Method == http.MethodPost) || (len(parts) == 2 && r.Method == http.MethodPut && validUUID(parts[1]))):
		var input api.RelayKeyInput
		if e := decode(w, r, &input); e != nil {
			return e
		}
		id := ""
		if len(parts) == 2 {
			id = parts[1]
		}
		out, e := b.SaveKey(r.Context(), tenant, id, input)
		if e != nil {
			return e
		}
		if id != "" {
			respond(w, out.Key)
		} else {
			respond(w, out)
		}
	case len(parts) == 1 && parts[0] == "logs" && r.Method == http.MethodGet:
		q, e := urlQuery(r)
		if e != nil {
			return e
		}
		id := q["keyId"]
		account := q["accountId"]
		if (id == "") == (account == "") {
			return invalid
		}
		if account != "" {
			id = account
		}
		if !validUUID(id) {
			return invalid
		}
		// Logs remain queryable after revocation. readKey intentionally is not used.
		var k api.RelayKey
		e = b.db.Transaction(r.Context(), tenant, func(tx pgx.Tx) error {
			var raw []byte
			if err := tx.QueryRow(r.Context(), `SELECT document FROM ai.relay_key WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&raw); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return missing
				}
				return err
			}
			if json.Unmarshal(raw, &k) != nil {
				return denied
			}
			return nil
		})
		if e != nil {
			return e
		}
		if account != "" && (k.Source != "member" || k.AccountId != account) {
			return missing
		}
		limit := 50
		if q["limit"] != "" {
			limit, e = strconv.Atoi(q["limit"])
			if e != nil || limit < 1 || limit > 200 {
				return invalid
			}
		}
		out, e := b.ledger.ListUsage(r.Context(), tenant, id, limit, q["cursor"])
		if e != nil {
			return e
		}
		respond(w, out)
	default:
		return missing
	}
	return nil
}

func poolCreateOnly(r *http.Request) (bool, error) {
	values := r.Header.Values("If-None-Match")
	if len(values) == 0 {
		return false, nil
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/pools" || len(values) != 1 || values[0] != "*" {
		return false, invalid
	}
	return true, nil
}

func urlQuery(r *http.Request) (map[string]string, error) {
	out := map[string]string{}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, invalid
	}
	for k, v := range q {
		if !slices.Contains([]string{"keyId", "accountId", "limit", "cursor"}, k) || len(v) != 1 || v[0] == "" {
			return nil, invalid
		}
		out[k] = v[0]
	}
	return out, nil
}

func (b *Backend) chat(w http.ResponseWriter, r *http.Request, tenant string, key api.RelayKey) {
	var fields map[string]json.RawMessage
	if e := decode(w, r, &fields); e != nil || fields == nil {
		reject(w, invalid)
		return
	}
	allowed := []string{"model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "tool_choice", "response_format", "seed", "stop", "n", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs", "parallel_tool_calls", "reasoning_effort", "service_tier", "user", "metadata"}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			reject(w, invalid)
			return
		}
	}
	var model string
	var stream bool
	var messages []json.RawMessage
	if json.Unmarshal(fields["model"], &model) != nil || !modelID.MatchString(model) || json.Unmarshal(fields["messages"], &messages) != nil || len(messages) < 1 {
		reject(w, invalid)
		return
	}
	if v := fields["stream"]; v != nil && json.Unmarshal(v, &stream) != nil {
		reject(w, invalid)
		return
	}
	if !slices.Contains(key.Models, "*") && !slices.Contains(key.Models, model) {
		reject(w, denied)
		return
	}
	pool, routes, e := b.poolRoutes(r.Context(), tenant, model)
	if e != nil {
		reject(w, e)
		return
	}
	if e = b.reconcileSubject(r.Context(), tenant, key.Id); e != nil {
		reject(w, e)
		return
	}
	maxTokens, field, limitErr := outputTokenLimit(fields, pool.MaxOutputTokens)
	if limitErr != nil {
		rejectOutputTokenLimit(w, field, pool.MaxOutputTokens)
		return
	}
	if v := fields["n"]; v != nil {
		var n int
		if json.Unmarshal(v, &n) != nil || n != 1 {
			reject(w, invalid)
			return
		}
	}
	key, e = b.readKey(r.Context(), tenant, key.Id, true)
	if e != nil {
		reject(w, e)
		return
	}
	if !key.GetMeteringOnly() && maxTokens == 0 {
		rejectOutputTokenLimit(w, "/max_tokens", 0)
		return
	}
	delete(fields, "metadata")
	fields["user"], _ = json.Marshal(b.pseudonym(tenant, key.Id))
	if stream {
		fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	raw, e := json.Marshal(fields)
	if e != nil {
		reject(w, invalid)
		return
	}
	expiry := time.Now().Add(150 * time.Second)
	device := key.Id
	if key.Source == "member" && validUUID(r.Header.Get("X-Auriga-Device")) {
		device = r.Header.Get("X-Auriga-Device")
	}
	inputEstimate, estimateErr := inputTokenEstimate(raw, key.Source == "member" && key.GetMeteringOnly())
	if estimateErr != nil {
		rejectInputAccounting(w, estimateErr)
		return
	}
	outputEstimate := maxTokens
	if outputEstimate == 0 {
		outputEstimate = max(int64(1), pool.MaxOutputTokens)
	}
	outputEstimate = protocolOutputEstimate(routes, outputEstimate)
	admission := &ai.BudgetAdmission{TenantId: tenant, AccountId: key.Id, ModelId: pool.Id, ProviderId: routes[0].ID, CredentialId: routes[0].ID, Currency: "USD", PriceVersion: pool.Price.Version, InputTokenUpperBound: inputEstimate, MaxOutputTokens: outputEstimate, MonthlyBudgetMicros: &key.MonthlyBudgetMicros, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: device}, TokenExpiresAt: timestamppb.New(expiry)}
	if key.Source == "member" && key.GetMeteringOnly() {
		admission.MeteringOnly = true
		admission.MonthlyBudgetMicros = nil
	}
	snapshot := budget.TrustedSnapshot{Admission: admission, Price: pool.Price, ValidUntil: expiry}
	reservation, e := b.admit(r.Context(), key, snapshot, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Key: r.Header.Get("Idempotency-Key"), Fingerprint: sha256.Sum256(raw)})
	if e != nil {
		reject(w, e)
		return
	}
	ref := budget.Ref{TenantID: tenant, AccountID: key.Id, RequestID: reservation.Admission.RequestId}
	reservation, e = b.ledger.MarkDispatching(r.Context(), ref, reservation.Revision)
	if e != nil {
		reject(w, e)
		return
	}
	var body []byte
	started := false
	result, runErr := Run(r.Context(), routes, raw, b.allowPrivate, func(chunk []byte) error {
		if !stream {
			body = bytes.Clone(chunk)
			return nil
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			started = true
		}
		if _, err := w.Write(append(append([]byte("data: "), chunk...), []byte("\n\n")...)); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	})
	finish := budget.Finish{Outcome: "failed", ExecutionStopped: true, Usage: &budget.FinalUsage{Price: pool.Price}}
	if !result.NoCharge {
		finish.Estimated = true
		finish.Usage.InputTokens = inputEstimate
		finish.Usage.OutputTokens = min(outputEstimate, result.ObservedBytes)
		if admission.MeteringOnly {
			finish.Usage.OutputTokens = result.ObservedBytes
		}
		if result.ObservedBytes == 0 {
			finish.Usage.OutputTokens = outputEstimate
		}
		if admission.MeteringOnly && result.ObservedBytes == 0 {
			finish.Usage.OutputTokens = 0
		}
		if runErr == nil {
			finish.Outcome = "completed"
		}
	}
	if result.Confirmed {
		finish.Estimated = false
		finish.Outcome = "completed"
		if runErr != nil {
			finish.Outcome = "failed"
		}
		finish.Usage = &budget.FinalUsage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, Price: pool.Price}
	}
	settleCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, settleErr := b.ledger.Settle(settleCtx, ref, reservation.Revision, finish)
	if runErr != nil || settleErr != nil {
		if !started {
			if settleErr != nil {
				reject(w, fault("common.unavailable"))
			} else {
				providerReject(w, runErr)
			}
		} else {
			_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"code\":\"common.unavailable\",\"message\":\"Relay request incomplete\"}}\n\n")
		}
		return
	}
	if stream {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func providerReject(w http.ResponseWriter, err error) {
	status, code := 502, "provider_unavailable"
	var upstream *ProviderFailure
	if errors.As(err, &upstream) {
		status, code = upstream.Status, upstream.Code
	} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status, code = 504, "provider_timeout"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "provider_error", "code": code, "message": code}})
}
