import type { InboundPlan, SellerScorecard } from "../types";

/** Wire-shaped fixtures the tests serve through the fetch mock. */

export const watchScorecard: SellerScorecard = {
  sellerId: "seller-77",
  window: "30d",
  windowStart: "2026-09-24T13:00:00Z",
  windowEnd: "2026-10-24T13:00:00Z",
  shipmentsReceived: 4,
  cartonsReported: 40,
  cartonsReceived: 38,
  receiptAccuracyRate: 0.95,
  discrepancyRatePerShipment: 0.75,
  discrepanciesByKind: { Damaged: 1, Short: 2 },
  complianceViolations: 1,
  complianceTier: "WATCH",
};

export const goodScorecard: SellerScorecard = {
  sellerId: "seller-78",
  window: "7d",
  windowStart: "2026-10-17T13:00:00Z",
  windowEnd: "2026-10-24T13:00:00Z",
  shipmentsReceived: 0,
  cartonsReported: 0,
  cartonsReceived: 0,
  discrepanciesByKind: {},
  complianceViolations: 0,
  complianceTier: "GOOD",
};

export const casePackedPlan: InboundPlan = {
  planId: "pln-0f3a91",
  sellerId: "seller-77",
  destinationNodeId: "node-0f3a91",
  packingOption: "CASE_PACKED",
  state: "CONFIRMED",
  lines: [
    { catalogItemId: "item-004512", gtin: "00614141000036", qtyEaches: 24 },
    { catalogItemId: "item-009911", gtin: "00614141000180", qtyEaches: 12 },
  ],
  createdAt: "2026-10-24T13:00:00Z",
  confirmedAt: "2026-10-24T13:30:00Z",
};

export const draftPlan: InboundPlan = {
  planId: "pln-1a2b3c",
  sellerId: "seller-78",
  destinationNodeId: "node-0f3a91",
  packingOption: "INDIVIDUAL",
  state: "DRAFT",
  lines: [{ catalogItemId: "item-004512", gtin: "00614141000036", qtyEaches: 6 }],
  createdAt: "2026-10-24T14:00:00Z",
};
