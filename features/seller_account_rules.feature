# seller_account_rules.feature: scenarios derived from the context documentation
# (warehouse-docs network/docs/contexts/seller-inbound: business-context, use-cases,
# aggregate-design-canvas; the repo's docs/docs/ddd/use-cases.md and ADR 0002).
# Each scenario names the rule it pins.
Feature: The seller account rules of the documentation
  A seller is onboarded under an ownerRef-shaped sellerId; a suspended seller
  creates no plan, shipment or carton and ships nothing, but its shipments
  already on the road still close.

  Background:
    Given node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  # --- OnboardSeller: the ownerRef and the name --------------------------------

  Scenario Outline: A sellerId is an ownerRef of at most 64 characters
    When I send POST "/sellers" with:
      """
      {"sellerId":"{repeat:s:<length>}","name":"Acme Goods"}
      """
    Then the response status is <status>

    Examples:
      | length | status |
      | 64     | 201    |
      | 65     | 422    |

  Scenario Outline: A sellerId uses only letters, digits, dot, underscore and hyphen
    When I send POST "/sellers" with:
      """
      {"sellerId":"<sellerId>","name":"Acme Goods"}
      """
    Then the response status is <status>

    Examples:
      | sellerId       | status |
      | acme.goods_1-b | 201    |
      | acme/goods     | 422    |

  Scenario Outline: A seller name is non-blank, trimmed and at most 120 characters
    When I send POST "/sellers" with:
      """
      {"sellerId":"S-1","name":"<name>"}
      """
    Then the response status is <status>

    Examples:
      | name             | status |
      | {repeat:n:120}   | 201    |
      | {repeat:n:121}   | 422    |
      | \u0020Acme Goods | 422    |
      |                  | 422    |

  Scenario: Onboarding publishes SellerOnboarded keyed by the seller
    When I send POST "/sellers" with:
      """
      {"sellerId":"S-1","name":"Acme Goods"}
      """
    Then the first "SellerOnboarded" event has entity "seller" and subject "S-1"

  # --- SuspendSeller / ReinstateSeller -------------------------------------------

  Scenario Outline: A suspension reason is at most 200 characters
    Given seller "S-1" is onboarded
    When I send POST "/sellers/S-1/suspensions" with:
      """
      {"reason":"{repeat:r:<length>}"}
      """
    Then the response status is <status>
    And the outbox holds <events> "SellerSuspended" event

    Examples:
      | length | status | events |
      | 200    | 200    | 1      |
      | 201    | 422    | 0      |

  Scenario: Suspending a seller publishes SellerSuspended keyed by the seller
    Given seller "S-1" is onboarded
    When seller "S-1" is suspended
    Then the first "SellerSuspended" event has entity "seller" and subject "S-1"

  Scenario: An unknown seller cannot be reinstated
    When I send POST "/sellers/S-404/reinstatements"
    Then the response status is 404
    And the problem type is "not-found"

  Scenario: A suspended seller cannot confirm a draft plan
    Given seller "S-1" is onboarded
    And seller "S-1" has a draft case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And seller "S-1" is suspended
    When I send POST "/inbound-plans/{plan}/confirmations"
    Then the response status is 409
    And the problem type is "seller-suspended"
    And the outbox event types are "SellerOnboarded, SellerSuspended"

  Scenario: A suspended seller cannot open a shipment on its confirmed plan
    Given seller "S-1" is onboarded
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And seller "S-1" is suspended
    When I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 409
    And the problem type is "seller-suspended"

  Scenario: A suspended seller cannot add a carton
    Given seller "S-1" is onboarded
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And seller "S-1" is suspended
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "seller-suspended"

  Scenario: A seller suspended after labelling cannot dispatch and the shipment stays LABELLED
    Given seller "S-1" is onboarded
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled
    And seller "S-1" is suspended
    When I send POST "/inbound-shipments/{shipment}/dispatches"
    Then the response status is 409
    And the problem type is "seller-suspended"
    When I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "LABELLED"
    And the outbox holds 0 "InboundShipmentShipped" events

  Scenario: A shipment already SHIPPED still closes when its seller is suspended
    Given seller "S-1" is onboarded
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 2 case cartons of 12 eaches of "item-1" with GTIN "00614141000036"
    And the shipment is labelled and shipped
    And seller "S-1" is suspended
    When the vendor reports this receipt of the shipment:
      """
      {"shipmentId":"{shipment}","nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[
        {"sscc":"{sscc:1}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]},
        {"sscc":"{sscc:2}","status":"RECEIVED","items":[{"gtin":"00614141000036","qtyEaches":12}],"complianceCodes":[]}]}
      """
    And I send GET "/inbound-shipments/{shipment}"
    Then the response field "state" is "CLOSED"
    And the outbox holds 1 "InboundShipmentReceived" event

  Scenario: A suspended seller may still cancel a draft plan
    Given seller "S-1" is onboarded
    And seller "S-1" has a draft individual plan for 5 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And seller "S-1" is suspended
    When I send POST "/inbound-plans/{plan}/cancellations"
    Then the response status is 200
    And the response field "state" is "CANCELLED"

  Scenario: Reinstating a suspended seller lifts every refusal
    Given seller "S-1" is onboarded
    And seller "S-1" is suspended
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the problem type is "seller-suspended"
    When I send POST "/sellers/S-1/reinstatements"
    And I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 201
    And the response field "state" is "DRAFT"
