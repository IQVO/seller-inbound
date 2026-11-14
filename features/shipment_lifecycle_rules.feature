# shipment_lifecycle_rules.feature: scenarios derived from the context documentation
# (use-cases: CreateInboundShipment, LabelShipment, ShipInboundShipment, GetLabels;
# aggregate-design-canvas: the InboundShipment state machine and invariants;
# business-context decisions 5 and 6; ADR 0002 A3 and A10). Each scenario names its rule.
Feature: The shipment lifecycle rules of the documentation

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  # --- LabelShipment ---------------------------------------------------------------------

  Scenario: A shipment with no carton cannot be labelled
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/labels"
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario: A plan line with no carton at all keeps the shipment from being labelled
    Given seller "S-1" has a confirmed individual plan to node "n-1" with lines:
      | item   | gtin           | qty |
      | item-1 | 00614141000012 | 5   |
      | item-2 | 00614141000050 | 3   |
    And the plan has an open shipment
    And the shipment has an individual carton of 5 eaches of "item-1" with GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/labels"
    Then the response status is 422
    And the problem type is "carton-contents-mismatch-plan"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "DRAFT"

  Scenario: Labelling seals every carton and publishes the manifest with its lot and expiry
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12,"lot":"L2209","expiry":"2028-09-30"}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    And the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}/labels"
    Then the response status is 200
    And the response field "labelledAt" is "2026-10-24T13:00:00Z"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "cartons.0.sealed" is "true"
    And the response field "cartons.1.sealed" is "true"
    And the InboundShipmentLabelled event carries seller "S-1" and node "n-1" for this shipment and 2 cartons
    And the manifest in the InboundShipmentLabelled event lists lot "L2209" expiring "2028-09-30" on carton 1
    And the first "InboundShipmentLabelled" event has entity "inboundshipment" and subject "{shipment}"

  Scenario: Cartons are not sealed while the shipment is a draft
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "DRAFT"
    And the response field "cartons.0.sealed" is "false"
    And the response field "labelledAt" is absent

  Scenario: The label data of a carton is available before the shipment is labelled
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send GET "/inbound-shipments/{shipment}/labels"
    Then the response status is 200
    And the response field "state" is "DRAFT"
    And the response field "labels.0.labelRef" is "LBL-{sscc:1}"
    And the response field "labels.0.gs1128" is "(00){sscc:1}"
    And the response field "labels.0.humanReadable" is "(00) 0 0614 1410 0000 0001 2"

  Scenario: The shipment names its seller as the owner of the stock
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    When I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 201
    And the response field "sellerId" is "S-1"
    And the response field "nodeId" is "n-1"
    And the response field "planId" is "{plan}"

  # --- the node and the seller are re-checked along the way -------------------------------------

  Scenario: A carton is refused once the node stopped receiving
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And node "n-1" is suspended by node-topology
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: Labelling is refused once the node stopped receiving and the shipment stays a draft
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And node "n-1" is suspended by node-topology
    When I send POST "/inbound-shipments/{shipment}/labels"
    Then the response status is 409
    And the problem type is "node-cannot-receive"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "DRAFT"
    And the outbox holds 0 "InboundShipmentLabelled" events

  Scenario: Dispatch is refused once the node stopped receiving and the shipment stays LABELLED
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled
    And node "n-1" is suspended by node-topology
    When I send POST "/inbound-shipments/{shipment}/dispatches"
    Then the response status is 409
    And the problem type is "node-cannot-receive"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "LABELLED"
    And the outbox holds 0 "InboundShipmentShipped" events

  # --- every refused edge of the state machine -----------------------------------------------

  Scenario: A SHIPPED shipment takes no carton, no second label and no second dispatch
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "illegal-transition"
    When I send POST "/inbound-shipments/{shipment}/labels"
    Then the response status is 409
    And the problem type is "illegal-transition"
    When I send POST "/inbound-shipments/{shipment}/dispatches"
    Then the response status is 409
    And the problem type is "illegal-transition"
    And the outbox holds 1 "InboundShipmentShipped" event

  Scenario: A closed shipment takes no dispatch either
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped
    And the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[],"complianceCodes":[]}]}
      """
    When I send POST "/inbound-shipments/{shipment}/dispatches"
    Then the response status is 409
    And the problem type is "illegal-transition"
    And the outbox holds 1 "InboundShipmentShipped" event

  Scenario Outline: A command on an unknown shipment is not found
    When I send POST "/inbound-shipments/shp-404/<action>"
    Then the response status is 404
    And the problem type is "not-found"

    Examples:
      | action     |
      | labels     |
      | dispatches |

  Scenario: An unknown shipment is not found
    When I send GET "/inbound-shipments/shp-404"
    Then the response status is 404
    And the problem type is "not-found"
