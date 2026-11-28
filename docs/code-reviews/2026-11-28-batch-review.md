**VERDICT: APPROVE WITH FIXES** (4 blocking fixes, all small; 15 non-blocking)

# Review: appointments batch (#7113, #7114, #7115/#7116 pure parts)

- **Date:** 2026-11-28. **Reviewer:** one combined independent Reviewer (Fable), `Skill(reviewer)`; not the Dev agents.
- **Scope:** `ut-plugin-page-appointments`, branch `feat/7111-batch` (HEAD `b72cd01`), `git diff origin/main`: 53 files, +9,233. Read-only: nothing edited, committed or pushed in the repo.
- **Read against:** ADR-0221, `appointment-v1.md`, `booking-common.md`, `2026-11-28-adr-0221-fable-review.md`, `fable-legal-review.md`, `seam-check.md`, the repo `CLAUDE.md`, and the host's `internal/db/plugindb` (guard.go, migrate.go, store.go on universal-till `origin/main`).
- **Gate:** `scripts/verify.sh` run by me: **OK** (gofmt, vet host + wasip1, race tests, build, validate, secret scan, package). Coverage: engine 99.1%, money 100%, admin 91.8%, calendar 91.1%, checkout 92.5%, logbook 100%, store 87.2%.
- **Extra verification:** two suspected store bugs reproduced with a scratch test in a temp copy of the repo (memdb and real SQLite): B2 and N1 below. The migration text was traced through the host's `splitMigration` (the triggers split correctly).
- **Pre-flight:** review-only pass; no commit, PR or merge made by this reviewer, so the commit-side items of PREFLIGHT.md do not apply here. Git identity, PR and merge steps stay with the orchestrator.

## What is right

- Binding build rules are met in code and schema: no phone, email or note column or field anywhere; `client_first_name` capped at 40, digits and `@` refused, label "First name only"; `time_off.reason` is a fixed list with a CHECK; override and correction reasons carry the "Do not write client or health details" hint; provider no-show/cancel needs an explicit acknowledgement; `no_show -> checked_in` is manager + reason + actor.
- Immutability is enforced by triggers, with the one allowed in-place change (blanking the name). Purge and erasure write an event without the name; a DB-dump test checks it.
- All SQL is constant text with numbered placeholders. No injection path found.
- Money: amounts are `money.Money` in engine/store/calendar; `int64` minor units appear only at JSON/SQL boundaries (one exception: N8).
- Purity: no `time.Now`, no network or file access outside `main.go`; engine purity test passes.
- Honesty: `manifest.json`, `README.md` and `src/main.go` are unchanged. Nothing is wired, no view is registered, the manifest still says "no UI yet". Nothing pretends to run on a till. `store/db.go` and `checkout/README.md` state the missing seams correctly. Exceptions: B4, N12.

## Blocking (fix in this batch)

**B1. Purge, Erase and Reconcile read the whole appointments table in one query; the host caps a query result at 1 MiB.**
`src/store/sql.go:43` (`SQLChainInfo`), `:47` (`SQLStaleProjection`); used by `src/store/purge.go:20,70,114` and `src/store/repo.go:650`.
Host: `plugindb/store.go` `MaxResultBytes = 1 << 20`, `ErrResultTooLarge`. One chain row is about 100 bytes of JSON, so the limit is reached near 10,000 appointment rows (4 chairs x 10 visits a day: about 8 months). From then on `Purge` returns an error on every call, so first names are never blanked again, and `Erase` fails too, so an erasure request cannot be honoured. This breaks the binding retention rule of #7113 (legal finding 1) silently and permanently.
Fix: select only rows that still have a name (`WHERE client_first_name <> ''`), page by keyset with `LIMIT`, and resolve each chain by walking `supersedes_id` per row (or store a `chain_id` column). `Erase` should load one chain, not the table. Reconcile: page the same way, or restrict to rows changed recently. Add a test with a DB double that enforces the 1 MiB cap.

**B2. `Create` with an id that already exists adds lines to the existing, immutable appointment.** (Reproduced.)
`src/store/repo.go:432-441`, `src/store/sql.go:29` (`INSERT OR IGNORE` on lines, written before the appointment row).
Scenario: a create succeeded but the answer was lost; the caller retries with the same id (the doc comment invites this) and a changed service list. Line 0 is ignored, line 1 is inserted, then the appointment insert fails on the primary key. The stored appointment now has an extra line that no trigger allows anyone to remove. Later the basket gets a service the client did not book, or auto-completion reports `Incomplete` for ever. `CheckConflict` does not stop it because it skips `e.ID == cand.ID`.
Fix: look the id up first and refuse (or return the existing booking) when the appointment row exists; when only orphan lines exist for the id, compare them and refuse on a difference, or take a fresh id.

