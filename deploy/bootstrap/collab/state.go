package collab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Compare public installed state to the approved manifest; Java remains the
// root-signature/schema/PG authority. This never constructs or signs a state.
func (c *Input) CheckState() error {
	var signed struct {
		Keyset struct {
			Issued  string `json:"issued_at"`
			Version string `json:"keyset_version"`
			Keys    []map[string]any
		}
	}
	var state struct {
		Version string
		Issued  string `json:"issued_at"`
		Keys    []map[string]any
	}
	if json.Unmarshal(c.Files["signed.json"], &signed) != nil || len(signed.Keyset.Keys) != 3 {
		return fmt.Errorf("activation manifest refused")
	}
	decoder := json.NewDecoder(bytes.NewReader(c.Files["state.json"]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Version != signed.Keyset.Version || state.Issued != signed.Keyset.Issued || len(state.Keys) != 3 {
		return fmt.Errorf("activation state mismatch")
	}
	for i, key := range signed.Keyset.Keys {
		expected := map[string]any{"kid": key["kid"], "realm": c.Realm, "purpose": key["purpose"], "state": "ACTIVE", "not_before": key["not_before"], "not_after": key["not_after"], "public_key_spki": key["public_key_spki"]}
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(state.Keys[i])
		if !bytes.Equal(a, b) {
			return fmt.Errorf("activation state mismatch")
		}
	}
	return nil
}
