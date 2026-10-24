Feature: Inbound plans
  A seller declares what it will send to one destination node, in eaches. The
  node must be able to receive; a confirmed plan is the basis of its shipments.

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  Scenario: A plan is created as a draft and confirmed
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"CASE_PACKED","lines":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":24}]}
      """
    Then the response status is 201
    And the response field "state" is "DRAFT"
    And the response field "lines.0.qtyEaches" is "24"
    When I send POST "/inbound-plans/{plan}:confirm"
    Then the response status is 200
    And the response field "state" is "CONFIRMED"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed"

  Scenario: Confirming a confirmed plan is an illegal transition and publishes nothing more
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    When I send POST "/inbound-plans/{plan}:confirm"
    Then the response status is 409
    And the problem type is "illegal-transition"
    And the outbox event types are "SellerOnboarded, InboundPlanConfirmed"

  Scenario: A confirmed plan cannot be cancelled
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    When I send POST "/inbound-plans/{plan}:cancel"
    Then the response status is 409
    And the problem type is "illegal-transition"

  Scenario: A draft plan can be cancelled and then never confirmed
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    And I send POST "/inbound-plans/{plan}:cancel"
    Then the response status is 200
    And the response field "state" is "CANCELLED"
    When I send POST "/inbound-plans/{plan}:confirm"
    Then the response status is 409
    And the problem type is "illegal-transition"

  Scenario: An unknown destination node cannot receive
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-404","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: A node without the inbound capability cannot receive
    Given node "n-2" is an active sortation center
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-2","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: A suspended node cannot receive
    Given node "n-1" is suspended by node-topology
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: A GTIN with a wrong check digit is a validation failure
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000013","qtyEaches":5}]}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario: An unknown plan is not found
    When I send GET "/inbound-plans/pln-404"
    Then the response status is 404
    And the problem type is "not-found"