**B3. Event order and "last event" use the wall clock (`at`), not insert order.** (Reproduced.)
`src/store/sql.go:40` (`ORDER BY at, rowid`), `:47` (`ORDER BY e.at DESC, e.rowid DESC`); `src/store/purge.go:91`.
Scenario: the till clock steps back (NTP correction, operator fixes the time). The next transition gets an earlier `at` than the previous event, so the "last event" is the older one. Each `Reconcile` call then appends a false event (`from` = the wrong state) for that row, again on every call until the clock passes the old time; in my test three calls wrote three false audit events. The history is listed in the wrong order, and Purge can take the wrong cancellation time.
Fix: order by `rowid` only in both statements (or add an integer `seq` column per appointment). Keep `at` as data.

**B4. The calendar's `Store` has no implementation, and its comments say it has.**
`src/calendar/model.go:47` ("The real store applies it in a transaction"), `:63` ("#7113 implements it").
`store.Repository` does not implement `calendar.Store`: different methods, no `Shop`, `Policy`, `Queue`, `Commit`, no `ProviderMastered`, and the schema has no walk-in queue table. The host refuses `BEGIN/COMMIT` for guests (`plugindb/guard.go`, stated in `store/db.go:11`), so "in a transaction" cannot be true. The calendar's safety rests on an atomic `Commit(Change)`: seating a walk-in is Insert + Dequeue, a move is Insert + Superseded. Without atomicity a crash between the two leaves the walk-in both seated and still queued.
Fix now: correct both comments to say what is true (no implementation yet; the host has no transactions; the adapter must order writes like `store.Repository` does). File one card for the adapter: queue table, crash-safe ordering, and one write path (the calendar should call `Repository.Create/Reschedule/Transition`, which already carry the optimistic check and the current-row check, instead of re-implementing them).

## Non-blocking (fix before the slice is wired to a till, or card it)

**N1. A corrected no-show is handled two different ways.** `src/store/repo.go:496-531` runs no conflict check on `no_show -> checked_in`, so the booking silently holds time again over whoever took the slot, with no override event. `src/calendar/actions.go:206-224` does check, and refuses the correction when "allow double booking" is off (the default). Scenario: client marked no-show at 10:16, a walk-in is seated in the slot at 10:20, client arrives at 10:25; the manager cannot undo the no-show at all. The binding rule (legal finding 7) says the mistake is undoable. Fix: one behaviour in the repository: always allow the correction for a manager, and when it overlaps, write a `conflict_override` event with the reason, whatever the double-booking setting.

**N2. Admin pages are open to every operator unless the wiring sets `CanAdmin`.** `src/admin/handler.go:97`. `nil` means allow. A cashier could switch on double booking or change retention. Fix: fail closed; build the default check in (operator absent, or role manager/admin/owner), the same rule as `calendar.Operator.IsManager`.

**N3. Editing a resource wipes its logbook link.** `src/admin/handler.go:231-242`, `src/admin/views.go:86-93`. The form is never pre-filled, and save overwrites every column. Scenario: the owner picks "Sam", types a new name, saves; `logbook_staff_ref`, colour and kind are reset to blank/defaults, and the logbook link stops writing entries for Sam ("unmapped") with no warning. Fix: pre-fill from the chosen resource (as the hours page does with `show_hours`), or keep stored values for fields left empty.

**N4. Bookings of an inactive staff member disappear from the day view.** `src/calendar/dayview.go:107`. Scenario: Sam is set inactive with 12 future bookings; no day shows them, so they cannot be opened, moved or cancelled, yet they still exist and clients turn up. Fix: draw a resource that has bookings on the day even when inactive (marked inactive), and warn on deactivation in admin.

**N5. The completed event uses one processor-wide currency, not the appointment's stored currency.** `src/checkout/checkout.go:170,331`. The contract stores `currency` on the record "so a later currency change never mixes amounts"; `engine.Appointment` has no currency and `checkout.Store.Appointment` does not return it. After a currency change, an old booking's amounts are published under the new code. Fix: return the record's currency from the store and use it in `BuildPayload`.

**N6. Logbook totals are the booking quote, while the package comment says they come from the paid sale.** `src/logbook/logbook.go:2-3,157-159`; payload built at `src/checkout/checkout.go:369-371`. The quote is "never forced onto the sale", so after a price change or discount the logbook total differs from the money taken. This follows `appointment-v1.md` section 5 as written, so it is a contract question: either the logbook reads amounts from the sale by `sale_ref` (ADR-0149 linked mode), or the payload carries the paid amounts from `sale.completed`. Fix the comment now; raise the contract point with the architect before #7116 goes live.

