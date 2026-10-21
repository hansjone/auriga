package storage

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// InstanceIDPattern: letter/digit start, then letter/digit/_/- , max 63 chars.
// Rejects ".", "..", empty, path separators, and Windows-reserved characters.
var InstanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

// ValidateInstanceID checks the id before any path join.
func ValidateInstanceID(id string) error {
	if id == "" {
		return fmt.Errorf("storage: empty instance id")
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("storage: instance id has leading/trailing space")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("storage: instance id reserved")
	}
	if strings.ContainsAny(id, `/\`+"\x00:") {
		return fmt.Errorf("storage: instance id contains forbidden character")
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return fmt.Errorf("storage: instance id contains control character")
		}
	}
	if !InstanceIDPattern.MatchString(id) {
		return fmt.Errorf("storage: instance id %q rejected by pattern", id)
	}
	if isWindowsDeviceName(id) {
		return fmt.Errorf("storage: instance id %q is a reserved device name", id)
	}
	return nil
}

// isWindowsDeviceName rejects CON, PRN, AUX, NUL, COM1-9, LPT1-9 (any case):
// on Windows they open a device instead of a directory.
func isWindowsDeviceName(id string) bool {
	u := strings.ToUpper(id)
	switch u {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(u) == 4 && (strings.HasPrefix(u, "COM") || strings.HasPrefix(u, "LPT")) && u[3] >= '1' && u[3] <= '9' {
		return true
	}
	return false
}

// AssertUnderRoot ensures absPath is strictly under root (after Clean).
func AssertUnderRoot(root, absPath string) error {
	root = filepath.Clean(root)
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return fmt.Errorf("storage: path escape: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("storage: path escapes instances dir")
	}
	return nil
}
