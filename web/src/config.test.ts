import { describe, expect, it } from "vitest";
import { resolveSellerInboundApiBase } from "./config";

describe("resolveSellerInboundApiBase", () => {
  it("appends this context's API path to the runtime apiOrigin", () => {
    expect(resolveSellerInboundApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/seller-inbound",
    );
  });

  it("trims trailing slashes off the origin", () => {
    expect(resolveSellerInboundApiBase({ apiOrigin: "http://localhost:8000/" }, false)).toBe(
      "http://localhost:8000/api/seller-inbound",
    );
  });

  it("falls back to the local api port in development", () => {
    expect(resolveSellerInboundApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to boot without an origin in production", () => {
    expect(() => resolveSellerInboundApiBase({}, true)).toThrow(/apiOrigin is required/);
  });
});
