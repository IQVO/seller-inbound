package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type env struct {
	svc   *usecases.Service
	store *memory.Store
	clock *memory.Clock
	sscc  *memory.SsccIssuer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	issuer := &memory.SsccIssuer{}
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Sellers: store.Sellers(), Plans: store.Plans(), Shipments: store.Shipments(), Nodes: store.Nodes(),
		Packaging: store.Packaging(), Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: clock, IDs: &memory.SequentialIDs{}, SSCC: issuer,
	}, usecases.Config{})
	return &env{svc: svc, store: store, clock: clock, sscc: issuer}
}

func gtin(t *testing.T, payload string) string {
	t.Helper()
	g, err := gs1.NewGTIN(payload)
	if err != nil {
		t.Fatal(err)
	}
	return g.String()
}

func sscc(t *testing.T, serial string) string {
	t.Helper()
	s, err := gs1.NewSSCC("0061414100000" + serial)
	if err != nil {
		t.Fatal(err)
	}
	return s.String()
}

// seed onboards seller S-1, activates a fulfillment centre n-1 and declares a CASE level of 12 eaches for item-1.
func (e *env) seed(t *testing.T) (caseGTIN, eachGTIN string) {
	t.Helper()
	ctx := context.Background()
	caseGTIN, eachGTIN = gtin(t, "0614141000036"), gtin(t, "0614141000012")
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Acme Goods"}); err != nil {
		t.Fatal(err)
	}
	e.node(t, "n-1", "FULFILLMENT_CENTER", true)
	if err := e.svc.ApplyPackagingDeclared(ctx, "ev-pk-1", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: caseGTIN,
		ChildGTIN: eachGTIN, UnitsPerPack: 12, DeclaredAt: t0}); err != nil {
		t.Fatal(err)
	}
	return caseGTIN, eachGTIN
}

func (e *env) node(t *testing.T, id, typ string, active bool) {
	t.Helper()
	ctx := context.Background()
	if err := e.svc.ApplyNodeRegistered(ctx, "ev-reg-"+id, requests.NodeRegistered{NodeID: id, NodeType: typ}); err != nil {
		t.Fatal(err)
	}
	if active {
		if err := e.svc.ApplyNodeActivated(ctx, "ev-act-"+id, requests.NodeLifecycle{NodeID: id}); err != nil {
			t.Fatal(err)
		}
	}
}

func eventNames(evs []shared.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.EventName()
	}
	return out
}
