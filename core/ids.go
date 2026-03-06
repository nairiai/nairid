package core

import (
	"crypto/rand"
	"nairid/utils"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewID generates a new ULID with the given prefix.
// The format is: prefix_ULID
// Example: core.NewID("u") returns "u_01G0EZ1XTM37C5X11SQTDNCTM1"
func NewID(prefix string) string {
	utils.AssertInvariant(prefix != "" && strings.TrimSpace(prefix) != "", "prefix cannot be empty")

	// Generate a new ULID with current timestamp and crypto/rand entropy
	entropy := ulid.Monotonic(rand.Reader, 0)
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)

	// Return formatted ID with lowercase prefix
	return strings.ToLower(strings.TrimSpace(prefix)) + "_" + id.String()
}

// IsValidULID checks if the given string is a valid ULID format with prefix.
// The format should be: prefix_ULID where ULID is 26 characters, base32 encoded.
// Returns true if valid, false otherwise.
func IsValidULID(id string) bool {
	if id == "" {
		return false
	}

	// Find the underscore separator
	parts := strings.Split(id, "_")
	if len(parts) != 2 {
		return false
	}

	prefix := parts[0]
	ulidPart := parts[1]

	// Validate prefix: should be non-empty, lowercase alphanumeric
	if prefix == "" {
		return false
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}

	// Validate ULID part: should be exactly 26 characters, valid base32
	if len(ulidPart) != 26 {
		return false
	}

	// Validate ULID characters: should be uppercase base32 (0-9, A-Z excluding I, L, O, U)
	for _, r := range ulidPart {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z' || r == 'I' || r == 'L' || r == 'O' || r == 'U') {
			return false
		}
	}

	// Try to parse as ULID to validate format
	_, err := ulid.Parse(ulidPart)
	return err == nil
}
