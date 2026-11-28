#!/usr/bin/env bash
# Packages the plugin into the canonical marketplace release artifact:
#   dist/<plugin-id>_<version>_universal.tar.gz
# A WASI module (bin/plugin.wasm, scripts/build.sh) runs on every till os/arch
# => one universal artifact. manifest.json sits at the archive root; no "./"
# members (the POS importer rejects them as path traversal). The artifact also
# carries db/migrations/*.sql (the host applies them at install, from the
# digest-verified artifact: the manifest's db.migrations) and locales/*.json
# (the till reads view text from the INSTALLED bundle).
set -euo pipefail
cd "$(dirname "$0")/.."

scripts/validate.sh

ID=$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")
VERSION=$(python3 -c "import json;print(json.load(open('manifest.json'))['version'])")
OUT="dist/${ID}_${VERSION}_universal.tar.gz"
mkdir -p dist

# Stage the shipped tree: the migrations live in src/store/migrations (embedded
# for tests) and are copied, never edited, into db/migrations; locales ship
# without the Go package file.
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/db/migrations" "$STAGE/locales" "$STAGE/bin"
cp src/store/migrations/*.sql "$STAGE/db/migrations/"
cp locales/*.json "$STAGE/locales/"
cp bin/plugin.wasm "$STAGE/bin/"
cp manifest.json README.md "$STAGE/"
[ -f LICENSE ] && cp LICENSE "$STAGE/"
# COPYFILE_DISABLE stops macOS tar shipping AppleDouble ._* junk (the
# marketplace bundle-hygiene gate rejects it).
COPYFILE_DISABLE=1 tar -czf "$OUT" -C "$STAGE" manifest.json README.md bin db locales $( [ -f LICENSE ] && echo LICENSE )
# cd into dist/ first so the recorded checksum names the bare filename, not
# "dist/<file>" — a self-hoster downloading the artifact + .sha256 pair into
# one directory runs `sha256sum -c`, which fails to find a "dist/..." path
# (ut-docs#166).
if command -v sha256sum >/dev/null 2>&1; then (cd "$(dirname "$OUT")" && sha256sum "$(basename "$OUT")") > "${OUT}.sha256"; else (cd "$(dirname "$OUT")" && shasum -a 256 "$(basename "$OUT")") > "${OUT}.sha256"; fi
echo "packaged $OUT"
