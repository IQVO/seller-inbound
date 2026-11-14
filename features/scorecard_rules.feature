# scorecard_rules.feature: scenarios derived from the repo's ADR 0005 and
# docs/docs/ddd/use-cases.md (GetSellerScorecard), restated in the context's
# aggregate-design-canvas (read model SellerScorecard). The tier thresholds are
# pinned at their edges: the facts are seeded straight into the facts store at
# the instants the scenario names, because no receipt flow produces 100 closed
# shipments at the edge of a threshold one by one.
Feature: The seller scorecard thresholds of the documentation

  Background:
    Given seller "S-1" is onboarded

  Scenario Outline: The accuracy edge: strictly below 0.98 is WATCH, strictly below 0.90 is AT_RISK
    Given seller "S-1" has 100 closed shipments reporting 100 cartons of which <arrived> arrived
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "receiptAccuracyRate" is "<rate>"
    And the response field "complianceTier" is "<tier>"

    Examples:
      | arrived | rate | tier    |
      | 100     | 1    | GOOD    |
      | 98      | 0.98 | GOOD    |
      | 97      | 0.97 | WATCH   |
      | 90      | 0.9  | WATCH   |
      | 89      | 0.89 | AT_RISK |

  Scenario Outline: The discrepancy edge: at or above 0.5 per shipment is WATCH, at or above 2 is AT_RISK
    Given seller "S-1" has 100 closed shipments reporting 100 cartons of which 100 arrived
    And seller "S-1" has <discrepancies> discrepancies of kind "Short" recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "discrepancyRatePerShipment" is "<rate>"
    And the response field "complianceTier" is "<tier>"

    Examples:
      | discrepancies | rate | tier    |
      | 49            | 0.49 | GOOD    |
      | 50            | 0.5  | WATCH   |
      | 199           | 1.99 | WATCH   |
      | 200           | 2    | AT_RISK |

  Scenario Outline: The violation edge: one is WATCH, five is AT_RISK
    Given seller "S-1" has <violations> compliance violations recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "complianceViolations" is "<violations>"
    And the response field "complianceTier" is "<tier>"

    Examples:
      | violations | tier    |
      | 0          | GOOD    |
      | 1          | WATCH   |
      | 4          | WATCH   |
      | 5          | AT_RISK |

  Scenario: The tier is the worst of the three signals
    Given seller "S-1" has 10 closed shipments reporting 100 cartons of which 95 arrived
    And seller "S-1" has 5 compliance violations recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "receiptAccuracyRate" is "0.95"
    And the response field "complianceTier" is "AT_RISK"

  Scenario: Rates are rounded to four decimals
    Given seller "S-1" has 3 closed shipments reporting 3 cartons of which 2 arrived
    And seller "S-1" has 1 discrepancies of kind "MissingCarton" recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "receiptAccuracyRate" is "0.6667"
    And the response field "discrepancyRatePerShipment" is "0.3333"

  Scenario: A fact exactly one window old is still inside the window
    Given seller "S-1" has a compliance violation recorded exactly 7 days ago
    When I send GET "/sellers/S-1/scorecard?window=7d"
    Then the response field "complianceViolations" is "1"

  Scenario: A fact one second older than the window is outside it, and inside the 30 day window
    Given seller "S-1" has a compliance violation recorded 7 days and one second ago
    When I send GET "/sellers/S-1/scorecard?window=7d"
    Then the response field "complianceViolations" is "0"
    When I send GET "/sellers/S-1/scorecard?window=30d"
    Then the response field "complianceViolations" is "1"

  Scenario: The 30 day window also has an edge
    Given seller "S-1" has a compliance violation recorded 30 days and one second ago
    And seller "S-1" has a compliance violation recorded exactly 30 days ago
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "complianceViolations" is "1"

  Scenario: The scorecard counts the facts of its own seller only
    Given seller "S-2" has 5 compliance violations recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "complianceViolations" is "0"
    And the response field "complianceTier" is "GOOD"

  Scenario: An invalid window is refused before the seller is looked up
    When I send GET "/sellers/ghost/scorecard?window=90d"
    Then the response status is 400
    And the problem type is "invalid-window"

  Scenario: An AT_RISK tier suspends nothing and a read raises no event
    Given node "n-1" is an active fulfillment center
    And seller "S-1" has 5 compliance violations recorded now
    When I send GET "/sellers/S-1/scorecard"
    Then the response field "complianceTier" is "AT_RISK"
    When I send POST "/inbound-plans" with:
      """
      {"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}]}
      """
    Then the response status is 201
    And the outbox event types are "SellerOnboarded"
