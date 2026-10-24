package ids_test

import (
	"strings"
	"testing"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/ids"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func TestIDsArePrefixedUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		id := ids.UUID{}.NewID("pln")
		if !strings.HasPrefix(id, "pln-") || len(id) != 40 || seen[id] || !shared.ValidID(id) {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
}
