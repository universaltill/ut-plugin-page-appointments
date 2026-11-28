# Review: scheduling engine (ut-docs#7112, epic #7111)

Reviewer: independent inline review (Sonnet 5.5, no separate subagent; the Tester ran concurrently). Scope: whole working tree of the first feature commit.

## What shipped
Repo skeleton (manifest `page`/wasm, scripts, public-repo CI on ubuntu-latest) and a pure Go scheduling engine: availability (single/any resource), conflict check with manager override, state machine, deposit math (128-bit allocation), walk-in queue estimate, money copy.

## Checked, no finding
- Overlap: half-open intervals, hold = start..end+buffer, only booked/confirmed/checked_in/in_service hold, supersedes row ignored; matches contract s3.
- DST: window endpoints built with time.Date in the shop zone, slots stepped in real elapsed time; 24:00 handled by normalisation; horizon by civil days.
- State machine: table equals contract s2 exactly; no_show grace, completed needs sale_ref, late_cancel only on client cancel, error leaves input unchanged.
- Deposit: Total bounded (no int64 overflow), Div64 cannot panic (amount <= total, deposit <= total), largest-remainder never exceeds a line, sums to deposit exactly.
- Walk-in: created checked_in, source own; estimate candidates (now, hold ends, window starts, time-off ends) are complete; in_service overrun handled; deterministic tie-break.
- Security: manifest only `events:receive`; CI `contents: read`, GitHub-hosted runner only; no secrets (scan clean); no client PII in entities; engine takes clock and ids as inputs (purity test).

## Findings
| # | Severity | Finding | Status |
|---|---|---|---|
| 1 | Low | ApplyDeposit with a negative deposit returned a negative Excess. Now clamps to 0; test added. | Fixed |
| 2 | Low | CheckConflict override does not require a non-empty ActorUserID; the host must pass the actor. | Accepted, wiring slice |
| 3 | Low | Supersede keeps the old CreatedAt on the new row; the wiring slice should stamp it from the clock. | Accepted, wiring slice |
| 4 | Info | Availability returns ErrResourceNotEligible before the inactive-resource empty result (documented precedence). | Accepted |

## Verification
`scripts/verify.sh` run once after the fix: gofmt, vet (host and wasip1), race tests (engine 99.0%, money 100%), build, manifest validate, secret scan, package: OK. Finding 1 test not separately reverted (old code returns Excess -500 by construction).

Pre-flight: no UI, no locale keys, no help topic (no shop-visible surface in slice 1); no secrets; no real client names in test data; no file writes in the engine.

## Verdict
Approve. Deferred: findings 2 and 3 to the wiring slice.
