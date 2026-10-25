/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

/** GET /sellers/{sellerId}/scorecard?window= -- the rolling window, ending now. */
export const SCORECARD_WINDOWS = ["7d", "30d"] as const;
export type ScorecardWindow = (typeof SCORECARD_WINDOWS)[number];

/** Derived, informational; the worst of three signals (ADR 0005). */
export const COMPLIANCE_TIERS = ["GOOD", "WATCH", "AT_RISK"] as const;
export type ComplianceTier = (typeof COMPLIANCE_TIERS)[number];

export interface SellerScorecard {
  sellerId: string;
  window: ScorecardWindow;
  windowStart: string;
  windowEnd: string;
  shipmentsReceived: number;
  cartonsReported: number;
  cartonsReceived: number;
  /** cartonsReceived / cartonsReported, four decimals; omitted when no carton was reported. */
  receiptAccuracyRate?: number;
  /** Discrepancies / shipmentsReceived, four decimals; omitted when no shipment closed. */
  discrepancyRatePerShipment?: number;
  /** Per kind (MissingCarton, UnexpectedCarton, Damaged, Short, Over); open vocabulary, never undefined. */
  discrepanciesByKind: Record<string, number>;
  complianceViolations: number;
  complianceTier: ComplianceTier;
}

export const PACKING_OPTIONS = ["INDIVIDUAL", "CASE_PACKED"] as const;
export type PackingOption = (typeof PACKING_OPTIONS)[number];

export const PLAN_STATES = ["DRAFT", "CONFIRMED", "CANCELLED"] as const;
export type PlanState = (typeof PLAN_STATES)[number];

export interface PlanLine {
  catalogItemId: string;
  /** 14 digits with a GS1 mod-10 check digit. */
  gtin: string;
  /** Eaches, never cases. */
  qtyEaches: number;
}

export interface InboundPlan {
  planId: string;
  sellerId: string;
  destinationNodeId: string;
  packingOption: PackingOption;
  state: PlanState;
  lines: PlanLine[];
  createdAt: string;
  confirmedAt?: string;
  cancelledAt?: string;
}
