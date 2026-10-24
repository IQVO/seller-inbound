// Package ids holds the production ports.IDGenerator.
package ids

import "github.com/google/uuid"

// UUID mints aggregate ids as prefix + "-" + a random UUID v4.
type UUID struct{}

// NewID returns a fresh id such as "pln-3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40".
func (UUID) NewID(prefix string) string { return prefix + "-" + uuid.NewString() }
