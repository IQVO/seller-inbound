# ADR 0018: every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-20). Adopts the fleet decision of 2026-11-18 (`.claude/rules/fleet/idempotency-and-outbox.md`;
standard and reference test in warehouse-harness-template, `docs/idempotency-replay-classes.md`). It supersedes only
the sentences that say every 4xx answer is stored and replayed: the Idempotency bullet of
[ADR 0010](./0010-fleet-rest-api-guideline.md), the matching sentences of `apis/openapi.yaml`, the README and the
rules. Their Decision text is untouched and carries a dated pointer here.

## Context

The middleware stored every answer below 500 under the key for 24 hours. A refusal that a retry could pass was
therefore pinned: a `409 concurrent-update` (the optimistic-lock loser), a `404 not-found` for a seller that the
caller onboards a moment later, a `409 node-cannot-receive` before the node's facts arrived, a `409 seller-suspended`
until the reinstatement, a `409 shipment-not-labelled` until the last label: each replayed for a day to a client that
did the right thing and retried. Classifying by status cannot fix it (`409` is both `concurrent-update` and
`idempotency-key-reused`).

Phase-0 facts of this service (read from the code at develop `5e32428`, 2026-11-20):

- **Store (design A, a savepoint).** `RequireIdempotencyKey(pool, bind)` (`internal/adapters/inbound/http/idempotency.go`):
  `pool.Begin` (:88), the expired-key `DELETE ... created_at < now() - TTL` (:94, so a key older than 24 h is forgotten
  before it can replay), `INSERT INTO idempotency_keys ... ON CONFLICT (key) DO NOTHING` (:103) in the SAME transaction
  as the business unit of work (the use case's `UnitOfWork.Do` joins it through `TxBinder`, `unit_of_work.go:37`), the
  handler behind `SAVEPOINT handler_run` (:248) against a capture. A 5xx rolls the transaction back, key row included
  (:258); otherwise the savepoint is released (:265), healed when a statement failed on the server (:270), then `UPDATE`
  + `COMMIT`. TTL 24 h: delete at claim plus the purge job (`cmd/api/wire_background.go:56`, `PurgeIdempotencyKeys`),
  so retention is enforced and needs no change here. There was no per-response opt-out.
- **Catalogue.** `problemCatalogue` (`errors.go`), a slice of `{err, problem}`, 21 entries (20 slugs; two entries map to
  `validation-failed`), `problemFor` first match. The middlewares answer three more problems of their own
  (`idempotency-key-*`), `/readyz` one (`dependency-unavailable`), `rejectNUL` one (`not-found` for a NUL in the path)
  and the writer a fallback (`internal-error`): 25 slugs.
- **Writers.** Every refusal goes through `writeError`/`writeProblem` (`response.go`); `rejectNUL` (`nul.go`) and
  `readiness.go` use `writeProblem` too. The only other response writers are `writeJSON` (2xx), the replay
  (`replayCachedResponse`) and `flush`. No handler writes `problem+json` by hand.
- **The NUL refusal.** `rejectNUL` runs before the idempotency middleware (`server.go:62`): a NUL in the path is
  `404 not-found`, in the query or body `422 validation-failed` (`errMalformedRequest`, whose slug is the catalogue's).
  It is written before the store is touched, so nothing is ever stored under a key for it. It is not a separate
  catalogue entry: its problem literal now lives in `errors.go` (`notFoundResource`) and shares the class of the slug.
- **Surfaces.** Twelve keyed `POST` routes (`server.go`): onboard seller, suspensions, reinstatements, inbound plans,
  confirmations, cancellations, shipments, cartons, labels, dispatches, placement plans, acceptances.
  The MCP server (six read-only tools, ADR 0006) and the A2A agent (ADR 0009) are read-only: no write tool, no store of
  their own, nothing to classify there. No outbound caller sends a deterministic key.
- **Writes of a refusal.** In this design a joined unit of work shares the middleware's transaction and the savepoint
  is released on a 4xx, so what a use case wrote before it answered a refusal COMMITS with the key row. OUTCOME is
  therefore representable. No problem type of this service is one (decision 5).

## Decision

1. **Three classes, declared on the catalogue entry.** `problem` (`errors.go`) gains `class` (`replayClass`: `FINAL`,
   `OUTCOME`, `RETRYABLE`, `replay_class.go`). Every `problem` literal of the package lives in `errors.go` and declares
   one (`TestEveryProblemLiteralIsInTheCatalogueAndClassified`); a problem type without a class fails
   `TestEveryProblemTypeDeclaresAReplayClass`; the table below is held equal to the catalogue by
   `TestTheADRClassTableMatchesTheCatalogue` and each class is pinned by `TestEveryProblemTypeHasTheClassTheADRDecided`.
   The catalogue shape is unchanged (a slice of `{err, problem}`); the only reshaping is the fourth field of `problem`
   and the two literals (`notFoundResource`, `dependencyUnavailable`) moving from `nul.go`/`readiness.go` into `errors.go`.
2. **The writer marks, the middleware reads.** `writeProblem` (so `writeError`) marks the middleware's `capture` with the
   class of the problem it answers. `runFreshRequest` stores a 2xx, never stores a 5xx or a panic, and stores any other
   answer only if its class is FINAL or OUTCOME. It holds no slug and no status range beyond 2xx and 5xx.
3. **RETRYABLE is released exactly as the 5xx branch.** `tx.Rollback` (the key row with it), the answer reaches the
   client without `Idempotency-Replayed`, and the same key executes again. A runtime refusal that declares no class is
   released the same way (a safe fallback).
4. **OUTCOME commits with the key row.** The savepoint is released, the answer is stored and the transaction commits:
   the use case's writes and the stored answer are one commit. If the release fails (the writes are gone) the OUTCOME is
   a 500 and the key is released, like a 2xx behind a failed release. Proven with a probe, because no entry is OUTCOME
   (`idempotency_outcome_integration_test.go`).
5. **No problem type is OUTCOME.** Every refusal of every command is raised BEFORE the command's first write (see the
   audit below), so none commits state and then refuses.
6. **2xx, 5xx and FINAL are byte-identical to before.** The status codes, headers and bodies of every answer are
   unchanged; only the storing of a 4xx became a decision of the class. Slugs, titles and statuses are unchanged.
7. **Nothing consumer-side or client-side changes.** RELEASED means the same key may be tried again by whoever sent it,
   nothing more. No retry behaviour is added anywhere in this repository.

What a **concurrent duplicate** sees: the second request blocks on the unique index until the first transaction
resolves. When the first ends RETRYABLE (rolled back) the second's insert succeeds and it EXECUTES the command itself
and answers its own result (a success, or the same refusal, released too). When the first ends FINAL or 2xx the second
replays it. Releasing a RETRYABLE key also means a different body under the same key is a new request afterwards, not
`idempotency-key-reused`: nothing is left to compare against.

### Replay class of every problem type

`FINAL`: a function of the request alone. `OUTCOME`: the command committed state and then refused. `RETRYABLE`: nothing
committed and the answer depends on state that can change (or a 5xx). Line numbers are those of develop `5e32428`.

| Problem type | Status | Class | Why |
|---|---|---|---|
| `idempotency-key-required` | 400 | FINAL | Answered by the middleware before the store is touched (`idempotency.go:126`); a function of the request. Never stored under a key. |
| `idempotency-key-invalid` | 400 | FINAL | Same (`idempotency.go:130`). |
| `idempotency-key-reused` | 409 | FINAL | The store's own answer to a key seen with another method, path or body (`idempotency.go:169`): no handler ran and no key row is written. |
| `invalid-window` | 400 | FINAL | `scorecard.ErrInvalidWindow`, raised by `GET /sellers/{id}/scorecard` from the query alone; not a keyed route, never stored. Classified so no problem type is unclassified. |
| `invalid-cover-days` | 400 | FINAL | `placement.go:192`: `coverDays` outside 1..90 is checked before the unit of work opens, from the body alone. Nothing is written. |
| `validation-failed` | 422 | RETRYABLE | One slug, mixed causes: request-only (`errMalformedRequest` for bad JSON, unknown field, NUL; field rules such as `carton_add.go:92` grossWeightG, `:120` parentSscc) and state-dependent (`carton_add.go:65` a shipment holds at most N cartons, `shipment.go:201` a shipment needs a carton before it is labelled, `carton_add.go:157` the kind the plan's packing allows). Mixed slug: RETRYABLE (judgement call 1). |
| `case-pack-not-uniform` | 422 | RETRYABLE | `carton_add.go:152` (the plan's packing, fixed) and `:224` (an earlier carton of the same shipment holds the item at another quantity): depends on the shipment's cartons. |
| `case-qty-mismatches-gtin` | 422 | RETRYABLE | `carton_add.go:211-214`: depends on the pack levels fed by the catalogue events (`Packaging.ListByItem`, `shipments.go` `caseUnits`). |
| `carton-overweight` | 422 | RETRYABLE | `carton_add.go:173`: depends on the destination node's `maxCartonWeightG`, an event-fed node fact that can change. |
| `carton-oversize` | 422 | RETRYABLE | `carton_add.go:176`: the node's `maxCartonDimsMm`, same. |
| `invalid-sscc` | 422 | FINAL | `carton_add.go:106`, `:117`: `gs1.ParseSSCC` of the request's own `sscc` / `parentSscc` (18 digits, mod-10): no state is read, and no write precedes it (`Save` follows `AddCarton`). |
| `carton-contents-mismatch-plan` | 422 | RETRYABLE | `carton_add.go:196-199`, `shipment.go:227`: the plan's lines and the quantities already packed in this and the plan's other shipments. |
| `inbound-capacity-exceeded` | 422 | RETRYABLE | `capacity.go:43-62` -> `capacity.Check`: the seller's reservation, the node's limit and the tier factor move with other plans and events. Raised before `Plans.Save` (`plans.go:107`). |
| `network-split-destination` | 422 | RETRYABLE | `capacity.go:69`: the destination node's type is an event-fed fact; a node can be re-registered. When unsure, RETRYABLE. |
| `network-split-unavailable` | 422 | RETRYABLE | `capacity.go:80`: the region has no fulfilment centre yet; node-topology events add one. |
| `not-found` | 404 | RETRYABLE | `ports.ErrNotFound`: an unknown plan, shipment, placement plan or the referenced seller of a body (`sellerId` of `POST /inbound-plans`, `requireActiveSeller` `sellers.go:91`), which another request onboards later. Shared with the NUL-path 404 (`notFoundResource`). Judgement call 2. |
| `seller-suspended` | 409 | RETRYABLE | `seller.go:166` `EnsureActive`: a reinstatement lifts it. Raised before any write. |
| `seller-exists` | 409 | RETRYABLE | `sellers.go:27`: the id is supplied by the caller and a seller is never deleted, so the answer cannot go stale and FINAL is arguable; RETRYABLE by the rule "when unsure". Judgement call 3. |
| `node-cannot-receive` | 409 | RETRYABLE | `plans.go:43`, `carton_add.go:123`, `network.go:75-78`: an unknown, inactive or non-inbound node (or one that does not accept pallets): event-fed node facts. |
| `sscc-not-unique` | 409 | RETRYABLE | `carton_add.go:110` (an earlier carton of the shipment) and `shipments.go` `mapSave` (the SSCC registered to another shipment, or the issuer's collisions exhausted `SsccIssueAttempts`): depends on other cartons and shipments; a retry with an issued SSCC can succeed. |
| `shipment-not-labelled` | 409 | RETRYABLE | `shipment.go:238-245`: labels are added by other requests until the shipment is labelled. |
| `illegal-transition` | 409 | RETRYABLE | The aggregate's state (plan confirmed or cancelled, shipment labelled or shipped, placement plan superseded) which other requests change. Raised before the save. |
| `concurrent-update` | 409 | RETRYABLE | `repositories.go:23-35`: a version-guarded save found zero rows (or a lost create race, 23505) after the use case's own one retry (`service.go:69` `atomically`); another writer committed. Nothing of this request was written. |
| `internal-error` | 500 | RETRYABLE | A 5xx is never stored. |
| `dependency-unavailable` | 503 | RETRYABLE | A 5xx; answered only by `GET /readyz`, not a keyed route. Classified so no problem type is unclassified. |

#### Why no problem type is OUTCOME

In this store a refusal's writes DO commit (savepoint released), so the question is whether any use case writes and then
refuses. Audit (`internal/application/usecases`): `OnboardSeller`, `changeSeller` (suspend, reinstate), `CreateInboundPlan`
(`requireActiveSeller`, `requireNode`, `planFacts` -> `checkCapacity`/`networkSplitFacts`, `inboundplan.Create`, THEN
`Plans.Save`, `Capacity.Reserve`), `ConfirmInboundPlan`, `CancelInboundPlan`, `CreateInboundShipment`, `AddCarton`
(`AddCarton` on the aggregate, THEN `Save`), `LabelShipment`, `ShipInboundShipment`, `AcceptPlacementPlan` and
`ComputePlacementPlan` (`requireActiveSeller`, the facts, `supersedePrevious`, THEN `placementplan.Compute`, `Save`): in
every one the checks that can refuse run before the first write, and `Save`/`Reserve`/`Append` fail only on a server
error (5xx) or a lost race (`concurrent-update`, nothing written). So no refusal commits state. The one place a
refusal textually follows a write is `ComputePlacementPlan` (`supersedePrevious` saves the previous plan, then `Compute`
can answer `validation-failed` for an invalid id): not reachable, because a previous plan to supersede exists only for
the same (seller, item) pair and a stored plan has valid ids; and `validation-failed` is RETRYABLE, so even then the whole
request would roll back.

### Judgement calls (most consequential first)

1. **`validation-failed` is RETRYABLE.** The rule: a slug that mixes request-only and state-dependent causes is
   RETRYABLE. It mixes them (see the row), so a malformed body, which a retry can never fix, is released too and
   re-executes once per retry (one more parse, no write). Splitting the slug so the request-only causes become FINAL
   changes a wire slug and is a recorded follow-up, not part of this change. The NUL refusals share it and are never
   stored anyway (written before the middleware).
2. **`not-found` is RETRYABLE.** Plan and shipment ids are minted here and nothing is deleted, which is the case where the
   standard allows FINAL; but the slug also answers the referenced seller of a body (onboarded by another request
   later) and the NUL path, so the slug is shared with a referenced fact. RETRYABLE costs one more lookup that fails the
   same way.
3. **`seller-exists` is RETRYABLE although FINAL is arguable.** The id is the caller's, and sellers are never deleted,
   so "already onboarded" cannot become false. It is state-dependent and not a function of the request alone, so the
   rule "when unsure choose RETRYABLE" decides; the cost is one more execution that answers the same refusal.
4. **`invalid-sscc` is the only FINAL refusal behind a handler** (with `invalid-cover-days`): it is a function of the
   SSCC string alone, but it is raised after state checks (`AddCarton` first refuses a non-DRAFT shipment or a full one),
   so a first answer can be a different refusal; a retry that reaches it gets the same one.
5. **`invalid-window` is FINAL** although it is never stored (a GET): classification only.
6. **The three `idempotency-key-*` problems are FINAL** although nothing is stored under their key: written before the
   store is touched, a function of the request alone.

## Consequences

- A retry under the same key of any RETRYABLE refusal executes again, including `concurrent-update` (the loser's retry
  now succeeds instead of replaying the conflict). `Idempotency-Replayed: true` appears only on a replayed 2xx or a
  replayed FINAL `invalid-cover-days` / `invalid-sscc`.
- Per-key storage and the 24-hour window are unchanged (delete at claim, purge job). A released refusal leaves no row.
- Callers: the console remote (`web/`) is the only caller of these routes and a person drives it; whether to retry a
  released refusal stays the caller's decision. No outbound caller of this service sends a deterministic key and no
  retry is added.
- Follow-ups recorded, not done: split `validation-failed` into request-only and state-dependent problem types; the MCP/A2A
  surfaces get no write tool in this change.
