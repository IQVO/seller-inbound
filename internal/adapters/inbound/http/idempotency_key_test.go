package http

import (
	"strings"
	"testing"
)

func TestValidIdempotencyKey(t *testing.T) {
	for key, want := range map[string]bool{
		"a":                                    true,
		"3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40": true,
		strings.Repeat("k", 128):               true,
		strings.Repeat("k", 129):               false,
		"has space ~!":                         true,
		"tab\there":                            false,
		"café":                                 false,
		"del\x7f":                              false,
	} {
		if got := validIdempotencyKey(key); got != want {
			t.Errorf("validIdempotencyKey(%q) = %v, want %v", key, got, want)
		}
	}
}
