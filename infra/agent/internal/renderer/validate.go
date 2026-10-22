package renderer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	safeIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,127}$`)
	// HTTP is allowed: IP-only / HTTP-only deployments must work (conventions).
	safeURLRe   = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+$`)
	patchPathRe = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,128}$`)
)

// rejectUnsafeString bans newlines, NUL, and YAML tag injection markers.
func rejectUnsafeString(field, v string) error {
	if strings.ContainsAny(v, "\n\r\x00") {
		return fmt.Errorf("renderer: %s contains newline or NUL", field)
	}
	if strings.Contains(v, "!!") {
		return fmt.Errorf("renderer: %s contains YAML tag marker", field)
	}
	for _, r := range v {
		if unicode.IsControl(r) && r != '\t' {
			return fmt.Errorf("renderer: %s contains control character", field)
		}
	}
	return nil
}

func validateEnvKey(k string) error {
	if err := rejectUnsafeString("env key", k); err != nil {
		return err
	}
	if !envKeyRe.MatchString(k) {
		return fmt.Errorf("renderer: invalid env key %q", k)
	}
	upper := strings.ToUpper(k)
	if isReservedEnvKey(upper) {
		return fmt.Errorf("renderer: reserved env key %q", k)
	}
	return nil
}

func isReservedEnvKey(upper string) bool {
	switch upper {
	case "DSH_HOME", "HOME", "PATH", "OPENSSL_CONF",
		"NODE_OPTIONS", "NODE_PATH", "NODE_EXTRA_CA_CERTS",
		"SSL_CERT_FILE", "SSL_CERT_DIR":
		return true
	}
	if strings.HasPrefix(upper, "AURIGA_") ||
		strings.HasPrefix(upper, "PYTHON") ||
		strings.HasPrefix(upper, "LD_") ||
		strings.HasSuffix(upper, "_PROXY") {
		return true
	}
	return false
}

func validateIdentity(id, field string) error {
	if err := rejectUnsafeString(field, id); err != nil {
		return err
	}
	if id == "" || !safeIDRe.MatchString(id) {
		return fmt.Errorf("renderer: invalid %s", field)
	}
	return nil
}

func validatePatch(path, valueJSON string) error {
	if err := rejectUnsafeString("patch.path", path); err != nil {
		return err
	}
	if err := rejectUnsafeString("patch.value", valueJSON); err != nil {
		return err
	}
	if !patchPathRe.MatchString(path) {
		return fmt.Errorf("renderer: invalid patch path")
	}
	return nil
}
