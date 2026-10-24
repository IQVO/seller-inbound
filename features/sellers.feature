Feature: Seller accounts
  A seller account gates every write of the seller API. A suspended seller
  creates no plan, shipment or carton and ships nothing.

  Scenario: Onboarding a seller publishes SellerOnboarded
    When I send POST "/sellers" with:
      """
      {"sellerId":"S-1","name":"Acme Goods"}
      """
    Then the response status is 201
    And the response field "state" is "ACTIVE"
    And the outbox event types are "SellerOnboarded"

  Scenario: Onboarding the same seller again is refused
    Given seller "S-1" is onboarded
    When I send POST "/sellers" with:
      """
      {"sellerId":"S-1","name":"Acme Goods"}
      """
    Then the response status is 409
    And the problem type is "seller-exists"
    And the outbox event types are "SellerOnboarded"

  Scenario: An invalid seller id is a validation failure
    When I send POST "/sellers" with:
      """
      {"sellerId":"not valid","name":"Acme Goods"}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario: Unknown fields in a body are refused
    When I send POST "/sellers" with:
      """
      {"sellerId":"S-1","name":"Acme Goods","extra":true}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario: Suspending and reinstating a seller publishes one event each
    Given seller "S-1" is onboarded
    When I send POST "/sellers/S-1:suspend" with:
      """
      {"reason":"repeated label violations"}
      """
    Then the response status is 200
    And the response field "state" is "SUSPENDED"
    And the response field "suspendReason" is "repeated label violations"
    When I send POST "/sellers/S-1:reinstate"
    Then the response status is 200
    And the response field "state" is "ACTIVE"
    And the response field "suspendedAt" is absent
    And the outbox event types are "SellerOnboarded, SellerSuspended, SellerReinstated"

  Scenario: Suspending a suspended seller returns the account with no new event
    Given seller "S-1" is onboarded
    And seller "S-1" is suspended
    When I send POST "/sellers/S-1:suspend"
    Then the response status is 200
    And the response field "state" is "SUSPENDED"
    And the outbox event types are "SellerOnboarded, SellerSuspended"

  Scenario: Reinstating an active seller returns the account with no new event
    Given seller "S-1" is onboarded
    When I send POST "/sellers/S-1:reinstate"
    Then the response status is 200
    And the outbox event types are "SellerOnboarded"

  Scenario: An unknown seller or action is not found
    When I send POST "/sellers/S-404:suspend"
    Then the response status is 404
    And the problem type is "not-found"
    Given seller "S-1" is onboarded
    When I send POST "/sellers/S-1:explode"
    Then the response status is 404
    And the problem type is "not-found"

  Scenario: A suspended seller cannot create a plan
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And seller "S-1" is suspended
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 409
    And the problem type is "seller-suspended"
