package network

import (
	"encoding/json"
	"strings"
	"time"
)

const sourceRequestTimeout = 30 * time.Second
const sourceRegistrationFields = "registration_id allocation_op_id allocation_fingerprint registration_revision local address_type"

func sourceOperation(op string) bool {
	return op == "source_register" || op == "source_get" || op == "source_revoke"
}

func sourceObject(raw []byte, fields string) (map[string]json.RawMessage, bool) {
	m, err := registrationObject(raw, fields)
	return m, err == nil
}

func sourceIDs(m map[string]json.RawMessage, fields string) bool {
	for _, field := range strings.Fields(fields) {
		if !opaqueID.MatchString(registrationString(m[field])) {
			return false
		}
	}
	return true
}

func sourceTimes(m map[string]json.RawMessage, fields string) bool {
	for _, field := range strings.Fields(fields) {
		s := registrationString(m[field])
		v, err := time.Parse("2006-01-02T15:04:05Z", s)
		if err != nil || v.Format("2006-01-02T15:04:05Z") != s {
			return false
		}
	}
	return true
}

func sourceRegistration(raw []byte) bool {
	m, ok := sourceObject(raw, sourceRegistrationFields)
	return ok && sourceIDs(m, "registration_id allocation_op_id") && registrationDigest.MatchString(registrationString(m["allocation_fingerprint"])) && registrationNumber(m["registration_revision"], true) && registrationNumber(m["local"], true) && registrationString(m["address_type"]) == "session"
}

func sourceNative(raw []byte, witness bool) bool {
	fields := "binding_id binding_revision"
	if witness {
		fields += " witness_id observed_at valid_until"
	}
	m, ok := sourceObject(raw, fields)
	if !ok || !sourceIDs(m, "binding_id") || !registrationNumber(m["binding_revision"], true) {
		return false
	}
	return !witness || sourceIDs(m, "witness_id") && sourceWindow(m, "observed_at", "valid_until", sourceRequestTimeout)
}

func sourceWindow(m map[string]json.RawMessage, start, end string, maxLifetime time.Duration) bool {
	if !sourceTimes(m, start+" "+end) {
		return false
	}
	a, _ := time.Parse(time.RFC3339, registrationString(m[start]))
	b, _ := time.Parse(time.RFC3339, registrationString(m[end]))
	return b.After(a) && (maxLifetime == 0 || b.Sub(a) <= maxLifetime)
}

func sourcePredecessor(raw json.RawMessage) bool {
	s := registrationString(raw)
	return string(raw) == `""` || opaqueID.MatchString(s)
}

// Consumer validation keeps unknown optional fields and exact field names.
// It establishes no native authority, freshness lease or SEND permission.
func decodeSourceRequest(raw []byte, operation string) (map[string]json.RawMessage, *AppError) {
	fields := "app_version type request_id epoch boot_id"
	switch operation {
	case "source_register":
		fields += " operation_id mode registration scope expected_revision predecessor_binding native_witness"
	case "source_get":
		fields += " source_binding registration native_witness"
	case "source_revoke":
		fields += " source_binding operation_id"
	default:
		return nil, protocolError("", "source operation")
	}
	m, ok := sourceObject(raw, fields)
	valid := ok && registrationVersion.MatchString(registrationString(m["app_version"])) && registrationString(m["type"]) == operation && sourceIDs(m, "request_id boot_id") && registrationNumber(m["epoch"], true)
	switch operation {
	case "source_register":
		mode := registrationString(m["mode"])
		valid = valid && sourceIDs(m, "operation_id") && sourceRegistration(m["registration"]) && registrationString(m["scope"]) == "durable_unicast_send" && sourcePredecessor(m["predecessor_binding"]) && registrationNumber(m["expected_revision"], registrationString(m["predecessor_binding"]) != "")
		valid = valid && (registrationString(m["predecessor_binding"]) != "" || registrationString(m["expected_revision"]) == "0")
		valid = valid && (mode == "issue" && sourceNative(m["native_witness"], true) || mode == "reconcile" && string(m["native_witness"]) == "null")
	case "source_get":
		valid = valid && sourceIDs(m, "source_binding") && sourceRegistration(m["registration"]) && sourceNative(m["native_witness"], true)
	case "source_revoke":
		valid = valid && sourceIDs(m, "source_binding operation_id")
	}
	if !valid {
		return nil, protocolError(registrationString(m["request_id"]), "source frame")
	}
	return m, nil
}

func sourceEqual(a, b json.RawMessage, fields string) bool {
	x, ok := sourceObject(a, fields)
	y, other := sourceObject(b, fields)
	if !ok || !other {
		return false
	}
	for _, field := range strings.Fields(fields) {
		if registrationString(x[field]) != registrationString(y[field]) {
			return false
		}
	}
	return true
}

