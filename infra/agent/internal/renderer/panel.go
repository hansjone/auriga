package renderer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const ContainerPanelKeys = ContainerMetaRoot + "/panel-keys.json"

// The path is installation-owned agent configuration, never an InstanceSpec or
// identity key. Only public Ed25519 keys may enter the instance material mount.
func panelKeys() ([]byte, error) {
	path := os.Getenv("AURIGA_AGENT_PANEL_KEYS_FILE")
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("renderer: read installation panel keys: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("renderer: invalid panel key file")
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			Nbf *int64 `json:"nbf,omitempty"`
			Exp *int64 `json:"exp,omitempty"`
		} `json:"keys"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&set) != nil || len(set.Keys) == 0 {
		return nil, fmt.Errorf("renderer: invalid public panel keys")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("renderer: invalid public panel keys")
	}
	seen := map[string]bool{}
	for _, k := range set.Keys {
		pub, err := base64.RawURLEncoding.Strict().DecodeString(k.X)
		if err != nil || len(pub) != 32 || k.Kid == "" || seen[k.Kid] || k.Kty != "OKP" || k.Crv != "Ed25519" || k.Alg != "EdDSA" || k.Use != "" && k.Use != "sig" {
			return nil, fmt.Errorf("renderer: invalid public panel keys")
		}
		seen[k.Kid] = true
	}
	return raw, nil
}
