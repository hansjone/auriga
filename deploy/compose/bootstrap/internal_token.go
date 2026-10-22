package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
)

const iamRPCPort = 9091

// Only the platform receives this private directory. No raw secret goes into JSON or env.
// Preserve an existing installation on a repeated bootstrap instead of rotating its credentials.
func writeInternalTokenInstallation(out string) error {
	installation, err := openPrivateInstallation(out)
	if err != nil {
		return err
	}
	defer installation.close()
	secret, err := installation.read("edge.secret")
	defer func() { clear(secret) }()
	if os.IsNotExist(err) {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate IAM installation credential")
		}
		secret = []byte(base64.RawURLEncoding.EncodeToString(raw))
		clear(raw)
		if err := installation.create("edge.secret", secret); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read IAM installation credential")
	}
	decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(string(secret))
	defer clear(decoded)
	if len(secret) != 43 || decodeErr != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid IAM installation credential")
	}
	hash := sha256.Sum256(secret)
	properties := []byte("auriga.iam.internal-secret-hashes.edge=" + hex.EncodeToString(hash[:]) + "\nauriga.iam.edge-token-export.installation-owner=" + fmt.Sprint(installation.owner()) + "\n")
	existing, err := installation.read("internal.properties")
	defer clear(existing)
	if os.IsNotExist(err) {
		if err := installation.create("internal.properties", properties); err != nil {
			return err
		}
	} else if err != nil || !bytes.Equal(existing, properties) {
		return fmt.Errorf("invalid IAM installation hash configuration")
	}
	return installation.validate()
}
