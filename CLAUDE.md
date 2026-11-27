# ut-plugin-page-appointments — rules

`page`-type plugin for Universal Till: own appointment calendar (ADR-0221,
Proposed). ADRs are binding: ADR-0001 (runtime wasm), ADR-0002 (type
taxonomy), ADR-0009 (repo naming), ADR-0086 (the main till is the sole
writer). Manifest contract: ut-docs `reference/plugin-manifest.md`.

## Layout
- `src/engine` — pure scheduling core (availability, conflicts, state machine,
  walk-in queue, deposit arithmetic). `src/money` — vendored copy of core's
  `internal/money` (source file and commit are named in its package comment).
- `src/main.go` — WASI command (`//go:build wasip1`); imports `time/tzdata`
  because a wasip1 guest has no system zoneinfo. It does not call the engine yet.

## Rules
- Purity (enforced by `src/engine/purity_test.go`): no `time.Now` (inject a
  `Clock` or pass `now`), and no net/os/io/rand/sync/database imports in
  `src/engine` and `src/money`. No map-iteration order in output.
- Money: every amount is `money.Money`; raw `int64` only inside `money`.
  Deposit allocation uses 128-bit math (`math/bits`), never `int64` products.
- The engine persists and publishes nothing; the main till writes (ADR-0086).
- Stdlib only; no third-party dependency.
- Error sentinels have stable wire codes (`errors_test.go`).

## Gate
`scripts/verify.sh` is the full local gate (gofmt, vet host + wasip1, race
tests with >= 90% coverage on engine and money, build, validate, secret scan,
package). Run it before every push. CI re-runs it on GitHub-hosted runners
only; it is not the test runner.

## Release
No release workflow and no tag until the owner accepts ADR-0221; the first
release slice adds `release.yml`, `auto-tag-release.yml` and the version-bump
guard. Never publish by hand. Commits: author is the developer; AI agents go
in a `Co-Authored-By:` trailer only.
