import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { ScorecardScreen } from "./ScorecardScreen";
import { json, mockApi, pending, problem } from "../test/fetchMock";
import { goodScorecard, watchScorecard } from "../test/fixtures";

function mount() {
  return render(
    <MemoryRouter initialEntries={["/scorecard"]}>
      <ScorecardScreen />
    </MemoryRouter>,
  );
}

async function lookup(user: ReturnType<typeof userEvent.setup>, sellerId: string, window?: string) {
  await user.type(screen.getByLabelText(/^Seller/), sellerId);
  if (window) await user.selectOptions(screen.getByLabelText("Window"), window);
  await user.click(screen.getByRole("button", { name: "View scorecard" }));
}

describe("ScorecardScreen", () => {
  it("asks for a sellerId before anything is fetched", () => {
    mount();
    expect(screen.getByText("Enter a sellerId to read its scorecard.")).toBeInTheDocument();
  });

  it("renders the scorecard with its derived tier and discrepancies by kind", async () => {
    mockApi({ "GET /sellers/seller-77/scorecard": json(watchScorecard) });
    mount();
    const user = userEvent.setup();
    await lookup(user, "seller-77");
    expect(await screen.findByText("WATCH")).toBeInTheDocument();
    expect(screen.getByText("40 / 38")).toBeInTheDocument();
    expect(screen.getByText("95%")).toBeInTheDocument();
    expect(screen.getByText("Damaged: 1")).toBeInTheDocument();
    expect(screen.getByText("Short: 2")).toBeInTheDocument();
  });

  it("sends the sellerId and the chosen window to GET /scorecard", async () => {
    const api = mockApi({ "GET /sellers/seller-77/scorecard": json(goodScorecard) });
    mount();
    const user = userEvent.setup();
    await lookup(user, "seller-77", "7d");
    await waitFor(() => {
      const last = api.to("GET /sellers/seller-77/scorecard").at(-1);
      expect(last?.query.get("window")).toBe("7d");
    });
  });

  it("renders the problem body of a failed lookup", async () => {
    mockApi({
      "GET /sellers/seller-404/scorecard": problem(404, "not-found", "Not found", "no seller seller-404"),
    });
    mount();
    const user = userEvent.setup();
    await lookup(user, "seller-404");
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Not found")).toBeInTheDocument();
    expect(within(alert).getByText("no seller seller-404")).toBeInTheDocument();
  });

  it("shows the loading note while the scorecard is in flight", async () => {
    mockApi({ "GET /sellers/seller-77/scorecard": pending() });
    mount();
    const user = userEvent.setup();
    await lookup(user, "seller-77");
    expect(screen.getByRole("status")).toHaveTextContent("Loading the scorecard…");
  });
});
