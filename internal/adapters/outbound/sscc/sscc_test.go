package sscc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/sscc"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

type counter struct{ n int64 }

func (c *counter) Next(context.Context) (int64, error) { c.n++; return c.n, nil }

type failing struct{}

func (failing) Next(context.Context) (int64, error) { return 0, errors.New("db down") }

func TestIssuesValidSequentialSSCCs(t *testing.T) {
	is, err := sscc.NewIssuer("0614141", 0, &counter{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := is.Next(context.Background())
	if err != nil || first != "006141410000000012" {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, _ := is.Next(context.Background())
	if second == first || second[:16] != "0061414100000000" {
		t.Fatalf("second = %q", second)
	}
	for _, s := range []string{first, second} {
		if _, err := gs1.ParseSSCC(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestExtensionDigitAndPrefixLengths(t *testing.T) {
	is, err := sscc.NewIssuer("061414100", 3, &counter{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := is.Next(context.Background())
	if s[0] != '3' || s[1:10] != "061414100" || len(s) != 18 {
		t.Fatalf("%s", s)
	}
	for _, prefix := range []string{"061414", "06141410000"} {
		if _, err := sscc.NewIssuer(prefix, 0, &counter{}); err != nil {
			t.Errorf("prefix %q must be accepted: %v", prefix, err)
		}
	}
}

func TestRejectsBadConfiguration(t *testing.T) {
	cases := []struct {
		prefix string
		ext    int
	}{{"06141", 0}, {"061414100000", 0}, {"06141x1", 0}, {"", 0}, {"0614141", -1}, {"0614141", 10}}
	for _, c := range cases {
		if _, err := sscc.NewIssuer(c.prefix, c.ext, &counter{}); !errors.Is(err, sscc.ErrInvalidConfig) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestSerialRange(t *testing.T) {
	is, _ := sscc.NewIssuer("06141410000", 0, &counter{n: 99998}) // 5 serial digits
	if _, err := is.Next(context.Background()); err != nil {
		t.Fatalf("99999 fits: %v", err)
	}
	if _, err := is.Next(context.Background()); !errors.Is(err, sscc.ErrSerialExhausted) {
		t.Fatalf("100000 does not: %v", err)
	}
	zero, _ := sscc.NewIssuer("0614141", 0, &counter{n: -1})
	if _, err := zero.Next(context.Background()); !errors.Is(err, sscc.ErrSerialExhausted) {
		t.Fatalf("serial 0 is refused: %v", err)
	}
	broken, _ := sscc.NewIssuer("0614141", 0, failing{})
	if _, err := broken.Next(context.Background()); err == nil {
		t.Fatal("sequence error must surface")
	}
}
