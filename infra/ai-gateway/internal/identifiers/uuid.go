package identifiers

import "github.com/google/uuid"

// ValidUUID accepts canonical lowercase, non-nil UUIDs, including installed
// tenant identifiers whose variant predates generated RFC 9562 UUIDs.
func ValidUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}
