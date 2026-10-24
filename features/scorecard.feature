Feature: Seller compliance scorecard
  A seller's rolling-window scorecard is computed from the facts this service
  recorded when it applied the vendor's receipts. It is informational: the
  derived complianceTier never suspends or blocks the seller.

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped

  Scenario: A seller with no closed shipment has no rates and a GOOD tier
    When I send GET "/sellers/S-1/scorecard"
    Then the response status is 200
    And the response field "window" is "30d"
    And the response field "shipmentsReceived" is "0"
    And the response field "receiptAccuracyRate" is absent
    And the response field "discrepancyRatePerShipment" is absent
    And the response field "discrepanciesByKind" is "{}"
    And the response field "complianceViolations" is "0"
    And the response field "complianceTier" is "GOOD"

  Scenario: A clean receipt gives a perfect accuracy and a GOOD tier
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/sellers/S-1/scorecard?window=7d"
    Then the response field "window" is "7d"
    And the response field "shipmentsReceived" is "1"
    And the response field "cartonsReported" is "2"
    And the response field "cartonsReceived" is "2"
    And the response field "receiptAccuracyRate" is "1"
    And the response field "discrepancyRatePerShipment" is "0"
    And the response field "complianceTier" is "GOOD"

  Scenario: A damaged and short carton with a violation makes the seller AT_RISK without suspending it
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":10}],"complianceCodes":["CARTON_CRUSHED"]}]}
      """
    And I send GET "/sellers/S-1/scorecard"
    Then the response field "cartonsReported" is "2"
    And the response field "cartonsReceived" is "1"
    And the response field "receiptAccuracyRate" is "0.5"
    And the response field "discrepancyRatePerShipment" is "2"
    And the response field "discrepanciesByKind.Damaged" is "1"
    And the response field "discrepanciesByKind.Short" is "1"
    And the response field "complianceViolations" is "1"
    And the response field "complianceTier" is "AT_RISK"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundDiscrepancyRecorded, InboundDiscrepancyRecorded, SellerComplianceViolationRecorded, InboundShipmentReceived"

  Scenario: An unlisted carton on a partial receipt counts at once and makes the seller WATCH
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":[]}]}
      """
    And I send GET "/sellers/S-1/scorecard"
    Then the response field "shipmentsReceived" is "0"
    And the response field "receiptAccuracyRate" is absent
    And the response field "discrepanciesByKind.UnexpectedCarton" is "1"
    And the response field "complianceViolations" is "1"
    And the response field "complianceTier" is "WATCH"

  Scenario: The 7 day window forgets what the 30 day window still remembers
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"MISSING","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And the service clock advances 8 days
    And I send GET "/sellers/S-1/scorecard?window=7d"
    Then the response field "shipmentsReceived" is "0"
    And the response field "complianceTier" is "GOOD"
    When I send GET "/sellers/S-1/scorecard?window=30d"
    Then the response field "shipmentsReceived" is "1"
    And the response field "discrepanciesByKind.MissingCarton" is "1"
    And the response field "complianceTier" is "AT_RISK"

  Scenario: A window other than 7d or 30d is refused
    When I send GET "/sellers/S-1/scorecard?window=90d"
    Then the response status is 400
    And the problem type is "invalid-window"

  Scenario: An unknown seller has no scorecard
    When I send GET "/sellers/ghost/scorecard"
    Then the response status is 404
    And the problem type is "not-found"

  Scenario: A suspended seller still has a scorecard
    When seller "S-1" is suspended
    And I send GET "/sellers/S-1/scorecard"
    Then the response status is 200
    And the response field "complianceTier" is "GOOD"
