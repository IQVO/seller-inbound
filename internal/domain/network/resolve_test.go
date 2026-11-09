package network

import (
	"testing"
	"time"
)

const (
	gEach  = "00614141000012"
	gInner = "00614141000043"
	gCase  = "00614141000036"
	gOther = "00614141000050"
)

func level(t *testing.T, l Level, gtin, child string, units int) PackLevel {
	t.Helper()
	p, err := Declare(nil, "item-1", l, gtin, child, units, t0)
	if err != nil {
		t.Fatalf("declare %s: %v", gtin, err)
	}
	return p
}

// TestResolveEachWalksTheChildChainToEach: CASE -> INNER -> EACH resolves to
// the EACH GTIN, and an EACH GTIN resolves to itself.
func TestResolveEachWalksTheChildChainToEach(t *testing.T) {
	rows := []PackLevel{level(t, Case, gCase, gInner, 12), level(t, Inner, gInner, gEach, 4), level(t, Each, gEach, "", 1)}
	if each, known := ResolveEach(rows, gCase); each != gEach || !known {
		t.Fatalf("case = %q %v", each, known)
	}
	if each, known := ResolveEach(rows, gEach); each != gEach || !known {
		t.Fatalf("each = %q %v", each, known)
	}
}

// TestResolveEachKnownButUnresolved: the view holds the GTIN but its chain
// never reaches an EACH row.
func TestResolveEachKnownButUnresolved(t *testing.T) {
	cases := map[string][]PackLevel{
		"no child":            {level(t, Case, gCase, "", 12)},
		"child not declared":  {level(t, Case, gCase, gEach, 12)},
		"child is not a gtin": {{CatalogItemID: "item-1", Level: Case, GTIN: gCase, ChildGTIN: "12", UnitsPerPack: 12, Active: true}},
		"cycle": {
			{CatalogItemID: "item-1", Level: Case, GTIN: gCase, ChildGTIN: gInner, UnitsPerPack: 12, Active: true},
			{CatalogItemID: "item-1", Level: Inner, GTIN: gInner, ChildGTIN: gCase, UnitsPerPack: 4, Active: true},
		},
	}
	for name, rows := range cases {
		if each, known := ResolveEach(rows, gCase); each != "" || !known {
			t.Errorf("%s: = %q %v", name, each, known)
		}
	}
}

// TestResolveEachUnknownGtin: a GTIN the view does not hold is not known.
func TestResolveEachUnknownGtin(t *testing.T) {
	rows := []PackLevel{level(t, Case, gCase, gEach, 12), level(t, Each, gEach, "", 1)}
	if each, known := ResolveEach(rows, gOther); each != "" || known {
		t.Fatalf("= %q %v", each, known)
	}
	if each, known := ResolveEach(nil, gCase); each != "" || known {
		t.Fatalf("no rows = %q %v", each, known)
	}
}

// TestResolveEachRetiredRowsResolveAndActiveWins: a retired level still
// identifies its GTIN, and an active row of the same GTIN is preferred.
func TestResolveEachRetiredRowsResolveAndActiveWins(t *testing.T) {
	retired, err := Retire(nil, "item-1", Case, gCase, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	retired.ChildGTIN = gOther
	rows := []PackLevel{retired, level(t, Case, gCase, gEach, 12), level(t, Each, gEach, "", 1)}
	if each, known := ResolveEach(rows, gCase); each != gEach || !known {
		t.Fatalf("active wins = %q %v", each, known)
	}
	old := level(t, Each, gEach, "", 1)
	old.Active = false
	if each, known := ResolveEach([]PackLevel{old}, gEach); each != gEach || !known {
		t.Fatalf("retired each = %q %v", each, known)
	}
}
