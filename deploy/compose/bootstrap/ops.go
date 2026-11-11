package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	"os"
	"path/filepath"
)

func writeComposeOpsKey(out string) error {
	p, err := materials.Open(out)
	if err != nil {
		return err
	}
	defer p.Close()
	raw, err := p.Read("ops.key")
	if os.IsNotExist(err) {
		env, readErr := os.ReadFile(filepath.Join(out, "compose.env"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return fmt.Errorf("read compose environment")
		}
		if bytes.Contains(env, []byte("AURIGA_OPS_NOTIFICATION_KEY=")) {
			return fmt.Errorf("ops key missing; restore installation secret backup")
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return fmt.Errorf("ops key generation failed")
		}
		raw = []byte(base64.StdEncoding.EncodeToString(key))
		clear(key)
		if err = p.Create("ops.key", raw); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	defer clear(raw)
	decoded, err := base64.StdEncoding.Strict().DecodeString(string(raw))
	defer clear(decoded)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid ops notification key")
	}
	return p.Validate()
}
