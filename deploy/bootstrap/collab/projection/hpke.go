package projection

import (
	"crypto/ecdh"
	"crypto/hpke"
	"encoding/json"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// OpenSealed is deliberately limited to the frozen RFC9180 suite and info.
// The caller must verify the public root chain and bundle digest before calling
// it, and must bind the decrypted scope to its independently installed pin.
func OpenSealed(s api.SealedMaterial, key *ecdh.PrivateKey) ([]byte, error) {
	if key == nil || key.Curve() != ecdh.X25519() {
		return nil, Fault("invalid_chain")
	}
	raw, e := Bytes(s)
	if e != nil {
		return nil, e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, e
	}
	delete(m, "enc")
	delete(m, "ciphertext")
	aad, e := Bytes(m)
	if e != nil {
		return nil, e
	}
	if s.Kem != "DHKEM(X25519,HKDF-SHA256)" || s.Kdf != "HKDF-SHA256" || s.Aead != "AES-256-GCM" {
		return nil, Fault("invalid_chain")
	}
	enc, e := binaryText(s.Enc)
	if e != nil {
		return nil, e
	}
	ciphertext, e := binaryText(s.Ciphertext)
	if e != nil {
		return nil, e
	}
	private, e := hpke.NewDHKEMPrivateKey(key)
	if e != nil {
		return nil, Fault("invalid_chain")
	}
	r, e := hpke.NewRecipient(enc, private, hpke.HKDFSHA256(), hpke.AES256GCM(), []byte("Auriga.NetworkSealedMaterial.v2"))
	if e != nil {
		return nil, Fault("invalid_chain")
	}
	cleartext, e := r.Open(aad, ciphertext)
	if e != nil {
		return nil, Fault("invalid_chain")
	}
	return cleartext, nil
}
