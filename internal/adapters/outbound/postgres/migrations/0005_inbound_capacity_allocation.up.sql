-- Inbound capacity allocation and placement options (ADR 0015).

-- The reservation table: the dm3 a seller's DRAFT and CONFIRMED plans of the
-- month promise to one node. Write-side bookkeeping maintained by the use
-- cases (create reserves, cancel releases, confirm keeps); not an aggregate.
CREATE TABLE inbound_capacity_allocations (
    seller_id    TEXT        NOT NULL,
    month        TEXT        NOT NULL CHECK (month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    node_id      TEXT        NOT NULL,
    reserved_dm3 BIGINT      NOT NULL CHECK (reserved_dm3 >= 0),
    updated_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (seller_id, month, node_id)
);

-- NodeInboundView learns the consumed volumetric limit: the node's declared
-- monthly inbound dm3 (node-topology NodeOperationalProfileDeclared
-- inbound.monthlyVolumeDm3; NULL = not declared).
ALTER TABLE node_inbound_views
    ADD COLUMN monthly_volume_dm3 INTEGER CHECK (monthly_volume_dm3 BETWEEN 1 AND 2147483647);

-- The InboundPlan aggregate's placement facts of ADR 0015.
ALTER TABLE inbound_plans
    ADD COLUMN placement_option   TEXT  NOT NULL DEFAULT 'SELLER_SPLIT'
        CHECK (placement_option IN ('SELLER_SPLIT', 'NETWORK_SPLIT')),
    ADD COLUMN redistribution     JSONB,
    ADD COLUMN split_limit_known  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN placement_fee_basis JSONB,
    ADD COLUMN capacity_enforced  BOOLEAN NOT NULL DEFAULT FALSE;
