package network

import "encoding/json"

func receiveOperation(op string) bool {
	return op == "receive_set" || op == "attempt_begin" || op == "execution_admit"
}

func decodeReceiveRequest(raw []byte, op string) (map[string]json.RawMessage, *AppError) {
	fields := "app_version type request_id boot_id "
	switch op {
	case "receive_set":
		fields += "epoch registration enabled expected_version"
	case "attempt_begin":
		fields += "epoch attempt_id delivery_id recipient_id purpose"
	case "execution_admit":
		fields += "connection_epoch recipient_id target host_request_id attempt_id"
	default:
		return nil, protocolError("", "receive operation")
	}
	m, ok := sourceObject(raw, fields)
	valid := ok && registrationVersion.MatchString(registrationString(m["app_version"])) && registrationString(m["type"]) == op && sourceIDs(m, "request_id boot_id")
	epoch := "epoch"
	if op == "execution_admit" {
		epoch = "connection_epoch"
	}
	valid = valid && registrationNumber(m[epoch], true)
	switch op {
	case "receive_set":
		valid = valid && sourceRegistration(m["registration"]) && registrationNumber(m["expected_version"], true) && (string(m["enabled"]) == "true" || string(m["enabled"]) == "false")
	case "attempt_begin":
		valid = valid && sourceIDs(m, "attempt_id delivery_id recipient_id") && registrationString(m["purpose"]) == "process"
	case "execution_admit":
		valid = valid && sourceIDs(m, "recipient_id host_request_id attempt_id") && registrationString(m["host_request_id"]) == registrationString(m["recipient_id"])
	}
	if !valid {
		return nil, protocolError(registrationString(m["request_id"]), "receive control")
	}
	return m, nil
}
