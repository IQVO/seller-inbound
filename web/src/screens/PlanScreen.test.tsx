import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { PlanScreen } from "./PlanScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { casePackedPlan, draftPlan } from "../test/fixtures";

function mount() {
  return render(
    <MemoryRouter initialEntries={["/plans"]}>
      <PlanScreen />
    </MemoryRouter>,
  );
}

async function lookup(user: ReturnType<typeof userEvent.setup>, planId: string) {
  await user.type(screen.getByLabelText(/^Plan/), planId);
  await user.click(screen.getByRole("button", { name: "View plan" }));
}

describe("PlanScreen", () => {
  it("asks for a planId before anything is fetched", () => {
    mount();
    expect(screen.getByText("Enter a planId to read the plan.")).toBeInTheDocument();
  });

  it("renders the plan with its state, facts and lines", async () => {
    mockApi({ "GET /inbound-plans/pln-0f3a91": json(casePackedPlan) });
    mount();
    const user = userEvent.setup();
    await lookup(user, "pln-0f3a91");
    expect(await screen.findByText("CONFIRMED")).toBeInTheDocument();
    expect(screen.getByText("Confirmed; shipments may be opened against it.")).toBeInTheDocument();
    expect(screen.getByText("node-0f3a91")).toBeInTheDocument();
    const table = screen.getByRole("table");
    expect(within(table).getByText("item-004512")).toBeInTheDocument();
    expect(within(table).getByText("item-009911")).toBeInTheDocument();
    expect(within(table).getAllByText("24")).toHaveLength(1);
  });

  it("renders a draft plan with its declared instant and no confirmed fact", async () => {
    mockApi({ "GET /inbound-plans/pln-1a2b3c": json(draftPlan) });
    mount();
    const user = userEvent.setup();
    await lookup(user, "pln-1a2b3c");
    expect(await screen.findByText("DRAFT")).toBeInTheDocument();
    expect(screen.getByText("Declared by the seller, not confirmed yet.")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 14:00:00Z")).toBeInTheDocument();
    expect(screen.queryByText("Confirmed")).not.toBeInTheDocument();
  });

  it("sends exactly the planId to GET /inbound-plans", async () => {
    const api = mockApi({ "GET /inbound-plans/pln-0f3a91": json(casePackedPlan) });
    mount();
    const user = userEvent.setup();
    await lookup(user, "pln-0f3a91");
    await waitFor(() => {
      expect(api.to("GET /inbound-plans/pln-0f3a91")).toHaveLength(1);
    });
  });

  it("renders the problem body of a failed lookup", async () => {
    mockApi({
      "GET /inbound-plans/pln-nope": problem(404, "not-found", "Not found", "no plan pln-nope"),
    });
    mount();
    const user = userEvent.setup();
    await lookup(user, "pln-nope");
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Not found")).toBeInTheDocument();
    expect(within(alert).getByText("no plan pln-nope")).toBeInTheDocument();
  });
});
