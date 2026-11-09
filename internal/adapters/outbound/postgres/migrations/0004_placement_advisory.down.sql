-- Placement advisory (ADR 0012), the down migration.

DROP TABLE IF EXISTS placement_plans;
DROP TABLE IF EXISTS listing_availability;
DROP TABLE IF EXISTS demand_heat_lines;
DROP INDEX IF EXISTS idx_demand_heat_node;
DROP INDEX IF EXISTS idx_demand_heat_item_released;

ALTER TABLE node_inbound_views
    DROP COLUMN IF EXISTS operator,
    DROP COLUMN IF EXISTS region_id;
