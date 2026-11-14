-- Inbound capacity allocation and placement options (ADR 0015), reversed.
DROP TABLE inbound_capacity_allocations;
ALTER TABLE node_inbound_views DROP COLUMN monthly_volume_dm3;
ALTER TABLE inbound_plans
    DROP COLUMN placement_option,
    DROP COLUMN redistribution,
    DROP COLUMN split_limit_known,
    DROP COLUMN placement_fee_basis,
    DROP COLUMN capacity_enforced;
