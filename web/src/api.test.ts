import { describe, expect, it } from "vitest";
import { getSellerScorecard, toApiError, withQuery, ApiError } from "./api";
import { json, mockApi, problem } from "./test/fetchMock";
import { goodScorecard, watchScorecard } from "./test/fixtures";

describe("withQuery", () => {
  it("appends only the values that exist", () => {
    expect(withQuery("/sellers/s-1/scorecard", { window: "7d" })).toBe("/sellers/s-1/scorecard?window=7d");
    expect(withQuery("/sellers/s-1/scorecard", { window: undefined })).toBe("/sellers/s-1/scorecard");
    expect(withQuery("/sellers/s-1/scorecard", { window: "" })).toBe("/sellers/s-1/scorecard");
  });
});

describe("toApiError", () => {
  it("keeps an ApiError as is", () => {
    const err = new ApiError(404, { title: "Not found" }, "fallback");
    expect(toApiError(err)).toBe(err);
  });

  it("wraps anything else as a network error", () => {
    const err = toApiError(new TypeError("boom"));
    expect(err.status).toBe(0);
    expect(err.title).toBe("Network error");
    expect(err.detail).toBe("boom");
  });
});

describe("getSellerScorecard", () => {
  it("reads the scorecard and defaults a missing discrepancies map", async () => {
    mockApi({
      "GET /sellers/seller-77/scorecard": json({ ...watchScorecard, discrepanciesByKind: undefined }),
    });
    const scorecard = await getSellerScorecard("seller-77", "30d");
    expect(scorecard.discrepanciesByKind).toEqual({});
  });

  it("sends the window and keeps the problem body of a failure", async () => {
    const api = mockApi({
      "GET /sellers/seller-77/scorecard": json(goodScorecard),
      "GET /sellers/seller-x/scorecard": problem(400, "invalid-window", "Invalid window", "window must be 7d or 30d"),
    });
    await getSellerScorecard("seller-77", "7d");
    expect(api.to("GET /sellers/seller-77/scorecard").at(-1)?.query.get("window")).toBe("7d");

    await expect(getSellerScorecard("seller-x", "30d")).rejects.toMatchObject({
      status: 400,
      title: "Invalid window",
      detail: "window must be 7d or 30d",
      slug: "invalid-window",
    });
  });
});
