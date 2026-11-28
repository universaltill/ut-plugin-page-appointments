# ut-plugin-page-appointments — rules

`page`-type plugin for Universal Till: own appointment calendar (ADR-0221,
Proposed). ADRs are binding: ADR-0001 (runtime wasm), ADR-0002 (type
taxonomy), ADR-0009 (repo naming), ADR-0086 (the main till is the sole
writer). Manifest contract: ut-docs `reference/plugin-manifest.md`.

## Layout
- `src/engine` — pure scheduling core (availability, conflicts, state machine,
  walk-in queue, deposit arithmetic). `src/money` — vendored copy of core's
  `internal/money` (source file and commit are named in its package comment).
- `src/store` — repository over a small `DB` interface, migrations
  (`src/store/migrations`, copied to `db/migrations` in the artifact), purge,
  queue, completion outbox. `src/store/memdb` is the test double.
- `src/calendar`, `src/admin`, `src/checkout`, `src/logbook` — views and rules
  (pure over interfaces). `src/adapter` implements `calendar.Store` and
  `checkout.Store` on the repository (crash-safe write order: the host has no
  transaction).
- `src/hostdb` (db_exec/db_query), `src/hostview` (view_query) — the only
  code that calls host functions, through the Go guest SDK. `src/app` routes
  events (`ui.view.ask`, `ui.action.ask`, the schedule tick); it has no build
  tag, so the whole plugin runs natively in tests on the SDK's fake host.
- `src/main.go` — WASI command (`//go:build wasip1`); only plugs the real host
  functions into `app`. Imports `time/tzdata` (a wasip1 guest has no zoneinfo).

## Rules
- Purity (enforced by `src/engine/purity_test.go`): no `time.Now` (inject a
  `Clock` or pass `now`), and no net/os/io/rand/sync/database imports in
  `src/engine` and `src/money`. No map-iteration order in output.
- Money: every amount is `money.Money`; raw `int64` only inside `money`.
  Deposit allocation uses 128-bit math (`math/bits`), never `int64` products.
- The engine persists and publishes nothing; the main till writes (ADR-0086).
- Stdlib only, plus the first-party Go guest SDK
  (`github.com/universaltill/universal-till/sdk/plugin`, itself stdlib-only),
  and only in `src/hostdb`, `src/hostview`, `src/app` and `src/main.go`. The
  engine and money never import it.
- Every statement is a constant in `src/store/sql.go` with numbered
  placeholders; one statement per host call (no BEGIN/COMMIT). Order writes so
  a crash leaves nothing visible or something `Reconcile` repairs.
- Migrations are append-only once merged (the host pins each checksum); add a
  new `NNNN_name.sql`, never edit a shipped one.
- Error sentinels have stable wire codes (`errors_test.go`).

## Gate
`scripts/verify.sh` is the full local gate (gofmt, vet host + wasip1, race
tests with >= 90% coverage on engine and money, build, validate, secret scan,
package). Run it before every push. CI re-runs it on GitHub-hosted runners
only; it is not the test runner.

`scripts/realhost.sh <till checkout>` is the real-host run (needs the till
repo; not part of verify.sh).

## Release
No release workflow and no tag until the owner accepts ADR-0221; the first
release slice adds `release.yml`, `auto-tag-release.yml` and the version-bump
guard. Never publish by hand. Commits: author is the developer; AI agents go
in a `Co-Authored-By:` trailer only.
