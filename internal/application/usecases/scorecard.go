package usecases

import (
	"context"

	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
)

// GetSellerScorecard answers the informational compliance scorecard of a seller
// over a rolling window ("7d", "30d"; empty is 30d), computed from the facts
// this service recorded (ADR 0005). It is a pure read: it raises no event and
// changes no aggregate, and the tier it derives never suspends or blocks the
// seller. A window other than 7d or 30d is scorecard.ErrInvalidWindow; an
// unknown seller is ports.ErrNotFound (a suspended seller has a scorecard too).
func (s *Service) GetSellerScorecard(ctx context.Context, sellerID, window string) (views.SellerScorecard, error) {
	w, err := scorecard.ParseWindow(window)
	if err != nil {
		return views.SellerScorecard{}, err
	}
	if _, err := s.d.Sellers.Get(ctx, seller.ID(sellerID)); err != nil {
		return views.SellerScorecard{}, err
	}
	now := s.d.Clock.Now().UTC()
	counts, err := s.d.Scorecard.Counts(ctx, sellerID, w.Since(now), now)
	if err != nil {
		return views.SellerScorecard{}, err
	}
	c := scorecard.Compute(w, now, counts)
	return views.SellerScorecard{
		SellerID: sellerID, Window: string(c.Window), WindowStart: c.Since, WindowEnd: c.Until,
		ShipmentsReceived: c.ShipmentsReceived, CartonsReported: c.CartonsReported, CartonsReceived: c.CartonsReceived,
		ReceiptAccuracyRate: c.ReceiptAccuracyRate, DiscrepancyRatePerShipment: c.DiscrepancyRatePerShipment,
		DiscrepanciesByKind: c.DiscrepanciesByKind, ComplianceViolations: c.ComplianceViolations,
		ComplianceTier: string(c.Tier),
	}, nil
}
