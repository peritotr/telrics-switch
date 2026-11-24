#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
#
# Checks the example network-layer rules (deploy/prod/nftables) inside a
# throwaway Debian container (make test-nft): syntax with `nft -c` in both
# modes, loading and reloading in the container's own network namespace
# (the host's rules are never touched), and known-sources.sh against a stub
# of the API: paging, which trunks and vendors count, name resolution,
# idempotence, --apply, refusals that leave the file alone, and that the API
# key is never printed. Needs network access to Debian's mirrors for nftables.
set -eu

src=/src/deploy/prod/nftables
rules=$src/softswitch.nft
sync=$src/known-sources.sh
w=$(mktemp -d)
pass=0

ok() { pass=$((pass + 1)); echo "ok   $*"; }
no() { echo "FAIL $*" >&2; exit 1; }

echo "installing nftables, curl, jq, python3 ..."
apt-get update -qq >/dev/null 2>&1 || no "apt-get update (network to the Debian mirrors?)"
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends nftables curl jq python3 ca-certificates >/dev/null 2>&1 ||
	no "apt-get install nftables curl jq python3"
nft --version

# ---- syntax -------------------------------------------------------------------
nft -c -f "$rules" || no "nft -c softswitch.nft"
ok "nft -c softswitch.nft (rate-limit mode)"
grep -c '^[[:space:]]*#known-only drop$' "$rules" | grep -qx 2 || no "expected two #known-only lines"
sed 's/^\([[:space:]]*\)#known-only drop$/\1drop/' "$rules" >"$w/known-only.nft"
grep -c '^[[:space:]]*drop$' "$w/known-only.nft" | grep -qx 2 || no "known-only mode: two drop lines expected"
nft -c -f "$w/known-only.nft" || no "nft -c known-only mode"
ok "nft -c known-only mode"

# ---- load in this container's namespace ---------------------------------------
nft -f "$rules" || no "load softswitch.nft"
nft -f "$rules" || no "reload softswitch.nft"
nft list chain inet softswitch input | grep -q 'policy drop' || no "input policy"
ok "load and reload"

# ---- known-sources.sh against a stub API ----------------------------------------
token=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
printf '%s\n' "$token" >"$w/key"
mkdir "$w/data"
cat >"$w/stub.py" <<'EOF'
import http.server, json, sys, urllib.parse
token, data = open(sys.argv[1]).read().strip(), sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer " + token:
            self.send_response(401); self.end_headers(); self.wfile.write(b'{"error":"unauthorized"}'); return
        u = urllib.parse.urlparse(self.path)
        name = {"/v1/client-trunks": "trunks", "/v1/vendor-connections": "vendors"}.get(u.path)
        if not name:
            self.send_response(404); self.end_headers(); return
        q = urllib.parse.parse_qs(u.query)
        limit, offset = int(q["limit"][0]), int(q["offset"][0])
        rows = json.load(open(f"{data}/{name}.json"))
        body = json.dumps(rows[offset:offset + limit]).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(body)
http.server.HTTPServer(("127.0.0.1", 18080), H).serve_forever()
EOF
cat >"$w/data/trunks.json" <<'EOF'
[
 {"id": 1, "enabled": true, "auth_type": "ip", "addresses": ["203.0.113.10", "198.51.100.0/24", "2001:db8:12::/48"]},
 {"id": 2, "enabled": false, "auth_type": "ip", "addresses": ["192.0.2.50"]},
 {"id": 3, "enabled": true, "auth_type": "digest", "addresses": null},
 {"id": 4, "enabled": true, "auth_type": "ip", "addresses": ["203.0.113.10", "203.0.113.11"]}
]
EOF
cat >"$w/data/vendors.json" <<'EOF'
[
 {"id": 1, "enabled": true, "host": "vendor.test"},
 {"id": 2, "enabled": true, "host": "192.0.2.9"},
 {"id": 4, "enabled": true, "host": "2001:db8:99::7"},
 {"id": 3, "enabled": false, "host": "192.0.2.200"}
]
EOF
# vendor.test: inside a trunk's network (auto-merge). Names are resolved for
# the families the host has addresses in (getaddrinfo's AI_ADDRCONFIG): this
# container has no IPv6 address, so the IPv6 vendor is given as an address.
echo "198.51.100.77 vendor.test" >>/etc/hosts
python3 "$w/stub.py" "$w/key" "$w/data" &
stub=$!
trap 'kill $stub 2>/dev/null || :; rm -rf "$w"' EXIT
i=0
until curl -s -o /dev/null http://127.0.0.1:18080/; do
	i=$((i + 1)); [ $i -lt 50 ] || no "stub API did not start"; sleep 0.1
done

export SOFTSWITCH_API=http://127.0.0.1:18080 SOFTSWITCH_API_KEY_FILE="$w/key" PAGE_SIZE=2
out=$w/etc/known-sources.nft
mkdir -p "$w/etc"
run() { sh "$sync" "$@" >"$w/log" 2>&1; }
nokey() { ! grep -q "$token" "$w/log" || no "the API key was printed: $1"; }

