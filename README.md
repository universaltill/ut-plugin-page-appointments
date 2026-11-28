# ut-plugin-page-appointments

A `page`-type plugin (ADR-0009 naming: `ut-plugin-{type}-{name}`) for Universal Till:
an own appointment calendar for service shops (ADR-0221).

Status: wired to the till's plugin-owned database (ABI 2, `db:own`; ut-docs#7208).
Day calendar, walk-in queue, check-in, completion with a paid receipt, name
erasure and the staff, services, hours and settings pages run inside the
till. Bookings live in `plugin-data/<id>/plugin.db`; migrations ship in the
artifact (`db/migrations`) and the till applies them at install. Nothing is
published until ADR-0221 is accepted and the owner enables publishing.

- `scripts/verify.sh`: the full local gate (also what CI runs).
- `scripts/realhost.sh /path/to/universal-till`: runs the packaged artifact
  against the real till host (manifest validator, install migrations, wasm
  guest, backup and restore, `keep_years` retention).
- Not yet: provider calendars, deposits, reminders, the basket seam (the till
  does not yet echo a per-line `ref`, so completion is by picking the receipt).
