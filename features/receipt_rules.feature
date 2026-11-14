# receipt_rules.feature: scenarios derived from the context documentation
# (use-cases: RecordInboundReceipt, policy ReceiveOnVendorReceipt; domain-events:
# InboundShipmentReceived, InboundDiscrepancyRecorded, SellerComplianceViolationRecorded;
# aggregate-design-canvas: the receipt edges of the InboundShipment state machine;
# ADR 0002 A8, ADR 0003, ADR 0014; the troubleshooting dead-letter rows).
# Each scenario names the rule it pins and asserts the facts the service published.
Feature: The receipt rules of the documentation

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped

  # --- one fact per difference, with the kind the vendor's status and counts give ------------

  Scenario: A MISSING carton is a MissingCarton discrepancy and closes the shipment with a discrepancy
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"MISSING","items":[],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED_WITH_DISCREPANCY"
    And the response field "cartons.1.receiptStatus" is "MISSING"
    And the outbox holds an InboundDiscrepancyRecorded of kind "MissingCarton" for carton 2
    And the outbox holds 1 "InboundDiscrepancyRecorded" event
    And the InboundShipmentReceived event has status "CLOSED_WITH_DISCREPANCY"
    And the InboundShipmentReceived event lists carton 2 as "MISSING"

  Scenario: A DAMAGED carton counted in full is one Damaged discrepancy and no quantity difference
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED_WITH_DISCREPANCY"
    And the outbox holds an InboundDiscrepancyRecorded of kind "Damaged" for carton 2
    And the outbox holds 1 "InboundDiscrepancyRecorded" event

  Scenario Outline: A counted quantity below or above the manifest is Short or Over, with expected and counted
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":<counted>}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED_WITH_DISCREPANCY"
    And the outbox holds an InboundDiscrepancyRecorded of kind "<kind>" for carton 2 expecting "00614141000036:12" and counting "00614141000036:<counted>"
    And the outbox holds 1 "InboundDiscrepancyRecorded" event

    Examples:
      | counted | kind  |
      | 11      | Short |
      | 13      | Over  |
      | 0       | Short |

  Scenario: A carton reported without any count is not compared with its manifest
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the outbox holds 0 "InboundDiscrepancyRecorded" events

  Scenario: A counted GTIN the manifest does not list is Over against zero
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12},{"gtin":"00614141000050","qtyEaches":3}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the outbox holds an InboundDiscrepancyRecorded of kind "Over" for carton 1 expecting "00614141000050:0" and counting "00614141000050:3"
    And the outbox holds 1 "InboundDiscrepancyRecorded" event

  # --- the carton the manifest does not list ----------------------------------------------------------

  Scenario: A counted GTIN the manifest does not list, counted at zero, is no difference
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12},{"gtin":"00614141000050","qtyEaches":0}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the outbox holds 0 "InboundDiscrepancyRecorded" events
    And the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 24        | item-1        |

  Scenario: An unlisted carton is recorded once with UNMANIFESTED_CARTON, however often it is reported
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":["PALLET_LABEL_MISSING"]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"
    And the outbox holds an InboundDiscrepancyRecorded of kind "UnexpectedCarton" for SSCC "006141420000000035"
    And the outbox holds a SellerComplianceViolationRecorded with code "UNMANIFESTED_CARTON" for SSCC "006141420000000035"
    And the outbox holds a SellerComplianceViolationRecorded with code "PALLET_LABEL_MISSING" for SSCC "006141420000000035"
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:31:00Z","cartons":[
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":["PALLET_LABEL_MISSING"]}]}
      """
    Then the outbox holds 1 "InboundDiscrepancyRecorded" event
    And the outbox holds 2 "SellerComplianceViolationRecorded" events

  Scenario: Receipts in three parts walk RECEIVING to CLOSED_WITH_DISCREPANCY and close the shipment once
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:31:00Z","cartons":[
        {"sscc":"006141420000000035","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"
    And the outbox holds 0 "InboundShipmentReceived" events
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:32:00Z","cartons":[
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED_WITH_DISCREPANCY"
    And the outbox holds 1 "InboundShipmentReceived" event
    And the InboundShipmentReceived event lists SSCC "006141420000000035" as "UNEXPECTED"

  Scenario: Receipts in two parts close a clean shipment, and nothing is received until it closes
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"
    And the response field "cartons.0.receiptStatus" is "RECEIVED"
    And the response field "cartons.1.receiptStatus" is absent
    And the response field "receivedAt" is absent
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:32:00Z","cartons":[
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the response field "receivedAt" is "2026-10-25T09:32:00Z"
    And the outbox holds 1 "InboundShipmentReceived" event

  Scenario: A discrepancy is published while the shipment is still RECEIVING
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "RECEIVING"
    And the outbox holds an InboundDiscrepancyRecorded of kind "Damaged" for carton 1
    And the outbox holds 0 "InboundShipmentReceived" events

  # --- first answer final, redelivery and late receipts ------------------------------------------------

  Scenario: A carton answered once keeps its first answer
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:31:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"MISSING","items":[],"complianceCodes":["LABEL_UNREADABLE"]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the response field "cartons.0.receiptStatus" is "RECEIVED"
    And the outbox holds 0 "InboundDiscrepancyRecorded" events
    And the outbox holds 0 "SellerComplianceViolationRecorded" events
    And the InboundShipmentReceived event lists carton 1 as "RECEIVED"

  Scenario: The same carton status reported under another event id is not recounted
    When the vendor reports this receipt of the shipment as event "ev-damaged-1":
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And the vendor reports this receipt of the shipment as event "ev-damaged-2":
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:05Z","cartons":[
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/sellers/S-1/scorecard"
    Then the outbox holds 1 "InboundDiscrepancyRecorded" event
    And the response field "discrepanciesByKind.Damaged" is "1"

  Scenario: A late receipt for a closed shipment changes nothing
    Given the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-26T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"MISSING","items":[],"complianceCodes":["LABEL_UNREADABLE"]},
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":[]}]}
      """
    Then the receipt is accepted
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundShipmentReceived"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"

  Scenario: A late receipt for a shipment closed with a discrepancy changes nothing either
    Given the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-26T09:30:00Z","cartons":[
        {"sscc":"006141420000000035","status":"UNEXPECTED","items":[],"complianceCodes":["LABEL_UNREADABLE"]}]}
      """
    Then the receipt is accepted
    And the outbox holds 1 "InboundDiscrepancyRecorded" event
    And the outbox holds 0 "SellerComplianceViolationRecorded" events
    And the outbox holds 1 "InboundShipmentReceived" event

  # --- receipts the service can never apply are dead-lettered and edit nothing ---------------------------

  Scenario: A receipt for a shipment that was never shipped is dead-lettered
    Given seller "S-1" has a confirmed case-packed plan for 12 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:3}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    Then the receipt is dead-lettered because "LABELLED"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "LABELLED"
    And the response field "cartons.0.receiptStatus" is absent

  Scenario: A receipt naming another node is dead-lettered and the shipment is not edited to match the dock
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-9","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    Then the receipt is dead-lettered because "is not the shipment's node"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "SHIPPED"
    And the response field "nodeId" is "n-1"

  Scenario Outline: A receipt with an unreadable carton is dead-lettered whole
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]},
        <carton>]}
      """
    Then the receipt is dead-lettered because "<reason>"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "SHIPPED"
    And the response field "cartons.0.receiptStatus" is absent
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped"

    Examples:
      | carton                                                                    | reason                                          |
      | {"sscc":"{sscc:2}","status":"LOST","items":[],"complianceCodes":[]}       | must be RECEIVED, MISSING, UNEXPECTED or DAMAGED |
      | {"sscc":"00614142000000001","status":"RECEIVED","items":[],"complianceCodes":[]} | cartons[1].sscc                           |
      | {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]}  | appears twice in the receipt                    |

  # --- compliance codes ------------------------------------------------------------------------------------

  Scenario: A compliance code repeated on a carton is one violation, and a violation alone is no discrepancy
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":["LABEL_UNREADABLE","LABEL_UNREADABLE"]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[],"complianceCodes":["LABEL_UNREADABLE","LABEL_UNREADABLE"]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the outbox holds 2 "SellerComplianceViolationRecorded" events
    And the outbox holds a SellerComplianceViolationRecorded with code "LABEL_UNREADABLE" for carton 1
    And the outbox holds a SellerComplianceViolationRecorded with code "LABEL_UNREADABLE" for carton 2
    And the first "SellerComplianceViolationRecorded" event has entity "seller" and subject "S-1"

  # --- ADR 0014: the stock a closed shipment received, per item ---------------------------------------------

  Scenario: The closing event reports the received eaches per item at the each-level GTIN
    Given offer-availability delivers CatalogItemPackagingDeclared with:
      """
      {"catalogItemId":"item-1","level":"EACH","gtin":"00614141000012","unitsPerPack":1,"declaredAt":"2026-10-24T12:00:00Z"}
      """
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000012 | 24        | item-1        |

  Scenario: A case GTIN the packaging view cannot resolve to an each is reported as it is
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 24        | item-1        |

  Scenario: A DAMAGED carton is not received stock
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"DAMAGED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 12        | item-1        |

  Scenario: The counted quantity replaces the manifest quantity in the received stock
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":10}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 22        | item-1        |

  Scenario: A carton reported without a count is received at its manifest quantity
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 24        | item-1        |

  Scenario: A shipment where nothing arrived closes with an empty received list
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"MISSING","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"MISSING","items":[],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event has status "CLOSED_WITH_DISCREPANCY"
    And the InboundShipmentReceived event reports no received lines

  Scenario: A counted GTIN the manifest does not list is received stock without an item
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12},{"gtin":"00614141000050","qtyEaches":3}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000036 | 24        | item-1        |
      | 00614141000050 | 3         |               |

  Scenario: A counted GTIN that only another carton's manifest lists takes that carton's item
    Given offer-availability delivers CatalogItemPackagingDeclared with:
      """
      {"catalogItemId":"item-1","level":"EACH","gtin":"00614141000012","unitsPerPack":1,"declaredAt":"2026-10-24T12:00:00Z"}
      """
    And offer-availability delivers CatalogItemPackagingDeclared with:
      """
      {"catalogItemId":"item-2","level":"EACH","gtin":"00614141000050","unitsPerPack":1,"declaredAt":"2026-10-24T12:00:00Z"}
      """
    And seller "S-1" has a confirmed individual plan to node "n-1" with lines:
      | item   | gtin           | qty |
      | item-1 | 00614141000012 | 5   |
      | item-2 | 00614141000050 | 3   |
    And the plan has an open shipment
    And the shipment has an individual carton of 5 eaches of "item-1" with GTIN "00614141000012"
    And the shipment has an individual carton of 3 eaches of "item-2" with GTIN "00614141000050"
    And the shipment is labelled and shipped
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:3}","status":"RECEIVED","items":[{"gtin":"00614141000012","qtyEaches":5},{"gtin":"00614141000050","qtyEaches":1}],"complianceCodes":[]},
        {"sscc":"{sscc:4}","status":"MISSING","items":[],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000012 | 5         | item-1        |
      | 00614141000050 | 1         | item-2        |

  Scenario: An item the packaging view has never heard of is received without its catalogItemId
    Given seller "S-1" has a confirmed individual plan for 5 eaches of "item-9" with GTIN "00614141000050" to node "n-1"
    And the plan has an open shipment
    And the shipment has an individual carton of 5 eaches of "item-9" with GTIN "00614141000050"
    And the shipment is labelled and shipped
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:3}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    Then the InboundShipmentReceived event reports the received lines:
      | gtin           | qtyEaches | catalogItemId |
      | 00614141000050 | 5         |               |

  # --- the scorecard rests on this service's own clock -----------------------------------------------------------

  Scenario: The scorecard dates a closed shipment by this service's clock, not by the vendor's receivedAt
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2020-01-01T00:00:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    And I send GET "/sellers/S-1/scorecard?window=7d"
    Then the response field "shipmentsReceived" is "1"
