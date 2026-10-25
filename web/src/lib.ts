import { NO_VALUE } from "@warehouse/ui-kit";
import type { ComplianceTier } from "./types";

/** "2026-10-09 08:05:30Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/**
 * A four-decimal API rate as a percentage: 0.95 -> "95%", 0.975 -> "97.5%".
 * One decimal at most (the underlying rate carries the real precision).
 */
export function rateLabel(rate: number | undefined): string {
  if (rate === undefined) return NO_VALUE;
  const pct = rate * 100;
  return `${Number.isInteger(pct) ? pct : Math.round(pct * 10) / 10}%`;
}

/** The tone a tier carries on a StatusPill: GOOD is fine, AT_RISK is not. */
export function tierTone(tier: ComplianceTier): "success" | "warning" | "danger" {
  if (tier === "GOOD") return "success";
  if (tier === "WATCH") return "warning";
  return "danger";
}

/** The word for a carton discrepancy kind, as the vendor recorded it. */
export function kindLabel(kind: string): string {
  const words: Record<string, string> = {
    MissingCarton: "missing carton",
    UnexpectedCarton: "unexpected carton",
    Damaged: "damaged",
    Short: "short",
    Over: "over",
  };
  return words[kind] ?? kind;
}

/** The sentence a plan's state reads as: DRAFT has not been confirmed. */
export function planStateSentence(state: string): string {
  const sentences: Record<string, string> = {
    DRAFT: "Declared by the seller, not confirmed yet.",
    CONFIRMED: "Confirmed; shipments may be opened against it.",
    CANCELLED: "Cancelled; no shipment may be opened against it.",
  };
  return sentences[state] ?? state;
}
