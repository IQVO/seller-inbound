import { describe, expect, it } from "vitest";
import { kindLabel, planStateSentence, rateLabel, tierTone, utcLabel } from "./lib";

describe("utcLabel", () => {
  it("formats an RFC 3339 instant to the second", () => {
    expect(utcLabel("2026-10-09T08:05:30Z")).toBe("2026-10-09 08:05:30Z");
  });

  it("shows the raw text when unparseable, and no value when absent", () => {
    expect(utcLabel("not-a-date")).toBe("not-a-date");
    expect(utcLabel(undefined)).toBe("—");
  });
});

describe("rateLabel", () => {
  it("renders a four-decimal rate as a percentage with one decimal at most", () => {
    expect(rateLabel(0.95)).toBe("95%");
    expect(rateLabel(0.975)).toBe("97.5%");
    expect(rateLabel(1)).toBe("100%");
  });

  it("renders no value when the rate is omitted", () => {
    expect(rateLabel(undefined)).toBe("—");
  });
});

describe("tierTone", () => {
  it("maps the three tiers to the status colours", () => {
    expect(tierTone("GOOD")).toBe("success");
    expect(tierTone("WATCH")).toBe("warning");
    expect(tierTone("AT_RISK")).toBe("danger");
  });
});

describe("kindLabel", () => {
  it("words the known kinds and passes an open-vocabulary kind through", () => {
    expect(kindLabel("MissingCarton")).toBe("missing carton");
    expect(kindLabel("Short")).toBe("short");
    expect(kindLabel("SomethingNew")).toBe("SomethingNew");
  });
});

describe("planStateSentence", () => {
  it("sentences the three plan states", () => {
    expect(planStateSentence("DRAFT")).toBe("Declared by the seller, not confirmed yet.");
    expect(planStateSentence("CONFIRMED")).toBe("Confirmed; shipments may be opened against it.");
    expect(planStateSentence("CANCELLED")).toBe("Cancelled; no shipment may be opened against it.");
  });
});
