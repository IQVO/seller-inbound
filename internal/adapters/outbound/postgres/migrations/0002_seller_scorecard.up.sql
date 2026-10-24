-- Seller compliance scorecard facts (ADR 0005). The scorecard is computed from
-- these two append-only tables, written in the SAME transaction as the shipment
-- save and the outbox append of a vendor receipt. recorded_at is the instant
-- this service recorded the fact, never the receivedAt the vendor claims.

-- One row per shipment that closed (InboundShipmentReceived).
CREATE TABLE seller_scorecard_receipts (
    shipment_id     TEXT        PRIMARY KEY,
    seller_id       TEXT        NOT NULL,
    cartons_reported INTEGER    NOT NULL CHECK (cartons_reported >= 0),
    cartons_received INTEGER    NOT NULL CHECK (cartons_received >= 0 AND cartons_received <= cartons_reported),
    recorded_at     TIMESTAMPTZ NOT NULL
);

-- One row per InboundDiscrepancyRecorded (code = kind) and per
-- SellerComplianceViolationRecorded (code = compliance code).
CREATE TABLE seller_scorecard_occurrences (
    id              BIGSERIAL   PRIMARY KEY,
    seller_id       TEXT        NOT NULL,
    shipment_id     TEXT        NOT NULL,
    occurrence_type TEXT        NOT NULL CHECK (occurrence_type IN ('DISCREPANCY', 'VIOLATION')),
    code            TEXT        NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL
);

-- The window query is "this seller, recorded since T".
CREATE INDEX idx_scorecard_receipts_seller_recorded ON seller_scorecard_receipts (seller_id, recorded_at);
CREATE INDEX idx_scorecard_occurrences_seller_recorded ON seller_scorecard_occurrences (seller_id, recorded_at);

-- Backfill from the events this service already published: the outbox holds the
-- encoded CloudEvents of every closing, discrepancy and violation, with the
-- instant it was written. A deployment that already closed shipments therefore
-- starts with the same scorecard it would have had. Idempotent by shipment_id.
INSERT INTO seller_scorecard_receipts (shipment_id, seller_id, cartons_reported, cartons_received, recorded_at)
SELECT o.subject,
       data ->> 'sellerId',
       jsonb_array_length(data -> 'cartons'),
       (SELECT count(*) FROM jsonb_array_elements(data -> 'cartons') AS c WHERE c ->> 'status' = 'RECEIVED'),
       o.created_at
FROM outbox_events o, LATERAL (SELECT convert_from(o.value, 'UTF8')::jsonb -> 'data' AS data) AS d
WHERE o.event_type = 'com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived'
ON CONFLICT (shipment_id) DO NOTHING;

INSERT INTO seller_scorecard_occurrences (seller_id, shipment_id, occurrence_type, code, recorded_at)
SELECT data ->> 'sellerId', COALESCE(data ->> 'shipmentId', ''), 'DISCREPANCY', data ->> 'kind', o.created_at
FROM outbox_events o, LATERAL (SELECT convert_from(o.value, 'UTF8')::jsonb -> 'data' AS data) AS d
WHERE o.event_type = 'com.retailnetwork.partner.seller-inbound.inboundshipment.InboundDiscrepancyRecorded'
UNION ALL
SELECT data ->> 'sellerId', COALESCE(data ->> 'shipmentId', ''), 'VIOLATION', data ->> 'code', o.created_at
FROM outbox_events o, LATERAL (SELECT convert_from(o.value, 'UTF8')::jsonb -> 'data' AS data) AS d
WHERE o.event_type = 'com.retailnetwork.partner.seller-inbound.seller.SellerComplianceViolationRecorded';
