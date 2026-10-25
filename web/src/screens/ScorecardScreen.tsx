import { useState } from "react";
import { getSellerScorecard } from "../api";
import type { ScorecardWindow, SellerScorecard } from "../types";
import { SCORECARD_WINDOWS } from "../types";
import { useRequest } from "../hooks/useRequest";
import { RequestView } from "../components/ProblemAlert";
import { DiscrepancyList, Facts, Stack, TierPill } from "../components/layout";
import { FilterBar, SelectField, SubmitButton, TextField } from "../components/formkit";
import { rateLabel, utcLabel } from "../lib";

/** One lookup the operator asked for; null before the first submit. */
interface Lookup {
  sellerId: string;
  window: ScorecardWindow;
}

/**
 * The compliance scorecard browser: GET /sellers/{sellerId}/scorecard over a
 * rolling 7d/30d window (ADR 0005). There is no seller list route in this
 * context's API, so the screen is a lookup: type a sellerId, pick a window,
 * submit. The tier is informational -- it suspends and blocks nothing.
 */
export function ScorecardScreen() {
  const [sellerId, setSellerId] = useState("");
  const [window, setWindow] = useState<ScorecardWindow>("30d");
  const [lookup, setLookup] = useState<Lookup | null>(null);

  const state = useRequest<SellerScorecard>(
    lookup ? () => getSellerScorecard(lookup.sellerId, lookup.window) : null,
    lookup ? `scorecard:${lookup.sellerId}:${lookup.window}` : "scorecard:none",
  );

  return (
    <section aria-label="Compliance scorecard">
      <h2 style={{ fontSize: "var(--wh-font-size-xl)", margin: 0 }}>Compliance scorecard</h2>
      <p style={{ color: "var(--wh-color-text-muted)" }}>
        A seller's rolling-window compliance scorecard, from the facts this service recorded: shipments closed, carton
        receipts, discrepancies and violations. The tier is informational; it suspends and blocks nothing.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const id = sellerId.trim();
          setLookup(id ? { sellerId: id, window } : null);
        }}
      >
        <FilterBar>
          <TextField
            label="Seller"
            value={sellerId}
            onChange={setSellerId}
            placeholder="seller-77"
            hint="A sellerId, e.g. seller-77"
          />
          <SelectField label="Window" value={window} onChange={(v) => setWindow(v as ScorecardWindow)} options={SCORECARD_WINDOWS} />
          <SubmitButton label="View scorecard" />
        </FilterBar>
      </form>
      <RequestView
        state={state}
        what="the scorecard"
        whenIdle="Enter a sellerId to read its scorecard."
      >
        {(scorecard) => (
          <Stack>
            <div style={{ display: "flex", alignItems: "center", gap: "var(--wh-space-3)" }}>
              <strong style={{ fontSize: "var(--wh-font-size-lg)" }}>{scorecard.sellerId}</strong>
              <TierPill tier={scorecard.complianceTier} />
            </div>
            <Facts
              items={[
                { label: "Window", value: `${scorecard.window} (${utcLabel(scorecard.windowStart)} to ${utcLabel(scorecard.windowEnd)})` },
                { label: "Shipments received", value: scorecard.shipmentsReceived },
                { label: "Cartons reported / received", value: `${scorecard.cartonsReported} / ${scorecard.cartonsReceived}` },
                { label: "Receipt accuracy", value: rateLabel(scorecard.receiptAccuracyRate) },
                { label: "Discrepancies per shipment", value: rateLabel(scorecard.discrepancyRatePerShipment) },
                { label: "Compliance violations", value: scorecard.complianceViolations },
                { label: "Discrepancies by kind", value: <DiscrepancyList byKind={scorecard.discrepanciesByKind} /> },
              ]}
            />
          </Stack>
        )}
      </RequestView>
    </section>
  );
}