func sourceFact(raw []byte, q map[string]json.RawMessage, operation string) (map[string]json.RawMessage, bool) {
	m, ok := sourceObject(raw, "source_binding registration scope revision state not_before expires_at predecessor_binding native_binding")
	if !ok || !sourceIDs(m, "source_binding") || !sourceRegistration(m["registration"]) || !sourceEqual(m["registration"], q["registration"], sourceRegistrationFields) || registrationString(m["scope"]) != "durable_unicast_send" || !registrationNumber(m["revision"], true) || !sourcePredecessor(m["predecessor_binding"]) || !sourceNative(m["native_binding"], false) || !sourceWindow(m, "not_before", "expires_at", 0) {
		return nil, false
	}
	state := registrationString(m["state"])
	if state != "active" && state != "expired" && state != "revoked" && state != "stopped" {
		return nil, false
	}
	if operation == "source_register" {
		ok = registrationString(m["revision"]) == "1" && state == "active" && registrationString(m["predecessor_binding"]) == registrationString(q["predecessor_binding"])
	} else {
		ok = registrationString(m["source_binding"]) == registrationString(q["source_binding"])
	}
	if string(q["native_witness"]) != "null" {
		ok = ok && sourceEqual(m["native_binding"], q["native_witness"], "binding_id binding_revision")
	}
	return m, ok
}

func sourceFailure(raw []byte, result string) bool {
	m, ok := sourceObject(raw, "authority reason request_digest")
	if !ok || !registrationDigest.MatchString(registrationString(m["request_digest"])) {
		return false
	}
	a, reason := registrationString(m["authority"]), registrationString(m["reason"])
	if result == "not_accepted" {
		return a == "collab" && reason == "rejected_before_pending"
	}
	return result == "not_committed" && a == "iam" && (reason == "original_connection_invalid_uncommitted" || reason == "revision_mismatch" || reason == "predecessor_not_head" || reason == "lineage_stopped")
}

// Only validate structure, association and signing audience. The plugin remains
// the authoritative verifier of the complete signed original object.
func validateSourceResponse(b liveBinding, operation, requestID string, request, response []byte) error {
	q, ae := decodeSourceRequest(request, operation)
	m, ok := sourceObject(response, "app_version type request_id server_auth")
	if ae != nil || !ok || !registrationVersion.MatchString(registrationString(m["app_version"])) || registrationString(q["epoch"]) != u64str(b.Epoch) || requestID != registrationString(q["request_id"]) || requestID != registrationString(m["request_id"]) {
		return errUnauthorized
	}
	kind := registrationString(m["type"])
	if kind == "error" {
		_, ok = sourceObject(response, "code scope retryable retry_after")
		ok = ok && validRegistrationError(m)
	} else if kind != operation+"_result" {
		return errUnauthorized
	} else {
		switch operation {
		case "source_register":
			_, ok = sourceObject(response, "operation_id source result failure_evidence")
			ok = ok && registrationString(m["operation_id"]) == registrationString(q["operation_id"])
			if registrationString(m["result"]) == "registered" {
				_, factOK := sourceFact(m["source"], q, operation)
				ok = ok && factOK && string(m["failure_evidence"]) == "null"
			} else {
				ok = ok && string(m["source"]) == "null" && sourceFailure(m["failure_evidence"], registrationString(m["result"]))
			}
		case "source_get":
			_, ok = sourceObject(response, "source policy_revision observed_at witness_id")
			fact, factOK := sourceFact(m["source"], q, operation)
			witness, _ := sourceObject(q["native_witness"], "witness_id")
			ok = ok && factOK && registrationNumber(m["policy_revision"], true) && sourceTimes(m, "observed_at") && registrationString(m["witness_id"]) == registrationString(witness["witness_id"])
			if registrationString(fact["state"]) == "active" {
				ok = ok && sourceTimes(m, "valid_until")
			} else {
				ok = ok && m["valid_until"] == nil
			}
		case "source_revoke":
			_, ok = sourceObject(response, "operation_id source_binding revision state lineage_stopped")
			state := registrationString(m["state"])
			ok = ok && registrationString(m["operation_id"]) == registrationString(q["operation_id"]) && registrationString(m["source_binding"]) == registrationString(q["source_binding"]) && registrationNumber(m["revision"], true) && (state == "revoked" || state == "expired") && string(m["lineage_stopped"]) == "true"
		}
	}
	if !ok {
		return errUnauthorized
	}
	auth, ok := sourceObject(m["server_auth"], "issued_at not_after")
	if !ok || !sourceWindow(auth, "issued_at", "not_after", 300*time.Second) {
		return errUnauthorized
	}
	return validateRegistrationAuth(m["server_auth"], b, requestID)
}
