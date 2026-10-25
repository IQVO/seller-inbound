import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { PlanScreen } from "./screens/PlanScreen";
import { ScorecardScreen } from "./screens/ScorecardScreen";

const SUB_NAV = [
  { to: "scorecard", label: "Scorecard" },
  { to: "plans", label: "Plans" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/seller-inbound/*">`. A relative `<NavLink>` rendered straight in
 *  that splat route resolves against the FULL current URL (react-router 7
 *  uses the last path-contributing match's pathname, splat included), so from
 *  /seller-inbound/plans the link `scorecard` would point at
 *  /seller-inbound/plans/scorecard. A pathless layout route does not help
 *  (react-router drops pathless routes from relative resolution); a layout
 *  route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at `/`. (Same fix as the other fleet remotes;
 *  App.test.tsx mounts under the host's splat route to prove it.) */
function InboundLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Seller inbound sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as seller_inbound_mfe/App via Module Federation. Takes NO props:
 *  the console mounts it under `/seller-inbound/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix
 *  (in the shell) or at / (standalone dev, see main.tsx). It calls ONLY this
 *  context's own API (/api/seller-inbound) and is read-only end to end.
 *
 *   - (index): redirects to scorecard.
 *   - scorecard: a seller's rolling-window compliance scorecard.
 *   - plans: one inbound plan, its state and its lines. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<InboundLayout />}>
        <Route index element={<Navigate to="scorecard" replace />} />
        <Route path="scorecard" element={<ScorecardScreen />} />
        <Route path="plans" element={<PlanScreen />} />
      </Route>
    </Routes>
  );
}