**N7. Two name validators with different rules.** `src/store/validate.go:49` refuses `unicode.IsNumber` too; `src/calendar/names.go:36` only `IsDigit`. A name with "²" or "Ⅷ" passes the calendar and fails in the store as a non-user error (host error page instead of a notice). Fix: one function (the store's), used by both.

**N8. Raw `int64` money arithmetic outside `money`.** `src/logbook/logbook.go:95,158` (`total += l.AmountMinor`). Overflow is checked, but the repo rule is `money.Money` only. Fix: sum with `money.Money` (the package moves to the logbook repo anyway; carry the rule with it).

**N9. Queued walk-ins carry a first name with no retention rule.** `src/calendar/model.go:41-45`, `src/calendar/queue.go:190`. A queue entry that is never seated or dropped keeps the name for ever; `Purge` covers appointments only. Fix (in the adapter card of B4): the queue is cleared at the end of the business day, or the purge covers it; add it to the PII dump test.

**N10. Nothing calls `Purge`, and no screen calls `Erase`.** `src/store/purge.go:60,111`. The settings page says names are "blanked automatically" (`locales/en.json:76`). Not live today because nothing is wired. Condition for wiring #7113: a purge trigger (on every event, as the comment proposes) and an "erase this client's name" action on the booking detail, both tested.

**N11. A crash between blanking and the purge event loses the event.** `src/store/purge.go:135-147`. The rerun finds nothing to blank and writes no event, so the "purge writes an event" rule is missed for that chain. Fix: write the event first (it holds no name), then blank; a rerun then blanks without a second event when one exists.

**N12. Two UI texts promise something this batch cannot do.** `locales/en.json:115` "Complete this visit at the till when the client pays" (no basket seam and no receipt-picker screen, so an `in_service` booking has no way to complete); `locales/en.json:76` (N10). Not live. Condition for wiring #7114: the receipt-picker completion (`CompleteByReceipt`) has a screen, or the text says what is true.

**N13. Stale or repeated posts can act on a superseded row.** `src/calendar/model.go:72` does not say what `Booking(id)` returns for a superseded row (the test fake hides it; `store.Repository.Get` returns it). If it is returned, re-posting a move forks the chain into two current rows, and `checkout.complete` (`src/checkout/checkout.go:288`) completes a superseded row while the current one stays checked in. The schema also allows two rows with the same `supersedes_id`. Fix: state the contract ("superseded = not found"), add a unique partial index on `supersedes_id WHERE supersedes_id <> ''` in a new migration, and resolve a line ref to the head of its chain in checkout.

**N14. Locale gaps.** (a) `src/admin/views.go:77,135,244`: table cells carry the English literals "active"/"inactive", "staff"/"chair", "holiday"/"away"/"other". (b) `locales/locales.go` embeds English only, and `src/calendar/i18n.go` resolves cell text in the guest, so a non-English operator gets translated labels (host) around English cells (guest) until Weblate bundles are embedded too. (c) `src/calendar/i18n.go:35` formats with a translated string; a bad `%` verb from a translator prints `%!s(MISSING)`. Fix: resolve admin cells through the bundle like the calendar; embed `locales/*.json`; add a test that every key keeps the same verbs as English.

**N15. Smaller points.**
- `src/calendar/queue.go:109-117`: two buttons per queued walk-in with no cap, while open-buttons are capped at 60; a long queue on a busy day can pass the 200-component limit and the till refuses the whole page. Cap it with a notice.
- `src/store/repo.go:78`: currency and time zone are the plugin's own settings with GB defaults, not the shop's facts; line quotes come from the catalogue in the till's currency. Read both from the host when wired. `time.LoadLocation("Local")` is accepted as a zone (`repo.go:330`); refuse it.
- `src/logbook/logbook.go:78`: `DisallowUnknownFields` makes any additive payload field (allowed by ADR-0039) stop every logbook entry. Ignore unknown fields, or version the payload.
- `src/store/repo.go:125-134, 241-259`: hours and service resources are delete-then-insert without a transaction; a crash leaves partial hours until the form is saved again. Not repaired by `Reconcile`, although `store/db.go:12-14` says every multi-statement write is. Soften the comment.
- `src/calendar/booking.go:265,385`: a move offers slots computed with the service's current duration but keeps the booked duration; after a duration edit the offered slot can be refused as a conflict.
- `src/admin/handler.go:143`: removing a time-off entry that no longer exists answers "Saved".

## Checked and found fine

SQL injection (none: constants + placeholders); migration triggers through the host splitter; trigger rule for name blanking; conflict interval maths `[start, end + buffer)` in SQL and engine; superseded rows excluded in every reader (F11); override needs actor (F14); move excludes the row being moved (F13); no-show grace and late-cancel edges; DST handling in `bounds`, `localTime`, slot labels; action-name parsing and id tokens; redirect only to the plugin's own route; `sync.Mutex` only outside engine/money; wasip1 vet and build; no real shop or client names in test data; no secrets.

## Deferred items (cards for the orchestrator)

1. Adapter between calendar and repository, queue table, crash-safe write order, queue name retention (B4, N9, N13).
2. Purge trigger and erasure action before #7113 is wired (N10).
3. Receipt-picker completion screen before #7114 is wired (N12).
4. Contract question: logbook amounts from quote or from the sale (N6); record currency in the completed event (N5).
5. Embedded Weblate bundles for guest-resolved text (N14).

## Verdict

**APPROVE WITH FIXES.** Merge after B1 to B4 are fixed and `scripts/verify.sh` passes again (one run for the batch). The non-blocking items are conditions for wiring each slice to a till, not for this merge; nothing in the batch runs on a till today.
