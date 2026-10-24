package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func sellerView(a *seller.Account) views.Seller {
	s := a.Snapshot()
	return views.Seller{SellerID: string(s.ID), Name: s.Name, State: string(s.State), OnboardedAt: s.OnboardedAt,
		SuspendedAt: s.SuspendedAt, SuspendReason: s.SuspendReason}
}

// OnboardSeller creates an ACTIVE seller account and publishes SellerOnboarded.
// A sellerId that is already onboarded is seller.ErrSellerExists.
func (s *Service) OnboardSeller(ctx context.Context, in requests.OnboardSeller) (views.Seller, error) {
	var out views.Seller
	err := s.atomically(ctx, func(ctx context.Context) error {
		_, err := s.d.Sellers.Get(ctx, seller.ID(in.SellerID))
		if err == nil {
			return seller.ErrSellerExists
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		a, evs, err := seller.Onboard(seller.NewAccount{ID: seller.ID(in.SellerID), Name: in.Name}, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.saveSeller(ctx, a, evs); err != nil {
			return err
		}
		out = sellerView(a)
		return nil
	})
	return out, err
}

func (s *Service) saveSeller(ctx context.Context, a *seller.Account, evs []shared.Event) error {
	if err := s.d.Sellers.Save(ctx, a); err != nil {
		return err
	}
	return s.d.Outbox.Append(ctx, evs...)
}

// SuspendSeller moves the seller to SUSPENDED and publishes SellerSuspended.
// Suspending a suspended seller is a no-op.
func (s *Service) SuspendSeller(ctx context.Context, in requests.SuspendSeller) (views.Seller, error) {
	return s.changeSeller(ctx, in.SellerID, func(a *seller.Account) ([]shared.Event, error) {
		return a.Suspend(in.Reason, s.d.Clock.Now())
	})
}

// ReinstateSeller moves the seller back to ACTIVE and publishes SellerReinstated.
func (s *Service) ReinstateSeller(ctx context.Context, sellerID string) (views.Seller, error) {
	return s.changeSeller(ctx, sellerID, func(a *seller.Account) ([]shared.Event, error) {
		return a.Reinstate(s.d.Clock.Now())
	})
}

func (s *Service) changeSeller(ctx context.Context, sellerID string, cmd func(*seller.Account) ([]shared.Event, error)) (views.Seller, error) {
	var out views.Seller
	err := s.atomically(ctx, func(ctx context.Context) error {
		a, err := s.d.Sellers.Get(ctx, seller.ID(sellerID))
		if err != nil {
			return err
		}
		evs, err := cmd(a)
		if err != nil {
			return err
		}
		if len(evs) > 0 {
			if err := s.saveSeller(ctx, a, evs); err != nil {
				return err
			}
		}
		out = sellerView(a)
		return nil
	})
	return out, err
}

// requireActiveSeller loads the seller and refuses a suspended one. An unknown
// seller is ports.ErrNotFound.
func (s *Service) requireActiveSeller(ctx context.Context, sellerID string) error {
	a, err := s.d.Sellers.Get(ctx, seller.ID(sellerID))
	if err != nil {
		return err
	}
	return a.EnsureActive()
}
