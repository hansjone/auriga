package ingress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ValidateRelPath rejects escape / NUL / absolute / empty-component tricks.
// Returns a cleaned slash-separated relative path under the instance root.
func ValidateRelPath(rel string) (string, error) {
	if rel == "" || rel == "." {
		return ".", nil
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("%s: NUL in path", CodePathInvalid)
	}
	if !utf8.ValidString(rel) {
		return "", fmt.Errorf("%s: invalid utf-8", CodePathInvalid)
	}
	// Reject Windows volume / UNC and Unix absolute.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, `\`) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%s: absolute path", CodePathEscape)
	}
	if len(rel) >= 2 && rel[1] == ':' {
		return "", fmt.Errorf("%s: drive letter", CodePathEscape)
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s: parent reference", CodePathEscape)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s: parent reference", CodePathEscape)
		}
		if part == "" {
			return "", fmt.Errorf("%s: empty segment", CodePathInvalid)
		}
	}
	return cleaned, nil
}

// ResolveUnderRoot joins root + rel and ensures the result stays under root
// after EvalSymlinks on existing ancestors. For the final missing leaf, only
// the parent is checked. Callers that open files should prefer OpenBeneath.
func ResolveUnderRoot(root, rel string) (string, error) {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%s: root abs: %w", CodePathInvalid, err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("%s: root resolve: %w", CodePathInvalid, err)
	}
	// Walk existing prefix and reject out-of-root symlinks/junctions.
	cur := absRoot
	parts := strings.Split(clean, "/")
	if clean == "." {
		parts = nil
	}
	for i, p := range parts {
		next := filepath.Join(cur, p)
		fi, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				// Remainder must not exist yet; join literally under cur.
				rest := filepath.Join(parts[i:]...)
				candidate := filepath.Join(cur, rest)
				if !underRoot(absRoot, candidate) {
					return "", fmt.Errorf("%s: outside root", CodePathEscape)
				}
				return candidate, nil
			}
			return "", fmt.Errorf("%s: lstat: %w", CodePathInvalid, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("%s: symlink: %w", CodePathEscape, err)
			}
			if !underRoot(absRoot, resolved) {
				return "", fmt.Errorf("%s: symlink escapes root", CodePathEscape)
			}
			cur = resolved
			continue
		}
		cur = next
	}
	if !underRoot(absRoot, cur) {
		return "", fmt.Errorf("%s: outside root", CodePathEscape)
	}
	return cur, nil
}

func underRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// IdentityExecutor runs file ops as the instance account (setfsuid / impersonation).
// Linux/Windows concrete impls land with storage; A3 keeps the interface only.
type IdentityExecutor interface {
	// Run invokes fn while credentials match the instance account.
	Run(instanceID string, fn func() error) error
}

// DirectExecutor runs as the current process (tests / until storage lands).
type DirectExecutor struct{}

func (DirectExecutor) Run(_ string, fn func() error) error { return fn() }
