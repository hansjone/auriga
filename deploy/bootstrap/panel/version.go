package panel

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func verifiedManifest(raw []byte, pub ed25519.PublicKey) (manifest, error) {
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil {
		return m, fmt.Errorf("invalid previous panel manifest")
	}
	encoded, err := json.Marshal(m)
	if err != nil || !bytes.Equal(raw, encoded) {
		return m, fmt.Errorf("changed previous panel manifest")
	}
	signed, marshalErr := json.Marshal(m.body)
	sig, sigErr := base64.RawURLEncoding.DecodeString(m.Signature.Value)
	if marshalErr != nil || sigErr != nil || m.Signature.Alg != "EdDSA" || m.Signature.Kid != keyID(pub) || !ed25519.Verify(pub, signed, sig) {
		return m, fmt.Errorf("previous panel manifest signature invalid")
	}
	return m, nil
}

// Versions belong to an installation: a new digest gets the next patch, while
// a previously published digest can only renew its current version. The ledger
// consists entirely of signed manifests and is never served by edge.
func automaticVersion(out string, current manifest, entry string, pub ed25519.PublicKey) (string, error) {
	archive := filepath.Join(out, ".panel-history")
	if info, err := os.Lstat(archive); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("invalid panel history directory")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	files, err := os.ReadDir(archive)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			return "", fmt.Errorf("invalid panel history file")
		}
		raw, err := readRegular(filepath.Join(archive, file.Name()), 256<<10)
		if err != nil {
			return "", err
		}
		old, err := verifiedManifest(raw, pub)
		if err != nil {
			return "", err
		}
		if file.Name() != old.Version+".json" || current.Version == "" {
			return "", fmt.Errorf("panel history/current mismatch")
		}
		if old.Version == current.Version && old.Entry != current.Entry {
			return "", fmt.Errorf("panel history same version changed bytes")
		}
		parts, err := versionParts(old.Version)
		if err != nil {
			return "", err
		}
		latest, err := versionParts(current.Version)
		if err != nil {
			return "", err
		}
		for i := range parts {
			if parts[i] > latest[i] {
				return "", fmt.Errorf("panel manifest rollback behind history")
			}
			if parts[i] < latest[i] {
				break
			}
		}
		if old.Entry == entry && current.Entry != entry {
			return "", fmt.Errorf("panel content rollback")
		}
	}
	if current.Version == "" {
		return "0.0.1", nil
	}
	if current.Entry == entry {
		return current.Version, nil
	}
	parts, err := versionParts(current.Version)
	if err != nil {
		return "", err
	}
	if parts[2] == 1<<53-1 {
		return "", fmt.Errorf("panel version exhausted")
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2]+1), nil
}

func archiveManifest(out, version string, raw []byte) error {
	dir := filepath.Join(out, ".panel-history")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid panel history directory")
	}
	path := filepath.Join(dir, version+".json")
	existing, err := readRegular(path, 256<<10)
	if err == nil {
		// The same version may have been renewed before an interrupted upgrade.
		var previous, next manifest
		if json.Unmarshal(existing, &previous) != nil || json.Unmarshal(raw, &next) != nil || previous.Entry != next.Entry || previous.Version != next.Version {
			return fmt.Errorf("panel history conflict")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return writePublic(path, raw)
}
