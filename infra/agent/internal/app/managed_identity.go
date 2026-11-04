package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The receipt-checked static installation is projected as one read-only file.
// It contains public issuance/network metadata, never a device credential.
func managedIdentityHost(path, allowed string) (string, error) {
	if path == "" {
		return "", nil
	}
	invalid := fmt.Errorf("managed identity installation is unavailable or invalid")
	if !filepath.IsAbs(path) {
		return "", invalid
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 65536 {
		return "", invalid
	}
	file, err := os.Open(path)
	if err != nil {
		return "", invalid
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", invalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return "", invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", invalid
	}
	fields := make(map[string]json.RawMessage, 3)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] != nil || (name != "platformOrigin" && name != "issuer" && name != "network") {
			return "", invalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return "", invalid
		}
		fields[name] = value
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return "", invalid
	}
	var origin, issuer string
	if len(fields) != 3 || json.Unmarshal(fields["platformOrigin"], &origin) != nil || json.Unmarshal(fields["issuer"], &issuer) != nil || origin == "" || origin != issuer || !bytes.HasPrefix(bytes.TrimSpace(fields["network"]), []byte{'{'}) {
		return "", invalid
	}
	host, err := managedOriginHost(origin, allowed)
	if err != nil {
		return "", invalid
	}
	return host, nil
}
