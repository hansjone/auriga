package network

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

var registrationDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var registrationErrorCode = regexp.MustCompile(`^network\.[a-z][a-z0-9_]{0,63}$`)
var registrationVersion = regexp.MustCompile(`^1\.(?:0|[1-9][0-9]{0,3})$`)

const localRegisterFields = "app_version type request_id epoch boot_id candidate_local allocation_op_id allocation_fingerprint expected_registration_revision address_type"
const registerResultFields = "app_version type request_id local registration_id allocation_op_id result registration_revision local_high_water address_type server_auth"

// Network consumers preserve unknown optional fields. Extract known names
// exactly after strict preflight; struct decoding would fold case and allow an
// unknown optional "TYPE" or "Epoch" to overwrite a known structural field.
func registrationObject(raw []byte, required string) (map[string]json.RawMessage, error) {
	if validateRegistrationJSON(raw) != nil {
		return nil, errUnauthorized
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, errUnauthorized
	}
	for _, key := range strings.Fields(required) {
		if m[key] == nil {
			return nil, errUnauthorized
		}
	}
	if feature := m["required_features"]; feature != nil {
		// LocalRegister has no negotiated required-feature extension. Never
		// silently execute a request/response requiring an unknown capability.
		var required []string
		if json.Unmarshal(feature, &required) != nil || required == nil || len(required) != 0 {
			return nil, errUnauthorized
		}
	}
	return m, nil
}

func registrationString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func registrationNumber(raw json.RawMessage, positive bool) bool {
	s := registrationString(raw)
	v, ae := parseU64("registration number", s)
	return ae == nil && s == u64str(v) && (!positive || v != 0)
}

func decodeLocalRegister(raw []byte) (map[string]json.RawMessage, *AppError) {
	m, err := registrationObject(raw, localRegisterFields)
	valid := err == nil && registrationVersion.MatchString(registrationString(m["app_version"])) && registrationString(m["type"]) == "local_register"
	for _, field := range []string{"request_id", "boot_id", "allocation_op_id"} {
		valid = valid && opaqueID.MatchString(registrationString(m[field]))
	}
	for _, field := range []string{"epoch", "candidate_local", "expected_registration_revision"} {
		valid = valid && registrationNumber(m[field], field != "expected_registration_revision")
	}
	kind := registrationString(m["address_type"])
	if !valid || !registrationDigest.MatchString(registrationString(m["allocation_fingerprint"])) || kind != "session" && kind != "subagent" {
		return nil, protocolError(registrationString(m["request_id"]), "local_register")
	}
	return m, nil
}

// Validate structure/association only. The plugin verifies the cryptographic
// signature against its trusted keyset; the gateway never re-signs this body.
func validateRegisterResponse(b liveBinding, requestID string, request, response []byte) error {
	q, ae := decodeLocalRegister(request)
	if ae != nil {
		return errUnauthorized
	}
	m, err := registrationObject(response, "app_version type request_id server_auth")
	if err != nil || !registrationVersion.MatchString(registrationString(m["app_version"])) || registrationString(m["request_id"]) != requestID || requestID != registrationString(q["request_id"]) {
		return errUnauthorized
	}
	fields := registerResultFields
	kind := registrationString(m["type"])
	if kind == "error" {
		fields = "app_version type request_id code scope retryable retry_after server_auth"
	}
	for _, field := range strings.Fields(fields) {
		if m[field] == nil {
			return errUnauthorized
		}
	}
	if kind == "error" {
		if !validRegistrationError(m) {
			return errUnauthorized
		}
	} else {
		if kind != "register_result" || !opaqueID.MatchString(registrationString(m["registration_id"])) {
			return errUnauthorized
		}
		for _, field := range []string{"local", "registration_revision", "local_high_water"} {
			if !registrationNumber(m[field], true) {
				return errUnauthorized
			}
		}
		result := registrationString(m["result"])
		if result != "registered" && result != "existing" || registrationString(m["allocation_op_id"]) != registrationString(q["allocation_op_id"]) || registrationString(m["local"]) != registrationString(q["candidate_local"]) || registrationString(m["address_type"]) != registrationString(q["address_type"]) {
			return errUnauthorized
		}
	}
	return validateRegistrationAuth(m["server_auth"], b, requestID)
}

func validRegistrationError(m map[string]json.RawMessage) bool {
	// The existing signed Error remains opaque apart from its known fields;
	// collab owns error visibility/presentation, as on the other mailbox RPCs.
	code := registrationString(m["code"])
	if !registrationErrorCode.MatchString(code) {
		return false
	}
	scope := registrationString(m["scope"])
	if scope != "request" && scope != "recipient" && scope != "contact" && scope != "connection" && scope != "node" {
		return false
	}
	if string(m["retryable"]) != "true" && string(m["retryable"]) != "false" {
		return false
	}
	if string(m["retry_after"]) != "null" {
		var n int
		if json.Unmarshal(m["retry_after"], &n) != nil || n < 1 || n > 86400 {
			return false
		}
	}
	if raw := m["related"]; raw != nil {
		related, err := registrationObject(raw, "")
		if err != nil {
			return false
		}
		for _, key := range []string{"recipient_id", "contact_id", "message_id", "contact_scope"} {
			if related[key] == nil {
				continue
			}
			s := registrationString(related[key])
			if key == "contact_scope" {
				if s != "conversation" && s != "human_dialogue" && s != "group_join" {
					return false
				}
			} else if !opaqueID.MatchString(s) {
				return false
			}
		}
	}
	return true
}

func validateRegistrationAuth(raw []byte, b liveBinding, requestID string) error {
	m, err := registrationObject(raw, "version purpose alg kid issuer_realm issued_at not_after audience request_id channel_nonce keyset_version signature")
	if err != nil {
		return err
	}
	kid := registrationString(m["kid"])
	if registrationString(m["alg"]) != "Ed25519" || registrationString(m["purpose"]) != "protocol_response" || !keyID.MatchString(kid) || kid == "stub-unsigned" || registrationString(m["request_id"]) != requestID || !opaqueID.MatchString(registrationString(m["channel_nonce"])) || registrationString(m["channel_nonce"]) != b.ChannelNonce {
		return errUnauthorized
	}
	var version int
	if json.Unmarshal(m["version"], &version) != nil || version != 1 || !registrationNumber(m["issuer_realm"], true) || !registrationNumber(m["keyset_version"], true) {
		return errUnauthorized
	}
	for _, key := range []string{"issued_at", "not_after"} {
		if _, err := time.Parse(time.RFC3339, registrationString(m[key])); err != nil {
			return errUnauthorized
		}
	}
	signature := registrationString(m["signature"])
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || len(sig) != 64 || base64.RawURLEncoding.EncodeToString(sig) != signature {
		return errUnauthorized
	}
	audience, err := registrationObject(m["audience"], "realm node incarnation epoch")
	if err != nil {
		return err
	}
	for _, key := range []string{"realm", "node", "epoch"} {
		if !registrationNumber(audience[key], true) {
			return errUnauthorized
		}
	}
	if registrationString(audience["realm"]) != b.Realm || registrationString(audience["node"]) != b.Node || registrationString(audience["epoch"]) != u64str(b.Epoch) || !opaqueID.MatchString(registrationString(audience["incarnation"])) || registrationString(audience["incarnation"]) != b.Incarnation {
		return errUnauthorized
	}
	return nil
}
