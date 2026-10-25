import type { ChangeEvent, ReactNode } from "react";

/**
 * Minimal styled form primitives for this remote's screens. Copied per remote
 * on purpose (not promoted to @warehouse/ui-kit: each remote's copy is allowed
 * to diverge) and styled purely off the shared design tokens so it looks
 * native inside the console shell. Every control is labelled by its visible
 * text, which is also how the tests find it.
 */

const labelStyle = {
  fontSize: "var(--wh-font-size-xs)",
  color: "var(--wh-color-text-muted)",
  fontWeight: 600,
};

const inputStyle = {
  padding: "8px 10px",
  borderRadius: "var(--wh-radius-md)",
  border: "1px solid var(--wh-color-border)",
  background: "var(--wh-color-bg-sunken)",
  color: "var(--wh-color-text)",
  fontFamily: "var(--wh-font-mono)",
  fontSize: "var(--wh-font-size-sm)",
};

const wrapStyle = {
  display: "flex",
  flexDirection: "column" as const,
  gap: 4,
};

/** A labelled text input; the label is the accessible name. */
export function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  /** Short helper text under the input. */
  hint?: string;
}) {
  return (
    <label style={wrapStyle}>
      <span style={labelStyle}>{label}</span>
      <input
        style={inputStyle}
        value={value}
        placeholder={placeholder}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value)}
      />
      {hint && (
        <span style={{ fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-muted)" }}>{hint}</span>
      )}
    </label>
  );
}

/** A labelled select over an option list. */
export function SelectField({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: readonly string[];
}) {
  return (
    <label style={wrapStyle}>
      <span style={labelStyle}>{label}</span>
      <select
        style={{ ...inputStyle, fontFamily: "inherit" }}
        value={value}
        onChange={(e: ChangeEvent<HTMLSelectElement>) => onChange(e.target.value)}
      >
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}

/** A row of lookup controls with a consistent gap. */
export function FilterBar({ children }: { children: ReactNode }) {
  return (
    <div style={{ display: "flex", gap: "var(--wh-space-4)", flexWrap: "wrap", alignItems: "flex-end" }}>
      {children}
    </div>
  );
}

/** The button that submits a lookup form. */
export function SubmitButton({ label }: { label: string }) {
  return (
    <button
      type="submit"
      style={{
        padding: "8px 14px",
        borderRadius: "var(--wh-radius-md)",
        border: "1px solid var(--wh-color-border)",
        background: "var(--wh-color-accent-muted)",
        color: "var(--wh-color-text)",
        fontSize: "var(--wh-font-size-sm)",
        fontWeight: 600,
      }}
    >
      {label}
    </button>
  );
}
