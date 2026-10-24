Feature: Vendor receipts close the shipment
  The vendor reports what its dock received per carton. The shipment closes
  when every manifest carton has an answer; differences are recorded as facts
  and the manifest is never edited.

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped

  Scenario: A clean receipt closes the shipment
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the response field "cartons.0.receiptStatus" is "RECEIVED"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundShipmentReceived"

  Scenario: A damaged and short carton closes the shipment with a discrepancy
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":10}],"complianceCodes":["CARTON_CRUSHED"]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED_WITH_DISCREPANCY"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundDiscrepancyRecorded, InboundDiscrepancyRecorded, SellerComplianceViolationRecorded, InboundShipmentReceived"

  Scenario: A partial receipt leaves the shipment receiving
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"

  Scenario: A carton the manifest does not list is unexpected and a compliance violation
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":[]}]}
      """
    Then the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundDiscrepancyRecorded, SellerComplianceViolationRecorded"

  Scenario: The same receipt event delivered twice changes nothing
    When the same receipt event is delivered twice:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundShipmentReceived"

  Scenario: A receipt for a shipment this service does not know is undeliverable
    When the vendor reports a receipt for the unknown shipment "shp-404"
    Then the receipt is dead-lettered as undeliverable
