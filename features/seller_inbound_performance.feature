Feature: Seller-inbound performance report
  The analytics read side folds this context's OWN events (inbound plans
  confirmed, placement advisories, received stock) into one report per UTC
  day, served by the separate reports process. Events reach the projection
  through the real analytics consumer, shaped by the real encoder.

  Scenario: Nothing was projected, so the report and the freshness are empty
    When I request the seller-inbound performance report from "2026-11-01" to "2026-11-07"
    Then the report status is 200
    And the report covers "2026-11-01" to "2026-11-07" with 0 days
    When I request the report freshness
    Then the report status is 200
    And the freshness has no event yet

  Scenario: Facts outside the range leave the report empty
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-10-20T09:00:00Z"
    And the shipment "shp-1" of seller "S-1" was received at "2026-11-09T09:00:00Z" with these items:
      | gtin           | catalogItemId | qtyEaches |
      | 00614141000012 | item-1        | 12        |
    When I request the seller-inbound performance report from "2026-11-01" to "2026-11-07"
    Then the report status is 200
    And the report covers "2026-11-01" to "2026-11-07" with 0 days

  Scenario: A quiet day between two active days has no row
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-11-02T09:00:00Z"
    And the plan "pln-2" of seller "S-1" was confirmed at "2026-11-04T09:00:00Z"
    When I request the seller-inbound performance report from "2026-11-01" to "2026-11-07"
    Then the report has the days "2026-11-02, 2026-11-04"

  Scenario: A day with only a plan has counts and no latency
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-11-03T09:00:00Z"
    When I request the seller-inbound performance report from "2026-11-03" to "2026-11-03"
    Then the day "2026-11-03" has "inbound_plans_confirmed" 1
    And the day "2026-11-03" has "placement_advisories_issued" 0
    And the day "2026-11-03" has "shipments_received" 0
    And the day "2026-11-03" has "advisory_to_receipt_samples" 0
    And the day "2026-11-03" has "advisory_to_receipt_p50_seconds" null
    And the day "2026-11-03" has "advisory_to_receipt_p95_seconds" null
    And the day "2026-11-03" lists no received items

  Scenario: A busy day reports counts, received stock per item and advisory-to-receipt time
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-11-03T08:00:00Z"
    And the plan "pln-2" of seller "S-2" was confirmed at "2026-11-03T08:30:00Z"
    And seller "S-1" got a placement advisory for item "item-1" at "2026-11-03T06:00:00Z"
    And seller "S-2" got a placement advisory for item "item-2" at "2026-11-03T08:00:00Z"
    And the shipment "shp-1" of seller "S-1" was received at "2026-11-03T10:00:00Z" with these items:
      | gtin           | catalogItemId | qtyEaches |
      | 00614141000012 | item-1        | 10        |
      | 00614141000029 |               | 4         |
    And the shipment "shp-2" of seller "S-2" was received at "2026-11-03T09:00:00Z" with these items:
      | gtin           | catalogItemId | qtyEaches |
      | 00614141000036 | item-2        | 6         |
    When I request the seller-inbound performance report from "2026-11-01" to "2026-11-07"
    Then the report has the days "2026-11-03"
    And the day "2026-11-03" has "inbound_plans_confirmed" 2
    And the day "2026-11-03" has "placement_advisories_issued" 2
    And the day "2026-11-03" has "shipments_received" 2
    And the day "2026-11-03" has "received_eaches" 20
    And the day "2026-11-03" has "received_item_count" 3
    And the day "2026-11-03" lists the received GTINs "00614141000012, 00614141000036, 00614141000029"
    And the day "2026-11-03" has "advisory_to_receipt_samples" 2
    And the day "2026-11-03" has "advisory_to_receipt_p50_seconds" 9000
    And the day "2026-11-03" has "advisory_to_receipt_p95_seconds" 13860

  Scenario: A receipt without item lines still counts as a receipt
    Given the shipment "shp-1" of seller "S-1" was received at "2026-11-03T10:00:00Z" without item lines
    When I request the seller-inbound performance report from "2026-11-03" to "2026-11-03"
    Then the day "2026-11-03" has "shipments_received" 1
    And the day "2026-11-03" has "received_eaches" 0
    And the day "2026-11-03" lists no received items

  Scenario: A redelivered event and a message that is not a CloudEvent change nothing
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-11-03T09:00:00Z"
    And the last event is delivered again
    And a message that is not a CloudEvent is consumed
    When I request the seller-inbound performance report from "2026-11-03" to "2026-11-03"
    Then the day "2026-11-03" has "inbound_plans_confirmed" 1

  Scenario: The freshness is the newest event time the projection applied
    Given the plan "pln-1" of seller "S-1" was confirmed at "2026-11-03T09:00:00Z"
    When I request the report freshness
    Then the freshness as of is "2026-11-03T09:00:00Z"

  Scenario: An inverted range is a problem
    When I request the seller-inbound performance report from "2026-11-07" to "2026-11-01"
    Then the report status is 400
    And the report problem type is "invalid-report-range"
