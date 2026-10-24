// Package sscc issues SSCCs from the network's own GS1 company prefix (policy
// SsccIssuance, ADR 0002 A5). An SSCC is: extension digit, the company prefix,
// a serial reference that pads the 16 digits after the extension digit, and the
// mod-10 check digit. The serial comes from a Sequence (a Postgres sequence in
// production), so two instances never issue the same number.
package sscc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

const (
	// MinPrefixLen and MaxPrefixLen bound the GS1 company prefix a deployment may
	// configure: shorter prefixes leave no room for a useful serial range, longer
	// ones leave fewer than five serial digits.
	MinPrefixLen = 6
	MaxPrefixLen = 11
	// DefaultExtensionDigit is the extension digit used when none is configured.
	DefaultExtensionDigit = 0
	bodyDigits            = 16 // digits between the extension digit and the check digit
)

// ErrInvalidConfig marks a company prefix or extension digit that cannot form SSCCs.
var ErrInvalidConfig = errors.New("invalid SSCC configuration")

// ErrSerialExhausted is returned when the sequence passes the serial range of the prefix.
var ErrSerialExhausted = errors.New("sscc serial range exhausted")

// Sequence hands out strictly increasing serial references, starting at 1.
type Sequence interface {
	Next(ctx context.Context) (int64, error)
}

// Issuer implements ports.SsccIssuer.
type Issuer struct {
	extension int
	prefix    string
	width     int
	seq       Sequence
}

var _ ports.SsccIssuer = (*Issuer)(nil)

// NewIssuer validates the configuration: prefix is MinPrefixLen to MaxPrefixLen
// ASCII digits, extension is a single digit 0 to 9.
func NewIssuer(prefix string, extension int, seq Sequence) (*Issuer, error) {
	if l := len(prefix); l < MinPrefixLen || l > MaxPrefixLen {
		return nil, fmt.Errorf("%w: SSCC_COMPANY_PREFIX must be %d to %d digits", ErrInvalidConfig, MinPrefixLen, MaxPrefixLen)
	}
	if strings.Trim(prefix, "0123456789") != "" {
		return nil, fmt.Errorf("%w: SSCC_COMPANY_PREFIX must contain digits only", ErrInvalidConfig)
	}
	if extension < 0 || extension > 9 {
		return nil, fmt.Errorf("%w: SSCC_EXTENSION_DIGIT must be 0 to 9", ErrInvalidConfig)
	}
	return &Issuer{extension: extension, prefix: prefix, width: bodyDigits - len(prefix), seq: seq}, nil
}

// Next issues the next SSCC.
func (i *Issuer) Next(ctx context.Context) (string, error) {
	n, err := i.seq.Next(ctx)
	if err != nil {
		return "", err
	}
	serial := strconv.FormatInt(n, 10)
	if n < 1 || len(serial) > i.width {
		return "", fmt.Errorf("%w: serial %d does not fit %d digits", ErrSerialExhausted, n, i.width)
	}
	body := strconv.Itoa(i.extension) + i.prefix + strings.Repeat("0", i.width-len(serial)) + serial
	s, err := gs1.NewSSCC(body)
	if err != nil {
		return "", err
	}
	return s.String(), nil
}
