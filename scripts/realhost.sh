#!/usr/bin/env bash
# Runs the packaged plugin against the REAL till host (ut-docs#7208, #7113,
# #7114): the till's manifest validator, install-time migrations, the wazero
# runtime with the real db_exec/db_query/view_query host functions, backup and
# restore, and keep_years retention. Needs a universal-till checkout at or
# after the plugin-DB merge (ABI 2, ut-docs#3157) and Go.
#   scripts/realhost.sh /path/to/universal-till
# The test file is copied into the checkout, run, and removed again; nothing
# is committed there. Not part of verify.sh (it needs the till repo).
set -euo pipefail
cd "$(dirname "$0")/.."
TILL=${1:?usage: scripts/realhost.sh /path/to/universal-till}
[ -f "$TILL/internal/plugins/plugin_db_install.go" ] || { echo "not a universal-till checkout with the plugin DB: $TILL" >&2; exit 1; }
scripts/build.sh
scripts/package.sh
ID=$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")
VERSION=$(python3 -c "import json;print(json.load(open('manifest.json'))['version'])")
ARTIFACT="$PWD/dist/${ID}_${VERSION}_universal.tar.gz"
DEST="$TILL/internal/plugins/zz_appointments_realhost_test.go"
trap 'rm -f "$DEST"' EXIT
cp scripts/realhost/appointments_realhost_test.go.txt "$DEST"
(cd "$TILL" && APPT_ARTIFACT="$ARTIFACT" go test ./internal/plugins -run 'TestAppointmentsRealHost_7208' -count=1 -v)
