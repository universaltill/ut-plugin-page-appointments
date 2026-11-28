# Tester record: appointments batch (#7113, #7114, #7115/#7116 pure parts)

Date: 2026-11-28. Branch `feat/7111-batch` vs origin/main in
`ut-plugin-page-appointments`. One combined Tester (not the Dev agent).
`scripts/verify.sh` was green beforehand and was not re-run. Nothing was edited
or committed on the branch; mutations ran on a scratch copy (`/tmp/ept-mut2`,
deleted at the end).

Verdict: **PASS for the code and its unit/store tests (no false-pass found in
the binding areas); FAIL (not met) on 7 acceptance points that depend on work
outside this batch (listed below).** No runtime driving was possible: there is
no #3157 seam (`src/main.go` still answers `{}`), so nothing was run on a till.
Nothing visual was looked at; the UX/touch/long-locale ACs are unattested except
by the synthetic `TestI18n_LongLocaleTextStaysInLimits`.

## 1. Mutation checks (by hand, scratch copy, `go test ./src/...`)

Killed (test failed as it should):

| Area | Mutants killed (by) |
|---|---|
| Retention purge | window x2 and /2, boundary `After`->`!Before`, cancelled rows ignoring cancellation time, default 90->89, max 730->731, min 1->0 (`TestPurge_DefaultNinetyDays_BoundaryAndSuperseded`, `TestPurge_CancelledUsesCancellationTime`, `TestSettings_SaveAndRanges`) |
| Name validation | max 41, max 39, bytes instead of runes, no `@`, no digit+number check, no `IsNumber`, no control check, no trim (`TestValidateFirstName`) |
| F11 superseded rows | `Current` identity, CheckConflict on raw rows, availability on raw rows, nearest-free/walk-in on raw rows, SQL without `NOT EXISTS` (engine F11 tests + `TestReschedule_FreesOldSlot`) |
| no_show -> checked_in manager only | engine gate removed, calendar gate removed, verb `manager` flag off, reason not required |
| Override needs actor | engine CheckConflict, correction transition, calendar do(), booking override (F14 tests) |
| Idempotent completion | `settled` never/never-duplicate, mark-published removed, mark on failed publish, flush skipped, line-subset check, logbook `InsertIfAbsent` always true, logbook duplicate branch (`TestHandleSale_CompletesOnceEvenIfDeliveredTwice`, `TestPublish_RedeliverySafe`, `TestHandle_WritesOneEntryAndIsIdempotent`) |
| Provider confirm tick, server side | ack check removed, always ok, no-show not covered, only own source (`TestTransitions_NoShowGraceAndProviderConfirm`) |
| DST | window end computed by UTC add, windows built in UTC (`TestAvailability_DSTSpringGap`); slot step ignored at 20/30; slot end `!Before` |
| Other engine | no-show grace inclusive, late cancel non-strict, completed without sale ref, no_show holding time, unmapped logbook staff, unknown payload fields |

Survived (6), triage:

1. `ack-no-cancelc`: dropping the provider-ack requirement for **cancelled_by_client** on provider bookings survives. Contract only demands the ack for no-show; the code is stricter and untested for cancel. Low; add one assertion or drop the extra rule.
2. `slot-end-after` (window end + 5 or + 10 minutes): a slot that overshoots the working window by a part of a step survives. Real weak boundary (needs a duration not a multiple of the step, for example 25 min on a 15 min grid). Low-medium; one test case closes it.
3. `logbook-unmapped-empty` (drop `StaffRef == ""` branch): equivalent in the test fake (`Has("")` is false); real directory could differ, so the explicit empty check is kept by the code but not proven. Low.
4. `name-no-isdigit`: equivalent mutant (`IsNumber` covers `IsDigit`).
5. `manager-bymanager-true`: equivalent for the unns verb because the calendar `v.manager` gate runs first; the engine gate was killed separately.
6. `dst-noon-weekday` and `dst-autumn-weekday` (weekday taken in UTC): equivalent on the tested dates (midday UTC and local give the same weekday in London).

Killed DST counts: spring gap and autumn repeat tests assert exact slot counts
(14 on the autumn day, with 01:00 to 01:45 each twice); day-view tests cover
both transitions.

