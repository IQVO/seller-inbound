-- seller-inbound schema (ADR 0002). The context owns its database.

-- SellerAccount aggregate.
CREATE TABLE sellers (
    seller_id      TEXT        PRIMARY KEY,
    name           TEXT        NOT NULL,
    state          TEXT        NOT NULL CHECK (state IN ('ACTIVE', 'SUSPENDED')),
    onboarded_at   TIMESTAMPTZ NOT NULL,
    suspended_at   TIMESTAMPTZ,
    suspend_reason TEXT        NOT NULL DEFAULT '',
    version        BIGINT      NOT NULL CHECK (version >= 1)
);

-- InboundPlan aggregate. The lines are value objects of the aggregate and live in JSONB.
CREATE TABLE inbound_plans (
    plan_id             TEXT        PRIMARY KEY,
    seller_id           TEXT        NOT NULL,
    destination_node_id TEXT        NOT NULL,
    packing_option      TEXT        NOT NULL CHECK (packing_option IN ('INDIVIDUAL', 'CASE_PACKED')),
    state               TEXT        NOT NULL CHECK (state IN ('DRAFT', 'CONFIRMED', 'CANCELLED')),
    lines               JSONB       NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    confirmed_at        TIMESTAMPTZ,
    cancelled_at        TIMESTAMPTZ,
    version             BIGINT      NOT NULL CHECK (version >= 1)
);

CREATE INDEX idx_inbound_plans_seller ON inbound_plans (seller_id, created_at, plan_id);

-- InboundShipment aggregate with its Carton entities (JSONB: a shipment is always
-- read and written whole). unexpected lists the SSCCs of cartons the vendor
-- received that the manifest does not list.
CREATE TABLE inbound_shipments (
    shipment_id TEXT        PRIMARY KEY,
    plan_id     TEXT        NOT NULL,
    seller_id   TEXT        NOT NULL,
    node_id     TEXT        NOT NULL,
    state       TEXT        NOT NULL CHECK (state IN ('DRAFT', 'LABELLED', 'SHIPPED', 'RECEIVING', 'CLOSED', 'CLOSED_WITH_DISCREPANCY')),
    cartons     JSONB       NOT NULL DEFAULT '[]',
    unexpected  JSONB       NOT NULL DEFAULT '[]',
    discrepant  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL,
    labelled_at TIMESTAMPTZ,
    shipped_at  TIMESTAMPTZ,
    received_at TIMESTAMPTZ,
    version     BIGINT      NOT NULL CHECK (version >= 1)
);

CREATE INDEX idx_inbound_shipments_plan ON inbound_shipments (plan_id, created_at, shipment_id);

-- sscc_registry: every carton SSCC of every shipment, unique network-wide. The
-- shipment repository registers the SSCCs of a saved shipment here in the same
-- transaction and refuses the save when another shipment holds one of them.
CREATE TABLE sscc_registry (
    sscc        TEXT PRIMARY KEY,
    shipment_id TEXT NOT NULL
);

CREATE INDEX idx_sscc_registry_shipment ON sscc_registry (shipment_id);

-- Serial references of the SSCCs the network issues from its own GS1 company prefix.
CREATE SEQUENCE sscc_serial_seq AS BIGINT START WITH 1 INCREMENT BY 1 MINVALUE 1 NO CYCLE;

-- NodeInboundView: event-fed read model of node-topology's nodes (ADR 0003).
CREATE TABLE node_inbound_views (
    node_id          TEXT    PRIMARY KEY,
    node_type        TEXT    NOT NULL DEFAULT '',
    status           TEXT    NOT NULL DEFAULT '',
    profile_revision INTEGER NOT NULL DEFAULT 0 CHECK (profile_revision >= 0),
    inbound_limits   JSONB
);

-- CatalogPackagingView: event-fed read model of offer-availability's pack levels.
CREATE TABLE catalog_pack_levels (
    catalog_item_id TEXT        NOT NULL,
    level           TEXT        NOT NULL CHECK (level IN ('EACH', 'INNER', 'CASE', 'PALLET')),
    gtin            TEXT        NOT NULL,
    child_gtin      TEXT        NOT NULL DEFAULT '',
    units_per_pack  INTEGER     NOT NULL DEFAULT 0 CHECK (units_per_pack >= 0),
    active          BOOLEAN     NOT NULL,
    declared_at     TIMESTAMPTZ,
    retired_at      TIMESTAMPTZ,
    PRIMARY KEY (catalog_item_id, level, gtin)
);

-- outbox_events: the transactional outbox. One row per already-encoded Kafka
-- message, inserted in the SAME transaction as the aggregates; the relay drains
-- unpublished rows to Kafka.
--   event_id    the CloudEvents `id`, minted once at encode time and persisted:
--               a relay retry republishes the same id.
--   event_type  the FULL CloudEvents `type`.
--   subject     the CloudEvents `subject` (the aggregate root id).
--   key         the Kafka message key (the aggregate root id).
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: the inbox of the consumers. The CloudEvents id is claimed
-- with INSERT ... ON CONFLICT DO NOTHING in the SAME transaction as the effect,
-- so a rollback un-claims it.
CREATE TABLE processed_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL
);

-- idempotency_keys: the fleet Idempotency-Key middleware, keyed by (route, key)
-- with a 24 hour TTL (model conventions). The middleware INSERTs a bare row
-- inside the transaction shared with the use case, then UPDATEs it with the
-- outcome before it commits, so a COMMITTED row always has a status_code.
CREATE TABLE idempotency_keys (
    route            TEXT        NOT NULL,
    key              TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ,
    PRIMARY KEY (route, key)
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
