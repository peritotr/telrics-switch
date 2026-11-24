#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Fills a lab API with demo data for the panel: tariffs, vendors, a routing
plan, clients, payments and ten days of call records. Safe to run once on an
empty database; names are unique, so a second run fails on the first insert.

    set -a; . test/seed/dev.env; set +a; python3 test/seed/seed.py
"""
import json, os, random, sys, urllib.request
from datetime import datetime, timedelta, timezone

API = os.environ.get("API_URL", "http://127.0.0.1:58080")
KEY = os.environ["BOOTSTRAP_API_KEY"]
random.seed(7)


def call(method, path, body=None, raw=None):
    data = raw.encode() if raw is not None else json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method, headers={"Authorization": "Bearer " + KEY})
    try:
        with urllib.request.urlopen(req) as resp:
            text = resp.read()
            return json.loads(text) if text else None
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode()}")


def post(path, body):
    return call("POST", path, body)


def rates(tariff, sheet):
    call("POST", f"/v1/tariffs/{tariff}/rates/import?effective_from=2026-01-01T00:00:00Z", raw="prefix,price,connect_fee,initial,increment\n" + sheet)


sell = post("/v1/tariffs", {"name": "TR-Wholesale-Premium", "kind": "sell"})["id"]
sell_eu = post("/v1/tariffs", {"name": "EU-Standard", "kind": "sell"})["id"]
rates(sell, "90,0.0125,0,1,1\n9053,0.0142,0,1,1\n9054,0.0148,0,1,1\n9050,0.0139,0,1,1\n44,0.0090,0,1,1\n447,0.0160,0,1,1\n49,0.0110,0,1,1\n4915,0.0450,0,60,60\n33,0.0105,0,1,1\n1,0.0065,0,6,6\n971,0.1650,0,60,60\n")
rates(sell_eu, "44,0.0098,0,1,1\n49,0.0120,0,1,1\n33,0.0112,0,1,1\n34,0.0109,0,1,1\n39,0.0115,0,1,1\n")

vendors = {}
for name, sheet, host in [
    ("Örnek Transit", "90,0.0098\n9053,0.0118\n9054,0.0121\n9050,0.0116\n", "sbc1.ornek-transit.example"),
    ("Demo Carrier EU", "90,0.0104\n9053,0.0112\n44,0.0071\n447,0.0128\n49,0.0086\n4915,0.0380\n33,0.0081\n1,0.0048\n", "gw.demo-carrier.example"),
    ("Demo Global Voice", "44,0.0068\n447,0.0131\n49,0.0090\n33,0.0079\n1,0.0051\n971,0.1420\n", "sip.demo-global.example"),
]:
    buy = post("/v1/tariffs", {"name": name + " alış", "kind": "buy"})["id"]
    rates(buy, "\n".join(line + ",0,1,1" for line in sheet.strip().split("\n")) + "\n")
    v = post("/v1/vendors", {"name": name, "tariff_id": buy, "email": "noc@" + host.split(".", 1)[1]})["id"]
    conns = [post("/v1/vendor-connections", {"vendor_id": v, "name": f"{host.split('.')[1]}-{s}", "host": host, "port": p, "transport": t, "max_calls": 2000, "max_cps": 100})["id"]
             for s, p, t in [("a", 5060, "udp"), ("b", 5061, "tls")]]
    vendors[name] = (v, conns)

plan = post("/v1/routing-plans", {"name": "LCR-Global", "policy": "lcr"})["id"]
post("/v1/routing-plans", {"name": "Öncelik-TR", "policy": "priority", "allow_loss": True})
for prefix, names in {"90": ["Örnek Transit", "Demo Carrier EU"], "44": ["Demo Carrier EU", "Demo Global Voice"], "49": ["Demo Carrier EU", "Demo Global Voice"],
                      "33": ["Demo Global Voice", "Demo Carrier EU"], "1": ["Demo Carrier EU", "Demo Global Voice"], "971": ["Demo Global Voice"]}.items():
    for n, name in enumerate(names):
        post("/v1/routing-entries", {"routing_plan_id": plan, "prefix": prefix, "connection_id": vendors[name][1][0], "priority": 10 * (n + 1)})

clients = []
for n, (name, mode, limit, tariff, deposit) in enumerate([
    ("Örnek Telekom A.Ş.", "prepaid", 0, sell, 12500), ("Örnek Toptan Ses Ltd.", "postpaid", 25000, sell, 4000),
    ("Demo VoIP S.L.", "prepaid", 0, sell_eu, 1800), ("Örnek Çağrı Merkezi", "prepaid", 0, sell, 350),
]):
    c = post("/v1/clients", {"name": name, "billing_mode": mode, "credit_limit": limit * 1_000_000, "tariff_id": tariff, "routing_plan_id": plan,
                             "max_calls": 600, "max_cps": 40, "email": f"noc@client{n + 1}.example", "tax_id": str(1234567890 + n)})["id"]
    post("/v1/client-trunks", {"client_id": c, "name": "main", "addresses": [f"203.0.113.{10 + n}", f"192.0.2.{16 * n}/28"]})
    post("/v1/client-trunks", {"client_id": c, "name": "backup", "addresses": [f"198.51.100.{10 + n}"], "tech_prefix": "777"})
    post("/v1/payments", {"party_type": "client", "party_id": c, "amount": deposit * 1_000_000, "reference": f"HVL-{1000 + n}", "note": "Açılış ödemesi"})
    clients.append((c, tariff))
post("/v1/clients", {"name": "Demo Carrier Services", "status": "suspended", "tariff_id": sell, "routing_plan_id": plan})

dests = {sell: ["90532", "90533", "90542", "90505", "90212", "44207", "44791", "49151", "4930", "331", "1212", "97150"], sell_eu: ["44207", "4930", "331", "3491", "3906"]}
route = {"90": "Örnek Transit", "44": "Demo Carrier EU", "49": "Demo Carrier EU", "33": "Demo Global Voice", "1": "Demo Carrier EU", "97": "Demo Global Voice", "34": "Demo Carrier EU", "39": "Demo Carrier EU"}
now = datetime.now(timezone.utc)
batch, total = [], 0
for i in range(2400):
    client, tariff = random.choice(clients)
    dst = random.choice(dests[tariff]) + "".join(random.choice("0123456789") for _ in range(7))
    start = now - timedelta(seconds=random.randint(60, 10 * 86400))
    code = random.choices([200, 486, 480, 487, 503, 404], [58, 14, 9, 10, 6, 3])[0]
    rec = {"call_id": f"seed-{i:05d}@203.0.113.10", "client_id": client, "connection_id": vendors[route.get(dst[:2]) or route[dst[:1]]][1][0],
           "src": "90850" + "".join(random.choice("0123456789") for _ in range(7)), "src_ip": "203.0.113.10", "dst": dst,
           "start_at": start.isoformat(), "sip_code": code}
    ring = timedelta(seconds=random.randint(2, 20))
    if code == 200:
        rec["answer_at"] = (start + ring).isoformat()
        rec["end_at"] = (start + ring + timedelta(seconds=int(random.expovariate(1 / 140)) + 3)).isoformat()
    else:
        rec["end_at"] = (start + ring).isoformat()
    batch.append(rec)
    if len(batch) == 400:
        total += post("/v1/cdrs", batch)["inserted"]
        batch = []
post("/v1/payments", {"party_type": "vendor", "party_id": vendors["Demo Carrier EU"][0], "amount": 5_000_000, "reference": "SWIFT-2291", "note": "Kısmi ödeme"})
print(f"seeded: {len(clients) + 1} clients, {len(vendors)} vendors, {total} call records")