run "$out" || { cat "$w/log"; no "first run"; }
nokey "first run"
grep -q 'updated (5 IPv4, 2 IPv6 entries)' "$w/log" || { cat "$w/log"; no "first run counts"; }
for a in 203.0.113.10 203.0.113.11 198.51.100.0/24 198.51.100.77 192.0.2.9 2001:db8:12::/48 2001:db8:99::7; do
	grep -q "[{ ]$a[,} ]" "$out" || { cat "$out"; no "missing $a"; }
done
for a in 192.0.2.50 192.0.2.200; do
	! grep -q "$a" "$out" || no "disabled entry $a listed"
done
ok "trunks and vendors over several pages; disabled ones and password trunks left out"

nft -c -f /dev/stdin <<EOF || no "nft -c of the rules with the generated sets"
$(cat "$rules")
$(cat "$out")
EOF
ok "nft -c of softswitch.nft followed by the generated file"

inode=$(stat -c %i "$out")
run "$out" || no "second run"
grep -q unchanged "$w/log" || { cat "$w/log"; no "second run should say unchanged"; }
[ "$(stat -c %i "$out")" = "$inode" ] || no "unchanged file was rewritten"
ok "idempotent: second run leaves the file alone"

run --apply "$out" || { cat "$w/log"; no "--apply"; }
nokey "--apply"
nft list set inet softswitch known_v4 >"$w/set"
grep -q 203.0.113.10 "$w/set" && grep -q 198.51.100.0/24 "$w/set" && grep -q 192.0.2.9 "$w/set" || { cat "$w/set"; no "set after --apply"; }
nft list set inet softswitch known_v6 | grep -q 2001:db8:12::/48 || no "IPv6 set after --apply"
run --apply "$out" || no "--apply twice"
ok "--apply loads the sets, and again without error"

cp "$out" "$w/before"
cmp_left() { cmp -s "$w/before" "$out" || no "$1 changed the file"; }

printf 'wrong-key\n' >"$w/badkey"
! SOFTSWITCH_API_KEY_FILE="$w/badkey" run "$out" || no "wrong key accepted"
nokey "wrong key"
grep -q 'left as it was' "$w/log" || no "wrong key: message"
cmp_left "wrong key"
printf 'a"b\n' >"$w/oddkey"
! SOFTSWITCH_API_KEY_FILE="$w/oddkey" run "$out" || no "key with a quote accepted"
cmp_left "odd key"
ok "wrong or malformed key: refused, file untouched, key not printed"

cp "$w/data/vendors.json" "$w/vendors.keep"
echo '[{"id": 9, "enabled": true, "host": "nowhere.invalid"}]' >"$w/data/vendors.json"
! run "$out" || no "unresolvable vendor accepted"
grep -q 'vendor connection 9' "$w/log" || { cat "$w/log"; no "unresolvable: message"; }
cmp_left "unresolvable vendor"
echo '[{"id": 8, "enabled": true, "host": "x;flush ruleset"}]' >"$w/data/vendors.json"
! run "$out" || no "odd host accepted"
cmp_left "odd host"
ok "vendor host that does not resolve or is not a name: refused, file untouched"

cp "$w/data/trunks.json" "$w/trunks.keep"
echo '[{"id": 1, "enabled": true, "auth_type": "ip", "addresses": ["10.0.0.1 }; flush ruleset; {"]}]' >"$w/data/trunks.json"
echo '[]' >"$w/data/vendors.json"
! run "$out" || no "odd address accepted"
cmp_left "odd address"
echo '[]' >"$w/data/trunks.json"
! run "$out" || no "empty list accepted"
grep -q 'allow-empty' "$w/log" || no "empty: message"
cmp_left "empty list"
run --allow-empty --apply "$out" || { cat "$w/log"; no "--allow-empty"; }
! grep -q 'add element' "$out" || no "empty file has elements"
! nft list set inet softswitch known_v4 | grep -q 203.0.113.10 || no "set not emptied"
ok "malformed address refused; empty list refused unless --allow-empty, which empties the sets"

cp "$w/trunks.keep" "$w/data/trunks.json"
cp "$w/vendors.keep" "$w/data/vendors.json"
printf '# monitoring\n  100.64.0.5  \n\n2001:db8:aa::1 # backup switch\n' >"$w/extra"
KNOWN_EXTRA="$w/extra" run --apply "$out" || { cat "$w/log"; no "KNOWN_EXTRA"; }
grep -q 100.64.0.5 "$out" && grep -q 2001:db8:aa::1 "$out" || no "KNOWN_EXTRA entries"
ok "KNOWN_EXTRA adds addresses"

nft -f "$rules" || no "reload after sync"
! nft list set inet softswitch known_v4 | grep -q 203.0.113.10 || no "reload should empty the sets"
nft -f "$out" || no "loading the generated file after a reload"
nft list set inet softswitch known_v4 | grep -q 203.0.113.10 || no "sets after reload + load"
ok "reloading the rules empties the sets; loading the generated file refills them"

echo "all $pass checks passed"
