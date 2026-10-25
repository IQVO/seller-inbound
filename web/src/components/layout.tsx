import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import type { ComplianceTier, PlanLine, PlanState } from "../types";
import { tierTone } from "../lib";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** A seller's derived compliance tier (ADR 0005): informational, never a command. */
export function TierPill({ tier }: { tier: ComplianceTier }) {
  return <StatusPill status={tier} tone={tierTone(tier)} size="sm" />;
}

/** A plan's lifecycle: DRAFT awaits confirmation, CONFIRMED feeds shipments. */
export function PlanStatePill({ state }: { state: PlanState }) {
  const tone: "neutral" | "progress" | "warning" =
    state === "CONFIRMED" ? "progress" : state === "DRAFT" ? "warning" : "neutral";
  return <StatusPill status={state} tone={tone} size="sm" />;
}

/** The plan's lines: one row per catalog item, quantities in eaches. */
export function PlanLinesTable({ lines }: { lines: PlanLine[] }) {
  return (
    <DataTable
      rowKey={(l) => `${l.catalogItemId}/${l.gtin}`}
      rows={lines}
      columns={[
        { key: "item", header: "Catalog item", render: (l) => l.catalogItemId },
        { key: "gtin", header: "Case GTIN", render: (l) => l.gtin },
        { key: "qty", header: "Eaches", align: "right", render: (l) => l.qtyEaches },
      ]}
    />
  );
}

/** The shape of one scored discrepancy kind and its count. */
export function DiscrepancyList({ byKind }: { byKind: Record<string, number> }) {
  const entries = Object.entries(byKind).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return <EmptyFacts />;
  return (
    <ul style={{ margin: 0, paddingLeft: "var(--wh-space-5)", fontSize: "var(--wh-font-size-sm)" }}>
      {entries.map(([kind, count]) => (
        <li key={kind}>
          {kind}: {count}
        </li>
      ))}
    </ul>
  );
}

function EmptyFacts() {
  return <span style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>none</span>;
}
