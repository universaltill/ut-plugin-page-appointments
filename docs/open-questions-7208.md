# Open questions for the architect (#7208)

## Q1 (review N6): logbook amounts, booking quote or paid sale?

`appointment.completed` carries each line's `amount_minor`, which is the
booking quote (`appointment-v1.md` section 5; the quote is "never forced onto
the sale"). The logbook entry total is the sum of those quotes. After a price
change or a discount at the till the logbook total differs from the money
taken.

Options:
1. Keep the quote and say so in the logbook UI (current behaviour; the
   package comment now states it).
2. The logbook reads the paid amounts by `sale_ref` (ADR-0149 linked mode,
   `sales.receipts.v1`).
3. The payload carries the paid amounts taken from `sale.completed`.

Needs a decision before #7116 goes live. Nothing else depends on it.

## Q2 (owner/legal): `retention.keep_years` is provisional

The manifest declares `keep_years: 1`. The signed number is owed by legal card
#6596; until then it is a placeholder. `keep_years > 0` means: the data is
moved aside (not deleted) for that long on uninstall, `delete-now` is refused,
and the database is opened `synchronous=FULL` (owner decision 2026-11-28).
Retained data can still hold client first names that the purge has not
blanked (the purge only runs while the plugin is installed). Decide the number
and whether a retained copy must be blanked first.

## Q3: shop time zone

The shop zone is the plugin's own setting (default `Europe/London`, GB-only
manifest). The host exposes only a UTC offset (`device_timezone_get`), not an
IANA zone, so the zone cannot be seeded from the till. The currency is seeded
from `shop.context.v2` the first time a page opens.

## Q4: concurrent writers

Each event is one wasm instance; the repository mutex orders writes inside one
event only. The conflict check and the insert are two host calls, so two
simultaneous operator posts could double-book the same slot. The unique
`supersedes_id` index closes the double-move case. Closing the booking case
needs either a single guarded `INSERT ... WHERE NOT EXISTS` statement or the
host serialising events per plugin; ask the architect whether the host already
does the latter.

