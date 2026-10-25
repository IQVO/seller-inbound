# ADR 0007: a web console remote

## Status

Accepted (2026-10-25).

## Context

The fleet's operators read every bounded context through the warehouse-console
shell, a Module Federation host that lazy-loads one remote per context. Every
context that exposes reads to operators ships such a remote
(node-topology, inbound-receiving, offer-availability, ...); seller-inbound
had none, so reading a seller's compliance scorecard or an inbound plan meant
raw REST calls (or the MCP server of ADR 0006, which serves agents, not
operators with a browser).

## Decision

- **`web/` is the `seller_inbound_mfe` Module Federation remote.** It exposes
  exactly `./App` (a default export, no props); the console lazy-loads it as
  `seller_inbound_mfe/App` and mounts it under its own `/seller-inbound/*`
  splat route, providing the `BrowserRouter`, the design tokens and the
  runtime config. It also runs standalone on port 4022 (`npm run dev` /
  `npm run preview`, `strictPort`) without the shell.
- **A small, REAL, read-only UI: two screens over this context's own GET
  routes.** A compliance scorecard browser (`GET
  /sellers/{sellerId}/scorecard?window=7d|30d`: the derived tier, the
  shipments/cartons counts, the rates, the discrepancies by kind -- ADR 0005)
  and an inbound plan browser (`GET /inbound-plans/{planId}`: the state, the
  facts and the lines in eaches). Both are lookups: this context's API has no
  list route for sellers or plans, so the screens take an id, not filters.
  Nothing writes: the remote has no mutation call, no `Idempotency-Key`, no
  POST/PUT anywhere -- `:confirm` and `:cancel` stay REST commands.
- **Runtime endpoint resolution, not build-time.** `src/config.ts` turns
  `window.__WAREHOUSE_CONFIG__.apiOrigin` (published by the shell from
  `/config.json`) into `${apiOrigin}/api/seller-inbound`, the only base this
  remote ever calls; a production build without an origin refuses to render
  rather than falling back to a developer port.
- **The deployment shape of the fleet.** `web/Dockerfile` is the two-stage
  uikit named-context build (node:22-bookworm-slim; `@warehouse/ui-kit` is the
  `file:../../warehouse-ui-kit` sibling checkout, built first and consumed from
  its `dist`), served by nginx-unprivileged with the fleet's `nginx.conf`
  (unhashed `remoteEntry.js` and `index.html` never cached; immutable hashed
  assets; SPA fallback; `/healthz` for the probes). Vite's `base` is
  `/mfes/seller-inbound/` on build (STATIC object config, never a callback, so
  configs stay mergeable) and the web gateway — not this chart — owns that
  public path.
- **Chart: an optional `frontend` component, off by default.** Its own
  Deployment + ClusterIP Service (`component=frontend`), no database, no
  Kafka, no routing in this chart. CI grows a `web` job (ui-kit checked out as
  a sibling; oxlint, `tsc -b`, vitest, `vite build`).

## Consequences

- Operators read a seller's compliance posture and a plan without REST
  tooling; agents keep using the MCP server of ADR 0006 against the same use
  cases.
- The remote is deployable as one image in more than one environment: only
  `/config.json` changes between them.
- The console shell must carry the matching federation entry
  (`seller_inbound_mfe` at `./App`, remote entry under
  `/mfes/seller-inbound/`) — a pinned host/remote contract, changed on both
  sides together or not at all.
