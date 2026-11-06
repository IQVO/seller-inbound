-- Placement advisory (ADR 0012). Three tables and one column addition:
-- the node view learns the region and the operator the advisory resolves
-- demand by, DemandHeatView gets its demand and availability halves, and the
-- PlacementPlan aggregate gets its table.

-- NodeInboundView: the region and operator a node declared at registration
-- (NodeRegistered). Empty means the registration the view folded pre-dates
-- this column; a re-registration fills it.
ALTER TABLE node_inbound_views
    ADD COLUMN operator  TEXT NOT NULL DEFAULT '',
    ADD COLUMN region_id TEXT NOT NULL DEFAULT '';

-- DemandHeatView, demand half: one row per LIVE released line of an
-- assignment group. A group's next release (a reassignment re-release)
-- replaces its rows; FulfillmentCancelled deletes them. The advisory sums
-- quantity per (item, region of the node) over the trailing window.
CREATE TABLE demand_heat_lines (
    assignment_id   TEXT        NOT NULL,
    group_no        INTEGER     NOT NULL CHECK (group_no >= 1),
    line_no         INTEGER     NOT NULL CHECK (line_no >= 1),
    node_id         TEXT        NOT NULL,
    catalog_item_id TEXT        NOT NULL,
    quantity        INTEGER     NOT NULL CHECK (quantity >= 1),
    released_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (assignment_id, group_no, line_no)
);

CREATE INDEX idx_demand_heat_item_released ON demand_heat_lines (catalog_item_id, released_at);
CREATE INDEX idx_demand_heat_node ON demand_heat_lines (node_id);

-- DemandHeatView, availability half: the latest ListingAvailabilityChanged
-- per (item, node) -- the availability the advisory credits a node with.
CREATE TABLE listing_availability (
    catalog_item_id TEXT        NOT NULL,
    node_id         TEXT        NOT NULL,
    listing_id      TEXT        NOT NULL,
    available_qty   INTEGER     NOT NULL CHECK (available_qty >= 0),
    as_of           TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (catalog_item_id, node_id)
);

-- PlacementPlan aggregate. Suggestions are value objects and live in JSONB.
CREATE TABLE placement_plans (
    plan_id         TEXT        PRIMARY KEY,
    seller_id       TEXT        NOT NULL,
    catalog_item_id TEXT        NOT NULL,
    cover_days      INTEGER     NOT NULL CHECK (cover_days BETWEEN 1 AND 90),
    window_days     INTEGER     NOT NULL CHECK (window_days >= 1),
    basis           TEXT        NOT NULL,
    state           TEXT        NOT NULL CHECK (state IN ('SUGGESTED', 'ACCEPTED', 'SUPERSEDED')),
    suggestions     JSONB       NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL,
    accepted_at     TIMESTAMPTZ,
    superseded_at   TIMESTAMPTZ,
    version         BIGINT      NOT NULL CHECK (version >= 1)
);

CREATE INDEX idx_placement_plans_seller_item ON placement_plans (seller_id, catalog_item_id, created_at, plan_id);
