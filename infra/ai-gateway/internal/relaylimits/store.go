package relaylimits

import (
	"bytes"
	"encoding/json"
)

// DropStoreOptOut removes an OpenAI `store` opt-out from a chat request.
//
// Standard OpenAI-compatible clients (for example pi-ai inside DSH) send
// `"store": false` to any endpoint they treat as standard. The relay never
// retains conversations and never forwards `store` upstream, so an opt-out (or
// an explicit null) is satisfied by dropping it. A request to store (`true` or
// any other value) cannot be honoured and is reported as invalid; the caller
// rejects it and names the `/store` field.
func DropStoreOptOut(fields map[string]json.RawMessage) bool {
	raw, present := fields["store"]
	if !present {
		return true
	}
	value := bytes.TrimSpace(raw)
	if !bytes.Equal(value, []byte("false")) && !bytes.Equal(value, []byte("null")) {
		return false
	}
	delete(fields, "store")
	return true
}
