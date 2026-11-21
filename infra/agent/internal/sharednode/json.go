package sharednode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// A duplicate key must not name one execution to validation and another to a
// downstream parser. Limit nesting as well as bytes before decoding a request.
func validRequest(raw []byte, op string) bool {
	if !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("depth")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || keys[s] {
						return errors.New("duplicate")
					}
					keys[s] = true
					if e = value(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return errors.New("object")
				}
			case '[':
				for d.More() {
					if e = value(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return errors.New("array")
				}
			default:
				return errors.New("delimiter")
			}
		}
		return nil
	}
	if value(0) != nil {
		return false
	}
	if _, e := d.Token(); e != io.EOF {
		return false
	}
	fields := map[string][]string{
		"enterpriseOutbound": {"capability", "url", "method", "headers", "bodyBase64", "timeoutMs"},
		"health":             {}, "resolve": {"binding", "platformSessionId"}, "register": {"binding", "parentSessionId"}, "lookup": {"tenantId", "physicalInstanceId", "sessionId"}, "resourceSnapshot": {"binding"},
		"beginTask": {"binding", "taskId", "scopes", "audience", "parentCapability"}, "issueCapability": {"binding", "taskId", "scopes", "audience"}, "finishTask": {"binding", "taskId"}, "authorizeCapability": {"capability"}, "readCredentials": {"capability"},
		"model":          {"tenantId", "sessionId", "taskId", "callId", "capability", "body"},
		"messageContext": {"tenantId", "sessionId", "taskId", "capability", "operationId", "bootId", "nativeWitness"},
		"send":           {"tenantId", "sessionId", "taskId", "capability", "messageId", "target", "body", "contextId", "bootId", "nativeWitness"},
		"receive":        {"tenantId", "sessionId", "taskId", "capability", "contextId", "bootId", "nativeWitness", "operation", "frame"},
	}
	allowed, ok := fields[op]
	if !ok {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	for k := range object {
		if !has(allowed, k) {
			return false
		}
	}
	return true
}
