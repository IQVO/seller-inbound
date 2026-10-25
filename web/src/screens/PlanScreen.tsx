import { useState } from "react";
import { getInboundPlan } from "../api";
import type { InboundPlan } from "../types";
import { useRequest } from "../hooks/useRequest";
import { RequestView } from "../components/ProblemAlert";
import { Facts, PlanLinesTable, PlanStatePill, Stack } from "../components/layout";
import { FilterBar, SubmitButton, TextField } from "../components/formkit";
import { planStateSentence, utcLabel } from "../lib";

/**
 * The inbound plan browser: GET /inbound-plans/{planId}. There is no plan
 * list route in this context's API, so the screen is a lookup: type a planId,
 * submit. Read-only -- :confirm and :cancel stay REST commands, not buttons
 * here.
 */
export function PlanScreen() {
  const [planId, setPlanId] = useState("");
  const [lookedUp, setLookedUp] = useState<string | null>(null);

  const state = useRequest<InboundPlan>(
    lookedUp ? () => getInboundPlan(lookedUp) : null,
    lookedUp ? `plan:${lookedUp}` : "plan:none",
  );

  return (
    <section aria-label="Inbound plans">
      <h2 style={{ fontSize: "var(--wh-font-size-xl)", margin: 0 }}>Inbound plans</h2>
      <p style={{ color: "var(--wh-color-text-muted)" }}>
        What a seller declared it will send to one destination node, in eaches, and where the plan stands. Confirming
        and cancelling stay REST commands; this screen only reads.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const id = planId.trim();
          setLookedUp(id || null);
        }}
      >
        <FilterBar>
          <TextField
            label="Plan"
            value={planId}
            onChange={setPlanId}
            placeholder="pln-0f3a91"
            hint="A planId, e.g. pln-0f3a91"
          />
          <SubmitButton label="View plan" />
        </FilterBar>
      </form>
      <RequestView
        state={state}
        what="the plan"
        whenIdle="Enter a planId to read the plan."
      >
        {(plan) => (
          <Stack>
            <div style={{ display: "flex", alignItems: "center", gap: "var(--wh-space-3)" }}>
              <strong style={{ fontSize: "var(--wh-font-size-lg)" }}>{plan.planId}</strong>
              <PlanStatePill state={plan.state} />
              <span style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
                {planStateSentence(plan.state)}
              </span>
            </div>
            <Facts
              items={[
                { label: "Seller", value: plan.sellerId },
                { label: "Destination node", value: plan.destinationNodeId },
                { label: "Packing", value: plan.packingOption },
                { label: "Declared", value: utcLabel(plan.createdAt) },
                ...(plan.confirmedAt ? [{ label: "Confirmed", value: utcLabel(plan.confirmedAt) }] : []),
                ...(plan.cancelledAt ? [{ label: "Cancelled", value: utcLabel(plan.cancelledAt) }] : []),
                { label: "Lines (eaches)", value: plan.lines.reduce((sum, l) => sum + l.qtyEaches, 0) },
              ]}
            />
            <PlanLinesTable lines={plan.lines} />
          </Stack>
        )}
      </RequestView>
    </section>
  );
}
