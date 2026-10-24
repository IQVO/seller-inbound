Feature: Inbound shipments, cartons and labels
  A shipment packs a confirmed plan into SSCC-identified cartons, is labelled
  once the cartons add up to the plan and then ships. Quantities are eaches.

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment

  Scenario: The network issues an SSCC when the seller supplies none
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "ssccIssued" is "true"
    And the response field "label.gs1128" is "(00){sscc:1}"
    And the response field "label.totalEaches" is "12"
    And the response field "label.destinationNodeId" is "n-1"

  Scenario: A valid seller-supplied SSCC is accepted as is
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141420000000011","kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "ssccIssued" is "false"
    And the response field "carton.sscc" is "006141420000000011"

  Scenario: An SSCC with a wrong check digit is refused
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141420000000010","kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "invalid-sscc"

  Scenario: An SSCC already used is refused
    Given the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"{sscc:1}","kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "sscc-not-unique"

  Scenario: A case carton must hold the quantity its case GTIN declares
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":10}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: A case carton holds one item only
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12},{"catalogItemId":"item-2","gtin":"00614141000012","qtyEaches":1}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-pack-not-uniform"

  Scenario: A shipment is labelled only when the cartons add up to the plan
    Given the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}:label"
    Then the response status is 422
    And the problem type is "carton-contents-mismatch-plan"

  Scenario: A shipment cannot ship before it is labelled
    Given the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}:ship"
    Then the response status is 409
    And the problem type is "shipment-not-labelled"

  Scenario: Label, read the label data and ship
    Given the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}:label"
    Then the response status is 200
    And the response field "state" is "LABELLED"
    When I send GET "/inbound-shipments/{shipment}/labels"
    Then the response status is 200
    And the response lists 2 items in "labels"
    And the response field "labels.0.gs1128" is "(00){sscc:1}"
    And the response field "labels.1.gs1128" is "(00){sscc:2}"
    And the response field "labels.1.contentSummary" is "12 eaches: item-1 x12"
    And the response field "destinationNodeId" is "n-1"
    When I send POST "/inbound-shipments/{shipment}:ship"
    Then the response status is 200
    And the response field "state" is "SHIPPED"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped"

  Scenario: Labelling a labelled shipment is an illegal transition and publishes nothing more
    Given the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}:label"
    And I send POST "/inbound-shipments/{shipment}:label"
    Then the response status is 409
    And the problem type is "illegal-transition"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed, InboundShipmentLabelled"

  Scenario: A labelled shipment takes no more cartons
    Given the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}:label"
    And I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "illegal-transition"

  Scenario: A suspended seller ships nothing
    Given the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And seller "S-1" is suspended
    When I send POST "/inbound-shipments/{shipment}:label"
    Then the response status is 409
    And the problem type is "seller-suspended"

  Scenario: An unknown shipment is not found
    When I send GET "/inbound-shipments/shp-404/labels"
    Then the response status is 404
    And the problem type is "not-found"
