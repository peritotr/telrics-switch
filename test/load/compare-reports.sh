#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Checks that the summary report read from ClickHouse equals the one read
# from PostgreSQL, for every grouping. Needs the lab and an API on 58080
# started with CLICKHOUSE_URL (make api-run); starts a second API without it.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
set -a; . test/seed/dev.env; set +a
DATABASE_URL=postgres://softswitch:lab-only@127.0.0.1:56432/softswitch LISTEN=127.0.0.1:58079 go run ./cmd/api >/dev/null 2>&1 &
# A third one lists call records from ClickHouse.
DATABASE_URL=postgres://softswitch:lab-only@127.0.0.1:56432/softswitch LISTEN=127.0.0.1:58078 METRICS_LISTEN= \
  CLICKHOUSE_URL=http://softswitch:lab-only@127.0.0.1:58123/softswitch CDR_LIST_SOURCE=clickhouse go run ./cmd/api >/dev/null 2>&1 &
trap 'for port in 58079 58078; do lsof -ti tcp:$port | xargs kill 2>/dev/null; done' EXIT
for port in 58079 58078; do
  for _ in $(seq 30); do curl -fsS -o /dev/null http://127.0.0.1:$port/healthz 2>/dev/null && break; sleep 1; done
done
KEY="$BOOTSTRAP_API_KEY" python3 - <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request
def get(port, g):
    r = urllib.request.Request(f"http://127.0.0.1:{port}/v1/reports/summary?group_by={g}&from=2020-01-01T00:00:00Z&to=2100-01-01T00:00:00Z",
                               headers={"Authorization": "Bearer " + os.environ["KEY"]})
    return json.load(urllib.request.urlopen(r))
failed = False
for g in ["client", "vendor", "prefix", "day"]:
    a, b = get(58080, g), get(58079, g)
    same = a == b
    failed |= not same
    print(("PASS" if same else "FAIL"), f" report by {g}: {len(a)} rows from ClickHouse, {len(b)} from PostgreSQL")

# The list of call records, page by page and filter by filter: the same
# records in the same order with the same contents, times written alike
# (T-24). Records from before amounts in the account's currency and delays
# were kept count at their own amounts and no delay; an empty prefix and
# none are the same.
def records(port, query):
    r = urllib.request.Request(f"http://127.0.0.1:{port}/v1/cdrs?{query}", headers={"Authorization": "Bearer " + os.environ["KEY"]})
    rows = json.load(urllib.request.urlopen(r))
    for row in rows:
        row["sell_base"] = row["sell_amount"] if row.get("sell_base") is None else row["sell_base"]
        row["buy_base"] = row["buy_amount"] if row.get("buy_base") is None else row["buy_base"]
        row["pdd_ms"] = row.get("pdd_ms") or 0
        for k in ("sell_prefix", "buy_prefix"):  # PostgreSQL has both "no prefix" and an empty one
            row[k] = row.get(k) or None
    return rows
first = records(58079, "limit=1&sip_code=200")[0]
# A call delivered to a client's trunk, when the lab has one (make
# test-routing makes them); its list must match as well.
delivered = [r for r in records(58079, "limit=1000") if r.get("dst_trunk_id")]
if not delivered:
    print("NOTE  no call delivered to a client's trunk among the latest 1000 records; run make test-routing for one")
extra = [("one destination trunk", f"limit=100&dst_trunk_id={delivered[0]['dst_trunk_id']}")] if delivered else []
for what, query in extra + [
    ("latest page", "limit=200"), ("second page", "limit=50&offset=50"),
    ("one client, answered", f"limit=200&client_id={first['client_id']}&sip_code=200"),
    ("one vendor", f"limit=100&vendor_id={first['vendor_id']}"), ("one connection", f"limit=100&connection_id={first['connection_id']}"),
    ("one call", "call_id=" + urllib.parse.quote(first["call_id"])), ("number prefix", "limit=100&dst=" + first["dst"][:4]),
    ("caller prefix", "limit=100&src=" + first["src"][:2]), ("refused calls", "limit=100&sip_code=403"),
    ("time range", "limit=300&from=2026-11-22T12:00:00Z&to=2026-11-22T13:00:00Z"), ("no match", "client_id=999999"),
    ("not a number", "client_id=abc"),
]:
    a, b = records(58078, query), records(58079, query)
    same = a == b
    failed |= not same
    print(("PASS" if same else "FAIL"), f" call records, {what}: {len(a)} from ClickHouse, {len(b)} from PostgreSQL")
    if not same:
        for x, y in zip(a, b):
            if x != y:
                print("   first difference:", {k: (x.get(k), y.get(k)) for k in set(x) | set(y) if x.get(k) != y.get(k)})
                break
# A single record (GET /v1/cdrs/{id}) is the row the list shows. An
# archived one comes from ClickHouse in the same form: make test-archive.
for row in records(58078, "limit=5"):
    r = urllib.request.Request(f"http://127.0.0.1:58079/v1/cdrs/{row['id']}", headers={"Authorization": "Bearer " + os.environ["KEY"]})
    one = json.load(urllib.request.urlopen(r))
    one["sell_base"] = one["sell_amount"] if one.get("sell_base") is None else one["sell_base"]
    one["buy_base"] = one["buy_amount"] if one.get("buy_base") is None else one["buy_base"]
    one["pdd_ms"] = one.get("pdd_ms") or 0
    for k in ("sell_prefix", "buy_prefix"):
        one[k] = one.get(k) or None
    same = one == row
    failed |= not same
    print(("PASS" if same else "FAIL"), f" call record {row['id']}: the single record equals its row in the list from ClickHouse")
for query in ["bogus=1", "limit=-1", "from=yesterday"]:
    codes = []
    for port in (58078, 58079):
        try:
            urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{port}/v1/cdrs?{query}", headers={"Authorization": "Bearer " + os.environ["KEY"]}))
            codes.append(200)
        except urllib.error.HTTPError as e:
            codes.append(e.code)
    same = codes[0] == codes[1] == 400
    failed |= not same
    print(("PASS" if same else "FAIL"), f" call records, bad request {query}: {codes}")
print("RESULT:", "FAIL" if failed else "PASS")
sys.exit(failed)
PY
