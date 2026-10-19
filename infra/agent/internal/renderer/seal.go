package renderer

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

const sealedAlg = "hpke-x25519-sha256-aes256gcm"

// Unseal decrypts a SealedSecret with the node's X25519 sealing private key.
// info = auriga/sealed/v1/<instance_id>/<secret_name>, aad = generation decimal.
func Unseal(priv [32]byte, instanceID, secretName string, generation int64, s *nodev1.SealedSecret) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("renderer: nil sealed secret")
	}
	if s.Alg != "" && s.Alg != sealedAlg {
		return nil, fmt.Errorf("renderer: unsupported seal alg %q", s.Alg)
	}
	curve := ecdh.X25519()
	ecdhPriv, err := curve.NewPrivateKey(priv[:])
	if err != nil {
		return nil, fmt.Errorf("renderer: sealing key: %w", err)
	}
	hpkePriv, err := hpke.NewDHKEMPrivateKey(ecdhPriv)
	if err != nil {
		return nil, err
	}
	info := []byte(fmt.Sprintf("auriga/sealed/v1/%s/%s", instanceID, secretName))
	recipient, err := hpke.NewRecipient(s.Enc, hpkePriv, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
	if err != nil {
		return nil, fmt.Errorf("renderer: hpke recipient: %w", err)
	}
	aad := []byte(strconv.FormatInt(generation, 10))
	plain, err := recipient.Open(aad, s.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("renderer: unseal %s: %w", secretName, err)
	}
	return plain, nil
}

// SealForTest encrypts plaintext for tests (Base mode HPKE).
func SealForTest(pub [32]byte, instanceID, secretName string, generation int64, plaintext []byte) (*nodev1.SealedSecret, error) {
	curve := ecdh.X25519()
	ecdhPub, err := curve.NewPublicKey(pub[:])
	if err != nil {
		return nil, err
	}
	hpkePub, err := hpke.NewDHKEMPublicKey(ecdhPub)
	if err != nil {
		return nil, err
	}
	info := []byte(fmt.Sprintf("auriga/sealed/v1/%s/%s", instanceID, secretName))
	enc, sender, err := hpke.NewSender(hpkePub, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
	if err != nil {
		return nil, err
	}
	aad := []byte(strconv.FormatInt(generation, 10))
	ct, err := sender.Seal(aad, plaintext)
	if err != nil {
		return nil, err
	}
	sum := sha256First8(pub[:])
	return &nodev1.SealedSecret{
		Alg:        sealedAlg,
		KeyId:      sum,
		Enc:        enc,
		Ciphertext: ct,
	}, nil
}

func sha256First8(pub []byte) string {
	// identity.SealingKeyID uses first 8 bytes of SHA-256 as 16 hex chars.
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}
