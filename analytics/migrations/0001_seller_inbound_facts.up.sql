-- seller-inbound analytics read model (ADR 0017).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/si-projector and read (read-only) by cmd/si-reports.
-- Everything here is a projection of this context's OWN two topics
-- (retailnetwork.seller-inbound.events and retailnetwork.seller-inbound.analytics),
-- not a source of truth, and can be dropped and rebuilt from them.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per inboundplan.InboundPlanConfirmed: a seller's inbound plan was
-- confirmed. confirmed_at is the payload's `confirmedAt`.
CREATE TABLE plan_confirmations (
    event_id     TEXT        PRIMARY KEY,
    plan_id      TEXT        NOT NULL CHECK (plan_id <> ''),
    seller_id    TEXT        NOT NULL CHECK (seller_id <> ''),
    confirmed_at TIMESTAMPTZ NOT NULL
);

-- One row per placementplan.PlacementPlanSuggested: a placement advisory was
-- recorded for one item of one seller. created_at is the payload's `createdAt`.
CREATE TABLE placement_advisories (
    event_id        TEXT        PRIMARY KEY,
    plan_id         TEXT        NOT NULL CHECK (plan_id <> ''),
    seller_id       TEXT        NOT NULL CHECK (seller_id <> ''),
    catalog_item_id TEXT        NOT NULL CHECK (catalog_item_id <> ''),
    created_at      TIMESTAMPTZ NOT NULL
);

-- One row per inboundshipment.InboundShipmentReceived. received_at is the
-- payload's `receivedAt` (the vendor's instant).
CREATE TABLE shipment_receipts (
    event_id    TEXT        PRIMARY KEY,
    shipment_id TEXT        NOT NULL CHECK (shipment_id <> ''),
    seller_id   TEXT        NOT NULL CHECK (seller_id <> ''),
    received_at TIMESTAMPTZ NOT NULL
);

-- One row per item of an InboundShipmentReceived `received` array (ADR 0014):
-- the stock received of one GTIN, in eaches. received_at repeats the receipt's
-- instant so a day's items need no join; catalog_item_id is '' when the
-- receipt did not resolve it. A receipt published before ADR 0014 has no row.
CREATE TABLE received_stock_lines (
    event_id        TEXT        NOT NULL REFERENCES shipment_receipts (event_id) ON DELETE CASCADE,
    gtin            TEXT        NOT NULL CHECK (gtin <> ''),
    catalog_item_id TEXT        NOT NULL DEFAULT '',
    qty_eaches      INTEGER     NOT NULL CHECK (qty_eaches > 0),
    seller_id       TEXT        NOT NULL CHECK (seller_id <> ''),
    received_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (event_id, gtin)
);

-- The reports filter each fact on its business instant, and the
-- advisory-to-receipt join looks the latest preceding advisory up by seller and item.
CREATE INDEX idx_plan_confirmations_confirmed_at ON plan_confirmations (confirmed_at);
CREATE INDEX idx_placement_advisories_created_at ON placement_advisories (created_at);
CREATE INDEX idx_placement_advisories_seller_item ON placement_advisories (seller_id, catalog_item_id, created_at);
CREATE INDEX idx_shipment_receipts_received_at ON shipment_receipts (received_at);
CREATE INDEX idx_received_stock_lines_received_at ON received_stock_lines (received_at);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
