Feature: Destination node limits
  The inbound limits a node declared in its operational profile bound every carton.

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center that accepts cartons up to 4000 grams
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment

  Scenario: A carton heavier than the node accepts is refused
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":4001,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-overweight"

  Scenario: A carton at the limit is accepted
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":4000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
