#!/usr/bin/env bash
# Validates the page-plugin manifest: marketplace-required fields
# (id/name/semver/permissions/locales), ADR-0001 runtime, type page, and the
# rules of this plugin: the permission set is exactly the declared one, no
# net:* permission, the db block points at shipped migrations, the schedule
# and published event names carry the plugin id, every entry label and the
# retention reason are keys of locales/en.json, and the migrations are
# numbered 1..N. The till itself re-validates all of it at install; the
# real-host test (scripts/realhost.sh) runs that validator.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import json, os, re, sys
m = json.load(open("manifest.json"))
errs = []
if not re.match(r'^[a-z0-9]+([.-][a-z0-9]+)*$', m.get("id","")): errs.append("bad id")
if not m.get("name"): errs.append("missing name")
if not re.match(r'^\d+\.\d+\.\d+', m.get("version","")): errs.append("bad version")
if not m.get("permissions"): errs.append("missing permissions")
if not m.get("locales"): errs.append("missing locales")
if m.get("runtime") not in ("none", "wasm"): errs.append("runtime must be 'none' or 'wasm' (ADR-0001)")
if m.get("runtime") == "wasm":
    ep = (m.get("entrypoint") or "").lstrip("./")
    if not ep.endswith(".wasm"): errs.append("wasm runtime needs a .wasm entrypoint")
    elif not os.path.isfile(ep): errs.append(f"module not found: {ep} (run scripts/build.sh)")
if m.get("device_arch") != "any": errs.append("device_arch must be 'any'")
if m.get("canonical_type") != "page": errs.append("canonical_type must be 'page'")
want = ["events:receive", "ui:page", "db:own", "schedule", "view:sales", "view:inventory", "view:shop"]
if sorted(m.get("permissions", [])) != sorted(want): errs.append(f"permissions must be exactly {want}")
if any(str(p).startswith("net:") for p in m.get("permissions", [])): errs.append("no net:* permission")
if sorted(m.get("views_used", [])) != ["catalog.items.v1", "sales.receipts.v1", "shop.context.v2"]: errs.append("views_used mismatch")
pid = m.get("id", "")
for s in m.get("schedules", []):
    if not s.get("event", "").startswith(pid + "."): errs.append("schedule event must start with the plugin id")
    if s.get("every_s", 0) < 30: errs.append("schedule every_s < 30")
db = m.get("db") or {}
mig = db.get("migrations", "")
if not mig or mig.startswith("/") or ".." in mig: errs.append("db.migrations must be a relative dir inside the plugin")
ret = m.get("retention") or {}
if not isinstance(ret.get("keep_years"), int) or ret["keep_years"] < 0: errs.append("retention.keep_years must be an int >= 0")
en = json.load(open("locales/en.json"))
if ret.get("reason_key") not in en: errs.append("retention.reason_key is not a key of locales/en.json")
for e in m.get("entries", []):
    if e.get("label") not in en: errs.append(f"entry {e.get('key')}: label {e.get('label')!r} is not a key of locales/en.json")
    if e.get("type") == "page" and not str(e.get("route", "")).startswith("/plugin/"): errs.append(f"page {e.get('key')}: route must start with /plugin/")
hooks = {h.get("event") for h in m.get("hooks", [])}
if not {"ui.view.ask", "ui.action.ask"} <= hooks: errs.append("hooks must include ui.view.ask and ui.action.ask")
files = sorted(f for f in os.listdir("src/store/migrations") if f.endswith(".sql"))
for i, f in enumerate(files, 1):
    if not f.startswith("%04d_" % i): errs.append(f"migrations must be numbered 1..N: {f}")
if errs:
    print("FAIL: " + "; ".join(errs)); sys.exit(1)
print(f"ok {m['id']} v{m['version']}")
PY
