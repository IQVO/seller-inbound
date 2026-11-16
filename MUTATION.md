# Mutation testing: residual mutants

Gate: `.gremlins.yaml` (`efficacy: 88`, `mutant-coverage: 95`), measured per package with
`gremlins unleash ./internal/domain/<pkg>/` (what the `mutation-fast` CI job runs). The fleet
standard is 99/99; it is not reachable here without production changes (see the capacity rows).

Measured 2026-11-15:

| Package | Killed | Lived | Not covered | Efficacy | Mutator coverage |
|---|---|---|---|---|---|
| capacity | 45 | 6 | 0 | 88.24% | 100.00% |
| inboundplan | 60 | 0 | 0 | 100.00% | 100.00% |
| inboundshipment | 148 | 1 | 0 | 99.33% | 100.00% |
| network | 44 | 0 | 2 | 100.00% | 95.65% |
| placementplan | 27 | 0 | 0 | 100.00% | 100.00% |
| scorecard | 31 | 0 | 0 | 100.00% | 100.00% |
| seller | 13 | 0 | 0 | 100.00% | 100.00% |
| shared (incl. gs1) | 216 | 0 | 0 | 100.00% | 100.00% |

Aggregate (`make mutation-full`, the weekly job): 584 killed, 7 lived, 2 not covered, efficacy 98.82%, mutator coverage 99.66%.

## Equivalent mutants (cannot be killed: no input changes the observable result)

| Package | File:line | Mutator | Why |
|---|---|---|---|
| capacity | capacity.go:81:22 | CONDITIONALS_BOUNDARY | `monthlyVolumeDm3 <= 0` to `< 0`: for 0 the next line computes `0 * factor / 10000 = 0`, the same value. |
| capacity | capacity.go:174:16 | CONDITIONALS_BOUNDARY | `len(known) > NetworkSplitMaxNodes` to `>=`: for exactly 3 entries `known[:3]` is the same slice. |
| capacity | capacity.go:200:14 | CONDITIONALS_BOUNDARY | Same guard on `ids` in `evenIDs`: `ids[:3]` of 3 ids is identical. |
| capacity | capacity.go:230:14 | CONDITIONALS_BOUNDARY | `assigned < qty` to `<=`: for equality the body adds `qty - assigned = 0`. |
| capacity | capacity.go:253:28 | CONDITIONALS_BOUNDARY | `out[i].remaining > out[j].remaining` sits inside `if out[i].remaining != out[j].remaining`, so the operands are never equal and `>` equals `>=`. |
| capacity | capacity.go:255:20 | CONDITIONALS_BOUNDARY | `out[i].id < out[j].id` to `<=`: only differs for equal ids AND equal remaining, i.e. two indistinguishable entries. |
| inboundshipment | receipt.go:204:12 | CONDITIONALS_BOUNDARY | `actual > expected` to `>=` in `quantityEvent`: its two callers only call it when `got != ct.QtyEaches` or with `expected = 0, actual > 0`, never with equal quantities. |

The capacity guards are redundant code; deleting them (production change, out of scope for a
tests-only ratchet) would remove these six survivors and let the gate return to 99.

## Not covered mutants (coverage-profile artefact)

| Package | File:line | Mutator | Why |
|---|---|---|---|
| network | demand.go:12:30 | ARITHMETIC_BASE | `const ObservationWindow = 28 * 24 * time.Hour` is a `const` declaration: the Go coverage profile has no executable statement for it. |
| network | demand.go:12:35 | ARITHMETIC_BASE | Same declaration, second `*` operator. |

Two of 46 mutants make 95.65% the ceiling of mutator coverage for `network`.
