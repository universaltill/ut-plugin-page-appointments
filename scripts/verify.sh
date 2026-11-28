#!/usr/bin/env bash
# The full local gate. Run it before every push; CI only re-runs this script.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> gofmt"
fmt_out=$(gofmt -l .)
if [ -n "$fmt_out" ]; then echo "gofmt found unformatted files:" >&2; echo "$fmt_out" >&2; exit 1; fi

echo "==> go vet (host)"
go vet ./...
echo "==> go vet (wasip1)"
GOOS=wasip1 GOARCH=wasm go vet ./...

echo "==> go test -race"
out=$(go test -race -count=1 -cover ./... 2>&1) || { echo "$out"; exit 1; }
echo "$out"
for pkg in src/engine src/money; do
  pct=$(echo "$out" | awk -v p="/$pkg" 'index($2, p) && $0 ~ /coverage:/ { for (i=1;i<=NF;i++) if ($i=="coverage:") { sub("%","",$(i+1)); print $(i+1) } }')
  if [ -z "$pct" ]; then echo "no coverage figure for $pkg" >&2; exit 1; fi
  awk -v c="$pct" -v p="$pkg" 'BEGIN { if (c+0 < 90.0) { printf "coverage of %s is %s%% (< 90%%)\n", p, c > "/dev/stderr"; exit 1 } }'
done

echo "==> build"
scripts/build.sh
echo "==> validate"
scripts/validate.sh

echo "==> secret scan"
hits=$(git ls-files -co --exclude-standard | grep -v '^scripts/verify.sh$' | while IFS= read -r f; do
  [ -f "$f" ] || continue
  grep -HnE -e '-----BEGIN .*PRIVATE KEY-----' -e 'ghp_[A-Za-z0-9]{20,}' -e 'AKIA[0-9A-Z]{16}' -e 'xox[bap]-' "$f" || true
  case "$f" in .env*|*/.env*) grep -HnE '(KEY|SECRET|TOKEN|PASSWORD)=[^ ]+' "$f" || true ;; esac
done)
if [ -n "$hits" ]; then echo "possible secret(s) found:" >&2; echo "$hits" >&2; exit 1; fi

echo "==> package"
scripts/package.sh

echo "verify: OK"