## 2. Acceptance criteria and contract

Met: migrations apply and roll forward on real SQLite
(`TestMigrations_ApplyAndRollForward_SQLite`) plus immutability triggers; DB
dump has no phone, email or notes (`TestMigrations_NoContactOrNoteColumns`,
`TestPII_DumpAfterPurgeHasNoNames`, `TestNoNoteOrContactFieldsOnTypes`, day view
and stored-type checks); en.json English only (`TestLocale_EnglishOnly`);
create/move/cancel with a named overlap reason and nothing written on conflict
(`TestMove_OverlapNamedAndNothingWritten`); two services give two lines with a
per-line ref, no name or deposit in the payload (`TestBuildLines_*`); completion
once on double delivery, including concurrent redelivery; logbook entry
idempotent, unmapped resource writes none and returns a message key; binding
build rules: 40 runes, no digit or `@`, "First name only", fixed time-off
reasons, override hint, 90/730 retention, erasure, no note column, superseded
rows never hold time (F11), no_show -> checked_in manager, reason and event,
provider no-show confirm tick enforced server side, `in_service ->
cancelled_by_shop` with reason, override with mandatory actor.

Unmet or not demonstrable in this batch:

1. **#7113 "admin page works offline"**: only view documents with fixtures; no runtime. `main.go` is not wired (no #3157), so no page exists on a till.
2. **#7114 "works with the network cable pulled"**: no code path touches the network, but it was not run. **UX pass for touch and long locales** was not done (no screenshots, no device); only a synthetic length test.
3. **#7115 "opens a basket with two lines and the deposit shown"**: only `BuildLines` (the pure part). The `add_to_basket` call is blocked by the missing basket seam (contract section 6), and the deposit display is by contract out of this slice (no deposit logic). Unmet by design; the card AC must be split or re-stated.
4. **#7116 "mapping set once by the owner"**: the admin resource form stores `logbook_staff_ref` (a free text field, editable at any time); there is no staff-mapping screen, no "set once" rule and no check against the logbook's staff list in this repo. Unmet. The "mapping" and the unmapped message exist only in the pure logbook consumer, which has no repo of its own yet (#3700, #3702).
5. Contract: manifest still says "slice 1, no UI yet", has only `events:receive`: no publish permission for `appointment.completed`, and no `retention.keep_years` following the 90-day setting (contract section 1 says the manifest follows it). The ADR-0221 per-slice list also asks to relax `validate.sh` permissions in the next slice. Flag for the integration slice.
6. Contract section 5: "published only after `event_publish` returns 0" and outbox are implemented against an interface (`Pub`), unproven against the real host function (no seam).
7. Receipt-picker recovery (`TestCompleteByReceipt*`) exists; the actual receipts view call is not wired.

## 3. Store tests really use SQLite; skip safety

- Store tests run each case against both the in-memory double and a real SQLite
  through the `sqlite3` CLI (`-bail -json`, `.parameter set`). Confirmed
  locally: 0 skips, 26 PASS lines in `go test -v ./src/store/...`.
- CI: `.github/workflows/ci.yml` runs `scripts/verify.sh` on `ubuntu-latest`.
  The Ubuntu 24.04 runner image readme (actions/runner-images) lists
  `sqlite3 3.45.1`, which supports `-json` and `.parameter` (3.33+ and 3.28+).
  So the SQLite subtests do run in CI today.
- **Weak point:** `newCLIDB` calls `t.Skip` when `sqlite3` is missing. With
  `sqlite3` removed from PATH, the `/sqlite` subtests (purge, erase, PII,
  migrations, immutability triggers) all show SKIP and the package still
  reports ok. A runner image change would silently remove the only real-SQLite
  coverage, including the migrations and trigger tests. Recommend: fail instead
  of skip when `CI=true` (or `REQUIRE_SQLITE=1`), or have `verify.sh` run
  `command -v sqlite3` and fail. Not changed here (no source edits).

## Gate and housekeeping

Not re-run: `scripts/verify.sh` (green per the orchestrator). Scratch copy
and helper scripts in /tmp removed. No server or binary was started. The real
repo was verified unchanged (`git status` clean, `cmp` with scratch for
logbook.go).
