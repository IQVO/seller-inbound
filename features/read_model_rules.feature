# read_model_rules.feature: scenarios derived from the context documentation
# (domain-events: the eight consumed events and their idempotency on the CloudEvents
# id; use-cases: the node and packaging views; ADR 0002 A4 order-tolerant views;
# ADR 0003). The views are fed only by events and must survive duplicates and
# out-of-order delivery; each scenario shows the effect on what a seller can do.
Feature: The local read models of the documentation

  Background:
    Given seller "S-1" is onboarded
    And item "item-1" has a case of 12 eaches with GTIN "00614141000036" and each GTIN "00614141000012"

  # --- NodeInboundView: order tolerance --------------------------------------------------------

  Scenario: A node activated before its registration can receive only once it is registered
    Given node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-7"}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-7"
    Then the response status is 409
    And the problem type is "node-cannot-receive"
    When node-topology delivers NodeRegistered with:
      """
      {"nodeId":"n-7","nodeType":"FULFILLMENT_CENTER"}
      """
    And seller "S-1" submits an individual plan of 1 distinct items to node "n-7"
    Then the response status is 201

  Scenario: A retired node stays retired when a later activation arrives
    Given node "n-1" is an active fulfillment center
    And node-topology delivers NodeRetired with:
      """
      {"nodeId":"n-1"}
      """
    And node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-1"}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-1"
    Then the response status is 409
    And the problem type is "node-cannot-receive"

  Scenario: A suspended node is receivable again once node-topology reactivates it
    Given node "n-1" is an active fulfillment center
    And node "n-1" is suspended by node-topology
    And node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-1"}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-1"
    Then the response status is 201

  Scenario: A redelivered CloudEvents id is applied once
    Given node "n-1" is an active fulfillment center
    And node-topology delivers NodeSuspended as event "ev-susp-1" with:
      """
      {"nodeId":"n-1"}
      """
    And node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-1"}
      """
    When node-topology delivers NodeSuspended as event "ev-susp-1" with:
      """
      {"nodeId":"n-1"}
      """
    And seller "S-1" submits an individual plan of 1 distinct items to node "n-1"
    Then the response status is 201

  Scenario: A profile for a node not yet registered keeps its limits but cannot receive yet
    Given node-topology delivers NodeOperationalProfileDeclared with:
      """
      {"nodeId":"n-8","profileRevision":1,"inbound":{"maxCartonWeightG":4000}}
      """
    When seller "S-1" submits an individual plan of 1 distinct items to node "n-8"
    Then the response status is 409
    And the problem type is "node-cannot-receive"
    Given node-topology delivers NodeRegistered with:
      """
      {"nodeId":"n-8","nodeType":"FULFILLMENT_CENTER"}
      """
    And node-topology delivers NodeActivated with:
      """
      {"nodeId":"n-8"}
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-8"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":4001,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-overweight"

  # --- NodeInboundView: profile revisions -------------------------------------------------------

  Scenario: A profile revision older than the one held does not replace the limits
    Given node "n-1" is an active fulfillment center
    And node-topology delivers NodeOperationalProfileDeclared with:
      """
      {"nodeId":"n-1","profileRevision":3,"inbound":{"maxCartonWeightG":3000}}
      """
    And node-topology delivers NodeOperationalProfileDeclared with:
      """
      {"nodeId":"n-1","profileRevision":2,"inbound":{"maxCartonWeightG":9000}}
      """
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":3001,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "carton-overweight"

  Scenario: A newer profile revision replaces the limits for the cartons added afterwards
    Given node "n-1" is an active fulfillment center that accepts cartons up to 4000 grams
    And seller "S-1" has a confirmed individual plan for 20 eaches of "item-1" with GTIN "00614141000012" to node "n-1"
    And the plan has an open shipment
    And node-topology delivers NodeOperationalProfileDeclared with:
      """
      {"nodeId":"n-1","profileRevision":2,"inbound":{"maxCartonWeightG":6000}}
      """
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"contents":[{"catalogItemId":"item-1","gtin":"00614141000012","qtyEaches":5}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 201

  # --- CatalogPackagingView -------------------------------------------------------------------------

  Scenario: A later declaration of the same case level replaces its unitsPerPack
    Given node "n-1" is an active fulfillment center
    And seller "S-1" has a confirmed case-packed plan for 24 eaches of "item-1" with GTIN "00614141000036" to node "n-1"
    And the plan has an open shipment
    And item "item-1" has a case of 6 eaches with GTIN "00614141000036" and each GTIN "00614141000012"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}
      """
    Then the response status is 422
    And the problem type is "case-qty-mismatches-gtin"
    When I send POST "/inbound-shipments/{shipment}/cartons" with:
      """
      {"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":6}],"grossWeightG":2600,"dimsMm":{"l":300,"w":200,"h":150}}
      """
    Then the response status is 201
