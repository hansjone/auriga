package network

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
)

const SubmitPath = "/api/ai/v1/network/submit"

type verifySubmitDevice func(context.Context, string) (*iam.VerifyDeviceAccessResponse, error)

// Unverified tid is used only to route the existing delegated VerifyDeviceAccess
// RPC. No claim authorizes routing to a mailbox until every identity is echoed
// by IAM, including its current owner/backing-session/expiry checks.
func submitDeviceVerifier(client iam.DeviceAccessServiceClient) verifySubmitDevice {
	return func(ctx context.Context, raw string) (*iam.VerifyDeviceAccessResponse, error) {
		claims := jwt.MapClaims{}
		if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
			return nil, errUnauthorized
		}
		tenant, _ := claims["tid"].(string)
		device, _ := claims["did"].(string)
		account, _ := claims["sub"].(string)
		if !uuidID.MatchString(tenant) || !uuidID.MatchString(device) || !uuidID.MatchString(account) {
			return nil, errUnauthorized
		}
		ctx = team.WithActor(ctx, &collab.Actor{TenantId: tenant})
		v, err := client.VerifyDeviceAccess(ctx, &iam.VerifyDeviceAccessRequest{AccessToken: raw})
		if err != nil {
			return nil, err
		}
		if v.GetTenantId() != tenant || v.GetDeviceId() != device || v.GetAccountId() != account {
			return nil, errUnauthorized
		}
		return v, nil
	}
}

type submitHandler struct {
	redeemer *IAMRedeemer
	client   *MailboxClient
	verify   verifySubmitDevice
	// Authentication is bounded separately so slow submissions cannot consume
	// every slot needed to verify another tenant's device.
	verifySlots chan struct{}
	admission   submitAdmission
}

func newSubmitHandler(r *IAMRedeemer, c *MailboxClient, v verifySubmitDevice) *submitHandler {
	return &submitHandler{redeemer: r, client: c, verify: v, verifySlots: make(chan struct{}, mailboxWorkers)}
}

// SubmitHandler shares the callout runtime's current READY bindings. It cannot
// be mounted in a separate process with a separately guessed connection tuple.
func (r *Runtime) SubmitHandler() http.Handler {
	return newSubmitHandler(r.redeemer, r.mailbox.client, submitDeviceVerifier(iam.NewDeviceAccessServiceClient(r.iam)))
}

func (h *submitHandler) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if q.URL.Path != SubmitPath {
		http.NotFound(w, q)
		return
	}
	if q.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		submitError(w, 405, CodeProtocolError)
		return
	}
	if q.TLS == nil {
		submitError(w, 400, CodeProtocolError)
		return
	}
	media, _, err := mime.ParseMediaType(q.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || q.URL.RawQuery != "" || submitIdentityHeaders(q.Header) {
		submitError(w, 400, CodeProtocolError)
		return
	}
	ctx, cancel := context.WithTimeout(q.Context(), 10*time.Second)
	defer cancel()
	auth := q.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") || len(auth[0]) > 16384 {
		submitError(w, 401, "network.unauthorized")
		return
	}
	select {
	case h.verifySlots <- struct{}{}:
	default:
		submitError(w, 429, "network.overloaded")
		return
	}
	v, err := h.verify(ctx, strings.TrimPrefix(auth[0], "Bearer "))
	<-h.verifySlots
	if err != nil || !v.GetActive() || !uuidID.MatchString(v.GetTenantId()) || !uuidID.MatchString(v.GetDeviceId()) || !uuidID.MatchString(v.GetAccountId()) || v.GetExpiresAt() == nil || !v.GetExpiresAt().IsValid() || !v.GetExpiresAt().AsTime().After(time.Now()) {
		submitError(w, 401, "network.unauthorized")
		return
	}
	b, code := h.redeemer.submitBinding(v.GetTenantId(), v.GetDeviceId())
	if code != 0 {
		submitError(w, code, "network.unauthorized")
		return
	}
	if !h.admission.acquire(v.GetTenantId(), v.GetDeviceId()) {
		submitError(w, 429, "network.overloaded")
		return
	}
	defer h.admission.release(v.GetTenantId(), v.GetDeviceId())
	raw, err := io.ReadAll(http.MaxBytesReader(w, q.Body, EncodingMaxPayloadBytes))
	if err != nil || !validSubmitJSON(raw) {
		submitError(w, 400, CodeProtocolError)
		return
	}
	result, ae := h.client.Call(ctx, b, "submit", raw)
	if ae != nil {
		code := 502
		switch ae.Code {
		case "network.unauthorized", "network.revoked", "network.expired":
			code = 401
		case CodeConflict, CodeStaleEpoch:
			code = 409
		case CodeProtocolError:
			code = 400
		}
		submitError(w, code, ae.Code)
		return
	}
	// A completed RPC may have committed even if the connection was lost. Return
	// only its original result; on transport failure the caller retains the key.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

func (r *IAMRedeemer) submitBinding(tenant, device string) (liveBinding, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found liveBinding
	count := 0
	for _, b := range r.bindings {
		if b.Tenant == tenant && b.Device == device && b.Epoch != 0 && !b.Revoked && time.Now().Before(b.NotAfter) {
			found = b
			count++
		}
	}
	if count == 0 {
		return liveBinding{}, 401
	}
	if count != 1 {
		return liveBinding{}, 409
	}
	return found, 0
}

func submitIdentityHeaders(h http.Header) bool {
	for _, k := range []string{"Auriga-Tenant-Id", "X-Tenant-Id", "X-Node-Id", "X-Owner-Id", "X-Device-Id", "X-Network-Connection", "X-Network-Ingress"} {
		if len(h.Values(k)) != 0 {
			return true
		}
	}
	return false
}

// Identity and platform-owned fields are forbidden even as optional extensions.
// Other unknown optional Message content stays untouched (network README §1).
// Collab performs full schema and source-observation validation.
func validSubmitJSON(raw []byte) bool {
	m, err := registrationObject(raw, "kind to durability body source idempotency")
	if err != nil {
		return false
	}
	for _, k := range strings.Fields("from tenant tenant_id realm node owner owner_id user user_id account account_id device device_id ingress server_id client_id client_account connection connection_tuple sender_kind message_class reply_to source_context server_auth authorization_ref origin correlation_id causation_id") {
		if m[k] != nil {
			return false
		}
	}
	s, ok := sourceObject(m["source"], "source_binding")
	if !ok || len(s) != 1 || !sourceIDs(s, "source_binding") {
		return false
	}
	i, ok := sourceObject(m["idempotency"], "producer_epoch key")
	return ok && len(i) == 2 && sourceIDs(i, "key") && registrationNumber(i["producer_epoch"], true)
}

func submitError(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	result := map[string]any{"code": reason}
	if reason == "network.overloaded" {
		result["retryable"] = true
	}
	_ = json.NewEncoder(w).Encode(result)
}
