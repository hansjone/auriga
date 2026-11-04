// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"fmt"
	"net/url"

	"github.com/maximhq/bifrost/core/schemas"
)

func EscapeResourceID(id, field string) (string, *schemas.BifrostError) {
	if err := validateOpaquePathSegment(id); err != nil {
		return "", NewBifrostBadRequestError(fmt.Sprintf("invalid %s: %v", field, err))
	}
	return url.PathEscape(id), nil
}

func validateOpaquePathSegment(id string) error {
	if id == "" {
		return fmt.Errorf("must not be empty")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("dot segments are not allowed")
	}
	for _, r := range id {
		switch r {
		case '/', '\\', '?', '#', '%':
			return fmt.Errorf("path delimiters and percent-encoding are not allowed")
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	return nil
}
