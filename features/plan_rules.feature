# plan_rules.feature: scenarios derived from the context documentation
# (use-cases: CreateInboundPlan, ConfirmInboundPlan, CancelInboundPlan,
# CreateInboundShipment; aggregate-design-canvas: InboundPlan invariants and
# state machine; ADR 0002 A4 for the node view). Each scenario names its rule.
Feature: The inbound plan rules of the documentation

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  # --- CreateInboundPlan: the lines ------------------------------------------------

  Scenario Outline: A plan holds 1 to 500 lines
    When seller "S-1" submits an individual plan of <lines> distinct items to node "n-1"
    Then the response status is <status>

    Examples:
      | lines | status |
      | 0     | 422    |
      | 1     | 201    |
      | 500   | 201    |
      | 501   | 422    |

  Scenario: A plan names each catalogue item once
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[
        {"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5},
        {"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":7}]}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario Outline: A plan line carries from 1 to 2147483647 eaches
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":<qty>}]}
      """
    Then the response status is <status>

    Examples:
      | qty        | status |
      | -5         | 422    |
      | 0          | 422    |
      | 1          | 201    |
      | 2147483647 | 201    |
      | 2147483648 | 422    |

  Scenario Outline: A GTIN of 8, 12, 13 or 14 digits is stored as 14 digits
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"<sent>","qtyEaches":5}]}
      """
    Then the response status is 201
    And the response field "lines.0.gtin" is "<stored>"

    Examples:
      | sent           | stored         |
      | 73513537       | 00000073513537 |
      | 036000291452   | 00036000291452 |
      | 0614141000012  | 00614141000012 |
      | 00614141000012 | 00614141000012 |

  Scenario Outline: A GTIN of another length or a variable-measure GTIN is refused
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"<gtin>","qtyEaches":5}]}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

    Examples:
      | gtin           |
      | 61414100001    |
      | 94005000000011 |
      | 00614141000013 |

  Scenario: Confirming publishes the plan with its lines and normalised GTINs
    Given seller "S-1" has a draft individual plan for 24 eaches of "item-1" with GTIN "0614141000012" to node "n-1"
    When I send POST "/inbound-plans/{plan}/confirmations"
    Then the response status is 200
    And the InboundPlanConfirmed event carries seller "S-1", node "n-1", packing "INDIVIDUAL" and the lines:
      | item   | gtin           | qty |
      | item-1 | 00614141000012 | 24  |
    And the first "InboundPlanConfirmed" event has entity "inboundplan" and subject "{plan}"

  # --- the destination node is re-checked at every step -----------------------------

  Scenario: A draft plan cannot be confirmed once its node stopped receiving, and can be after it recovers
    Given seller "S-1" has a draft case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And node "n-1" is suspended by node-topology
    When I send POST "/inbound-plans/{plan}/confirmations"
    Then the response status is 409
    And the problem type is "node-cannot-receive"
    And the outbox event types are "SellerOnboarded"
    When node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-1"}
      """
    And I send POST "/inbound-plans/{plan}/confirmations"
    Then the response status is 200
    And the response field "state" is "CONFIRMED"

  Scenario: No shipment is opened once the plan's node stopped receiving
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And node "n-1" is suspended by node-topology
    When I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: An inbound cross-dock has the INBOUND capability
    Given node "n-3" is an active inbound cross-dock
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-3"
    Then the response status is 201

  Scenario: A node that was registered but never activated cannot receive
    Given node-topology delivers NodeRegistered with:
      """
      {"nodeId":"n-4","nodeType":"FULFILLMENT_CENTER"}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-4"
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: A retired node cannot receive
    Given node-topology delivers NodeRetired with:
      """
      {"nodeId":"n-1"}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-1"
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  # --- the plan state machine -----------------------------------------------------------

  Scenario: A cancelled plan stamps its cancellation and publishes nothing
    Given seller "S-1" has a draft individual plan for 5 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    When I send POST "/inbound-plans/{plan}/cancellations"
    Then the response status is 200
    And the response field "state" is "CANCELLED"
    And the response field "cancelledAt" is "2026-10-24T13:00:00Z"
    And the response field "confirmedAt" is absent
    And the outbox event types are "SellerOnboarded"

  Scenario: CANCELLED is terminal: no second cancellation and no shipment
    Given seller "S-1" has a draft individual plan for 5 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And I send POST "/inbound-plans/{plan}/cancellations"
    When I send POST "/inbound-plans/{plan}/cancellations"
    Then the response status is 409
    And the problem type is "illegal-transition"
    When I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 409
    And the problem type is "illegal-transition"

  Scenario: A draft plan has no shipment
    Given seller "S-1" has a draft individual plan for 5 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    When I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 409
    And the problem type is "illegal-transition"

  Scenario: A confirmed plan may have several shipments
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    When I send POST "/inbound-plans/{plan}/shipments"
    And I send POST "/inbound-plans/{plan}/shipments"
    Then the response status is 201
    When I send GET "/inbound-shipments/{shipment:1}"
    Then the response field "state" is "DRAFT"
    And the response field "planId" is "{plan}"
    When I send GET "/inbound-shipments/{shipment:2}"
    Then the response field "state" is "DRAFT"
    And the response field "planId" is "{plan}"

  Scenario Outline: A command on an unknown plan is not found
    When I send POST "/inbound-plans/pln-404/<action>"
    Then the response status is 404
    And the problem type is "not-found"

    Examples:
      | action        |
      | confirmations |
      | cancellations |
      | shipments     |
