package network

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var registrationDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var registrationErrorCode = regexp.MustCompile(`^network\.[a-z][a-z0-9_]{0,63}$`)

const localRegisterFields = "app_version type request_id epoch boot_id candidate_local allocation_op_id allocation_fingerprint expected_registration_revision address_type"
const registerResultFields = "app_version type request_id local registration_id allocation_op_id result registration_revision local_high_water address_type server_auth"

// Only the small, closed objects used by LocalRegister are decoded here. Keys
// are exact (encoding/json struct matching is case insensitive), unique even
// after JSON escaping, and the entire input must be one UTF-8 JSON object.
// Values remain raw; callers validate every allowed nested object separately.
func registrationObject(raw []byte, required, optional string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > EncodingMaxPayloadBytes || !utf8.Valid(raw) {
		return nil, errUnauthorized
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, errUnauthorized
	}
	m := make(map[string]json.RawMessage)
	allowed := strings.Fields(required + " " + optional)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains(allowed, key) || m[key] != nil {
			return nil, errUnauthorized
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, errUnauthorized
		}
		m[key] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, errUnauthorized
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errUnauthorized
	}
	for _, key := range strings.Fields(required) {
		if m[key] == nil {
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
	m, err := registrationObject(raw, localRegisterFields, "")
	valid := err == nil && registrationString(m["app_version"]) == AppVersion && registrationString(m["type"]) == "local_register"
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
func validateRegisterResponse(request, response []byte) error {
	q, ae := decodeLocalRegister(request)
	if ae != nil {
		return errUnauthorized
	}
	var peek struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(response, &peek) != nil {
		return errUnauthorized
	}
	fields, optional := registerResultFields, ""
	if peek.Type == "error" {
		fields, optional = "app_version type request_id code scope retryable retry_after server_auth", "related"
	}
	m, err := registrationObject(response, fields, optional)
	if err != nil {
		return err
	}
	if peek.Type == "error" {
		if !validRegistrationError(m) {
			return errUnauthorized
		}
	} else {
		if peek.Type != "register_result" || !opaqueID.MatchString(registrationString(m["registration_id"])) {
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
	return validateRegistrationAuth(m["server_auth"])
}

func validRegistrationError(m map[string]json.RawMessage) bool {
	// The existing signed Error remains opaque apart from its closed structure;
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
		related, err := registrationObject(raw, "", "recipient_id contact_id message_id contact_scope")
		if err != nil {
			return false
		}
		for key, value := range related {
			s := registrationString(value)
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

func validateRegistrationAuth(raw []byte) error {
	m, err := registrationObject(raw, "version purpose alg kid issuer_realm issued_at not_after audience request_id channel_nonce keyset_version signature", "")
	if err != nil {
		return err
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
	audience, err := registrationObject(m["audience"], "realm node incarnation epoch", "")
	if err != nil {
		return err
	}
	for _, key := range []string{"realm", "node", "epoch"} {
		if !registrationNumber(audience[key], true) {
			return errUnauthorized
		}
	}
	return nil // Existing mailbox checks bind purpose, IDs, audience and nonce.
}
