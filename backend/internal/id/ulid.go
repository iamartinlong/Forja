package id

import (
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// New returns a new UUIDv7 string (36 chars, time-ordered, RFC 9562).
// Uses google/uuid.NewV7 (crypto/rand + time). Panics only on entropy failure.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id.String()
}

// IsValid reports whether s is a valid UUID (36) or ULID (26) —
// keeps backward compatibility for old ULID rows (019) and legacy UUIDv4.
func IsValid(s string) bool {
	if len(s) == 26 {
		_, err := ulid.Parse(s)
		return err == nil
	}
	if len(s) == 36 {
		_, err := uuid.Parse(s)
		return err == nil
	}
	return false
}
