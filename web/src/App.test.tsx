import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { casePackedPlan, watchScorecard } from "./test/fixtures";

/**
 * Mounts App exactly the way the console shell does: inside a splat route
 * (`/seller-inbound/*`), which is the case its relative sub-nav links must
 * survive (see InboundLayout).
 */
function mountHostedAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/seller-inbound/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("App (as seller_inbound_mfe/App under the shell's splat route)", () => {
  it("lands on the scorecard and navigates the sections with relative links", async () => {
    mockApi({
      "GET /sellers/seller-77/scorecard": json(watchScorecard),
      "GET /inbound-plans/pln-0f3a91": json(casePackedPlan),
    });
    mountHostedAt("/seller-inbound");
    expect(await screen.findByText("Compliance scorecard")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Plans" }));
    expect(await screen.findByText("Enter a planId to read the plan.")).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Scorecard" }));
    expect(await screen.findByText("Enter a sellerId to read its scorecard.")).toBeInTheDocument();
  });

  it("looks a plan up from the plans section", async () => {
    mockApi({ "GET /inbound-plans/pln-0f3a91": json(casePackedPlan) });
    mountHostedAt("/seller-inbound/plans");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/^Plan/), "pln-0f3a91");
    await user.click(screen.getByRole("button", { name: "View plan" }));
    expect(await screen.findByText("CASE_PACKED")).toBeInTheDocument();
    expect(screen.getByText("item-004512")).toBeInTheDocument();
  });
});
