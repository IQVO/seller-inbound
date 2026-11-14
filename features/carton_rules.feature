# carton_rules.feature: scenarios derived from the context documentation
# (use-cases: AddCarton; aggregate-design-canvas: the InboundShipment and Carton
# invariants; business-context decisions 2 to 4; ADR 0002 A5, A6, A9; the
# troubleshooting problem table). Each scenario names the rule it pins.
Feature: The carton rules of the documentation

  Background:
    Given seller "S-1" is onboarded
    And node "n-1" is an active fulfillment center
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  # --- the case level of the packaging view -------------------------------------------

  Scenario Outline: A case carton one each away from the unitsPerPack is refused
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":<qty>}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

    Examples:
      | qty |
      | 11  |
      | 13  |

  Scenario: A case carton must carry the GTIN of an active CASE level, not the each GTIN
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: A retired CASE level no longer validates a case carton
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And offer-availability delivers CatalogItemPackLevelRetired with:
      """
      {"catalogItemId":"item-1","level":"CASE","gtin":"00614141000036","retiredAt":"2026-10-24T12:30:00Z"}
      """
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: A retired GTIN is never reactivated by a later declaration
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And offer-availability delivers CatalogItemPackLevelRetired with:
      """
      {"catalogItemId":"item-1","level":"CASE","gtin":"00614141000036","retiredAt":"2026-10-24T12:30:00Z"}
      """
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: A level retired before it was declared stays retired
    Given offer-availability delivers CatalogItemPackLevelRetired with:
      """
      {"catalogItemId":"item-2","level":"CASE","gtin":"00614141000043","retiredAt":"2026-10-24T12:30:00Z"}
      """
    And item "item-2" has a case of 24 eaches with GTIN "00614141000043" and each GTIN "00614141000050"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-2" with GTIN "00614141000043" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-2","gtin":"00614141000043","qtyEaches":24}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: A level declared with a single unit per pack is not a case
    Given item "item-2" has a case of 1 eaches with GTIN "00614141000043" and each GTIN "00614141000050"
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-2" with GTIN "00614141000043" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-2","gtin":"00614141000043","qtyEaches":1}],"grossWeightG":900,"dimsMm":{"l":200,"w":150,"h":100}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  Scenario: An item whose packaging was never declared cannot be case-packed
    # Both documentation readings agree on this refusal; whether the PLAN itself
    # should have been refused earlier is a documentation contradiction (see the PR).
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-9" with GTIN "00614141000050" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-9","gtin":"00614141000050","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"

  # --- one item at one quantity per case carton ----------------------------------------

  Scenario: A case-packed plan refuses a MIXED carton even with a single item
    Given seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"MIXED","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-pack-not-uniform"

  Scenario: An item is packed at one quantity per carton
    Given seller "S-1" has a confirmed case-packed plan for 48 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And the shipment has 1 case carton of 12 eaches of "item-1" with GTIN "00614141000036"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":24}],"grossWeightG":9000,"dimsMm":{"l":500,"w":400,"h":300}}
      """
    Then the response status is 422
    And the problem type is "case-pack-not-uniform"

  Scenario Outline: An individual plan packs mixed cartons with no pack GTIN
    Given seller "S-1" has a confirmed individual plan to node "n-1" with lines:
      | item   | gtin           | qty |
      | item-1 | 00614141000012 | 5   |
      | item-2 | 00614141000050 | 3   |
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"<kind>","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5},{"catalogItemId":"item-2","gtin":"00614141000050","qtyEaches":3}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "carton.kind" is "MIXED"
    And the response field "label.totalEaches" is "8"
    And the response field "label.contentSummary" is "8 eaches: item-1 x5, item-2 x3"

    Examples:
      | kind  |
      |       |
      | MIXED |

  # --- what the plan allows ---------------------------------------------------------------

  Scenario: A carton may only hold items of its plan
    Given seller "S-1" has a confirmed individual plan for 10 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-2","gtin":"00614141000050","qtyEaches":1}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-contents-mismatch-plan"

  Scenario: The cartons may fill the plan line exactly and not one each more
    Given seller "S-1" has a confirmed individual plan for 10 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And the shipment has an individual carton of 10 eaches of "item-1" with GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":1}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-contents-mismatch-plan"

  Scenario: The plan line is shared by all the plan's shipments
    Given seller "S-1" has a confirmed individual plan for 24 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And the shipment has an individual carton of 12 eaches of "item-1" with GTIN "00614141000012"
    And the plan has an open shipment
    And the shipment has an individual carton of 12 eaches of "item-1" with GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":1}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-contents-mismatch-plan"

  # --- carton contents: lot, expiry, weight, size, count ------------------------------------

  Scenario Outline: A carton content has a lot up to 20 characters, an ISO expiry and positive measures
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5,"lot":"<lot>","expiry":"<expiry>"}],"grossWeightG":<weight>,"dimsMm":{"l":<length>,"w":300,"h":200}}
      """
    Then the response status is <status>

    Examples:
      | lot            | expiry     | weight | length | status |
      | {repeat:L:20}  | 2028-09-30 | 3000   | 400    | 201    |
      | {repeat:L:21}  | 2028-09-30 | 3000   | 400    | 422    |
      | L2209          | 30/09/2028 | 3000   | 400    | 422    |
      | L2209          | 2028-02-30 | 3000   | 400    | 422    |
      | L2209          | 2028-09-30 | 0      | 400    | 422    |
      | L2209          | 2028-09-30 | 3000   | 0      | 422    |

  Scenario Outline: A carton holds 1 to 100 content lines
    Given seller "S-1" has a confirmed individual plan of 101 distinct items of 1 eaches each to node "n-1"
    And the plan has an open shipment
    When I add a mixed carton holding the first <items> items of the plan
    Then the response status is <status>

    Examples:
      | items | status |
      | 100   | 201    |
      | 101   | 422    |

  @slow
  Scenario: A shipment holds at most 1000 cartons
    Given seller "S-1" has a confirmed individual plan for 1001 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And the shipment has 1000 individual cartons of 1 each of "item-1" with GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":1}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  # --- the node's declared limits ------------------------------------------------------------

  Scenario Outline: A carton fits the node's maximum size in some orientation
    Given node "n-5" is an active fulfillment center with the inbound limits:
      """
      {"maxCartonDimsMm":{"l":600,"w":400,"h":300}}
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-5"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":<l>,"w":<w>,"h":<h>}}
      """
    Then the response status is <status>

    Examples:
      | l   | w   | h   | status |
      | 600 | 400 | 300 | 201    |
      | 300 | 600 | 400 | 201    |
      | 601 | 400 | 300 | 422    |
      | 500 | 500 | 100 | 422    |
      | 400 | 300 | 301 | 201    |
      | 500 | 301 | 400 | 422    |

  Scenario: An oversize carton is refused with its own problem type
    Given node "n-5" is an active fulfillment center with the inbound limits:
      """
      {"maxCartonDimsMm":{"l":600,"w":400,"h":300}}
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-5"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":601,"w":400,"h":300}}
      """
    Then the response status is 422
    And the problem type is "carton-oversize"

  Scenario: A node that declared no inbound limits receives any carton
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":90000,"dimsMm":{"l":2000,"w":1000,"h":1000}}
      """
    Then the response status is 201

  Scenario Outline: A carton on a pallet needs a node that does not forbid pallets
    Given node "n-4" is an active fulfillment center with the inbound limits:
      """
      <limits>
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-4"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"parentSscc":"006141420000000011","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is <status>

    Examples:
      | limits                    | status |
      | {"acceptsPallets":false}  | 409    |
      | {"acceptsPallets":true}   | 201    |
      | {"maxCartonWeightG":9000} | 201    |

  Scenario: A carton on a refused pallet is answered node-cannot-receive
    Given node "n-4" is an active fulfillment center with the inbound limits:
      """
      {"acceptsPallets":false}
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-4"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"parentSscc":"006141420000000011","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the problem type is "node-cannot-receive"

  Scenario: The parent SSCC is echoed on the carton and its label
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"parentSscc":"006141420000000011","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "carton.parentSscc" is "006141420000000011"
    And the response field "label.parentSscc" is "006141420000000011"

  Scenario: A parent SSCC differs from the carton's own SSCC
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141420000000011","parentSscc":"006141420000000011","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "validation-failed"

  Scenario: A malformed parent SSCC is an invalid SSCC
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"parentSscc":"006141420000000010","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "invalid-sscc"

  # --- SSCC: shape, uniqueness, issuance --------------------------------------------------------

  Scenario Outline: A supplied SSCC is exactly 18 digits with a valid check digit
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"<sscc>","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is <status>

    Examples:
      | sscc                | status |
      | 006141420000000028  | 201    |
      | 00614142000000001   | 422    |
      | 0061414200000000281 | 422    |
      | 00614142000000002A  | 422    |

  Scenario: An SSCC is unique network-wide, across the shipments of a plan
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141420000000028","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141420000000028","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 409
    And the problem type is "sscc-not-unique"

  Scenario: An issued SSCC that collides with one the seller supplied elsewhere is issued again
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141410000000012","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "ssccIssued" is "true"
    And the response field "carton.sscc" is "006141410000000029"

  # ADR 0002 A5 and use-cases (AddCarton): an issued SSCC that collides is issued
  # again. The collision with an SSCC supplied in ANOTHER shipment is re-issued
  # (above); one supplied in the SAME shipment is refused by the aggregate before
  # the retry loop sees it: the seller gets sscc-not-unique for an SSCC it never sent.
  @bdd @known-bug
  Scenario: An issued SSCC that collides with one the seller supplied in the same shipment is issued again
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"sscc":"006141410000000012","contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "ssccIssued" is "true"
    And the response field "carton.sscc" is "006141410000000029"

  Scenario: Each issued SSCC is a new one
    Given seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And the shipment has an individual carton of 5 eaches of "item-1" with GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201
    And the response field "carton.sscc" is "006141410000000029"
