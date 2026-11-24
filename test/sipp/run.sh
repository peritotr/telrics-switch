#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Routing scenarios through the lab: places real calls with SIPp and checks
# the call records they leave. Prints PASS/FAIL per case.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
COMPOSE=(docker compose -f "$ROOT/deploy/compose/docker-compose.yml")
# The engine answers only to its token (the lab's, deploy/compose/lab.env);
# its counters at /metrics are open.
. "$ROOT/deploy/compose/lab.env"
engine() { curl -fsS -H "Authorization: Bearer $ENGINE_TOKEN" "$@"; }
# Calls an interrupted run left in the engine would count against the
# limits and balances below (T-29).
"$ROOT/test/lab-clean.sh" || { echo "RESULT: FAIL (the lab is not clean)"; exit 1; }
RUN="t$(date +%s)"
FAILED=0
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ALLOWED_BEFORE=$(curl -fsS http://127.0.0.1:58090/metrics | awk 'index($0, "softswitch_engine_authorize_total{result=\"allowed\"}") == 1 { print $NF }')
STORED_BEFORE=$(curl -fsS http://127.0.0.1:58092/metrics | awk 'index($0, "softswitch_rater_records_total{result=\"stored\"}") == 1 { print $NF }')
# The trace collector's counters run from its start; this run's share is
# what they gained (T-29: drops while ClickHouse was down in an earlier run
# must not fail this one).
trace_counts() { curl -fsS http://127.0.0.1:58093/metrics | awk '$1 == "softswitch_trace_received_total" { r = $2 } $1 == "softswitch_trace_stored_total" { s = $2 } index($1, "softswitch_trace_dropped_total") == 1 { d += $2 } END { printf "%d %d %d", r, s, d }'; }
TRACE_BEFORE=$(trace_counts)

sql() { "${COMPOSE[@]}" exec -T postgres psql -U softswitch -d softswitch -v ON_ERROR_STOP=1 -qtA "$@"; }
reload() { engine -X POST http://127.0.0.1:58090/v1/reload; }
cuts() { engine http://127.0.0.1:58090/v1/stats | sed 's/.*"cut":\([0-9]*\).*/\1/'; }
# A call whose funds run out is ended by the engine a second before they do
# (cutLead) or, when the dialog's limit is that same moment, by the SIP
# core's dialog timer: it counts in whole seconds and fires up to a second
# early, so for a few calls in ten it comes first (T-29). Either ends the
# call within what was held. ends_by: "engine <n> timer <n>" since the marks.
expired() { curl -fsS http://127.0.0.1:58888/metrics | awk '$1 == "opensips_expired_dialogs" { print $2 }'; }
mark_ends() { CUT=$(cuts); EXPIRED=$(expired); }
ended_once() { # ended_once <case>: one call, ended by the engine or the SIP core's limit, by nothing else
  local by_engine=$(( $(cuts) - CUT )) by_timer=$(( $(expired) - EXPIRED ))
  if [ $by_engine -le 1 ] && [ $by_timer -le 1 ] && [ $(( by_engine + by_timer )) -ge 1 ]; then
    echo "PASS  $1: ended by the switch at the end of the funds (engine $by_engine, SIP core's limit $by_timer)"
  else
    echo "FAIL  $1: ended by the switch at the end of the funds: engine $by_engine, SIP core's limit $by_timer"; FAILED=1
  fi
}

# uac <case> <scenario> <number> [sed expression] [extra sipp args...]
uac() {
  local name=$1 scenario=$2 number=$3 edit=${4:-} ; shift; shift; shift; shift || true
  "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c \
    "sed '$edit' /work/test/sipp/$scenario > /tmp/s.xml && exec sipp -sf /tmp/s.xml ${UAC_TARGET:-172.30.0.10:5060} -i 172.30.0.21 \
     -s $number -cid_str '$RUN-$name-%u@%s' -nostdin -m 1 $*" >/dev/null 2>&1
}

# legs <case>: "leg:vendor host:sip code" for every record of the case.
legs() {
  sql -c "select string_agg(c.leg || ':' || coalesce(vc.host, '-') || ':' || c.sip_code, ' ' order by c.leg)
          from cdrs c left join vendor_connections vc on vc.id = c.connection_id where c.call_id like '$RUN-$1-%'"
}

check() { # check <case> <what> <got> <want>
  if [ "$3" == "$4" ]; then echo "PASS  $1: $2"; else echo "FAIL  $1: $2: got '$3', want '$4'"; FAILED=1; fi
}

some() { awk '{ print ($1 > 0) ? "yes" : "no" }'; }
settle() { sleep 2; } # the rater stores records a moment after the call

sql -f - < "$ROOT/test/seed/lab-routing.sql"
sql -c "update clients set max_calls = 0 where name = 'Lab SIPp'; update clients set balance = 1000 where name = 'Lab SIPp low balance'"
reload

# 1. Two vendors refuse, the third answers; every attempt is recorded.
uac failover uac.xml 995551112233 "" -d 2000
check failover "sipp exit" $? 0
settle
check failover "legs" "$(legs failover)" "1:172.30.0.22:500 2:172.30.0.23:503 3:172.30.0.20:200"

# 2. A destination outside the tariff is refused and recorded without a vendor.
uac blocked uac-refused.xml 445551112233 "s/__CODE__/403/"
check blocked "sipp exit" $? 0
settle
check blocked "legs" "$(legs blocked)" "1:-:403"

# The engine's interface is closed. Without its token, or with another one,
# it gives out nothing (not the password hash of a trunk, not the traffic)
# and takes no orders (ending a call, lifting a ban); the SIP core, which
# has the token, goes on placing calls.
ask() { curl -s -o /dev/null -w '%{http_code}' "$@"; } # ask [-H ...] [-X POST -d ...] <url>: the status
refused() { curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_unauthorized_total" { print $2 }'; }
REFUSED=$(refused)
check token "no token: password hash refused" "$(ask -X POST http://127.0.0.1:58090/v1/credentials -d '{"v":1,"username":"lab-digest"}')" 401
check token "no token: call decision refused" "$(ask -X POST http://127.0.0.1:58090/v1/authorize -d '{"v":1,"call_id":"x","src_ip":"172.30.0.21","src":"1","dst":"905551112233"}')" 401
check token "no token: traffic refused" "$(ask http://127.0.0.1:58090/v1/stats) $(ask http://127.0.0.1:58090/v1/live) $(ask http://127.0.0.1:58090/v1/calls)" "401 401 401"
check token "no token: hangup, unban and reload refused" "$(ask -X POST http://127.0.0.1:58090/v1/hangup -d '{"v":1,"call_id":"x"}') $(ask -X POST http://127.0.0.1:58090/v1/unban -d '{"v":1,"src_ip":"172.30.0.21"}') $(ask -X POST http://127.0.0.1:58090/v1/reload)" "401 401 401"
check token "wrong token: refused" "$(ask -H "Authorization: Bearer $ENGINE_TOKEN-not" http://127.0.0.1:58090/v1/stats) $(ask -H "Authorization: Bearer $ENGINE_TOKEN-not" -X POST http://127.0.0.1:58090/v1/credentials -d '{"v":1,"username":"lab-digest"}')" "401 401"
check token "the token as another kind of credential: refused" "$(ask -H "Authorization: Basic $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats) $(ask -H "X-Token: $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats)" "401 401"
check token "with the token: answered" "$(ask -H "Authorization: Bearer $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats)" 200
check token "counters and health check stay open" "$(ask http://127.0.0.1:58090/metrics) $(ask http://127.0.0.1:58090/healthz)" "200 200"
check token "refusals are counted" "$(( $(refused) - REFUSED ))" 12
uac token uac.xml 905551112233 "" -d 1000
check token "calls go on: sipp exit" $? 0
settle
check token "calls go on: legs" "$(legs token)" "1:172.30.0.20:200"
check token "the token is in no log" "$({ docker logs softswitch-lab-opensips-1; docker logs softswitch-lab-route-engine-1; } 2>&1 | grep -cF "$ENGINE_TOKEN")" 0

# 3. An unknown source address is asked to sign in and leaves no record.
"${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c \
  "ip addr add 172.30.0.99/24 dev eth0 && sed 's/__CODE__/401/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.99 -s 905551112233 -cid_str '$RUN-unknown-%u@%s' -m 1 -nostdin" >/dev/null 2>&1
check unknown "sipp exit" $? 0
settle
check unknown "legs" "$(legs unknown)" ""

# 4. With a limit of one call, the second of two simultaneous calls is refused.
sql -c "update clients set max_calls = 1 where name = 'Lab SIPp'"; reload
uac limit uac-limited.xml 905551112233 "s/__REFUSED__/503/" -d 3000 -m 2 -r 2 -l 2
check limit "sipp exit" $? 0
sql -c "update clients set max_calls = 0 where name = 'Lab SIPp'"; reload
settle
check limit "records by code" "$(sql -c "select string_agg(sip_code::text, ' ' order by sip_code) from cdrs where call_id like '$RUN-limit-%'")" "200 503"

# 5. The engine ends a call just before the balance is used up (three
#    seconds here), so the call is never billed past it.
mark_ends
uac cutoff uac-cutoff.xml 555905551112233 ""
check cutoff "switch hung up" $? 0
settle
check cutoff "legs" "$(legs cutoff)" "1:172.30.0.20:200"
DUR=$(sql -c "select duration_sec from cdrs where call_id like '$RUN-cutoff-%'")
if [ "${DUR:-0}" -ge 2 ] && [ "${DUR:-0}" -le 3 ]; then echo "PASS  cutoff: duration ${DUR}s"; else echo "FAIL  cutoff: duration '${DUR}', want 2-3"; FAILED=1; fi
ended_once cutoff
check cutoff "balance not overdrawn" "$(sql -c "select balance >= 0 from clients where name = 'Lab SIPp low balance'")" t

# 6. Calls in progress share the balance: with six seconds in all, the first
#    call holds them and the second, placed meanwhile, is refused.
sql -c "update clients set balance = 2000 where name = 'Lab SIPp low balance'"; reload
uac shared uac-limited.xml 555905551112233 "s/__REFUSED__/402/" -d 3000 -m 2 -r 2 -l 2
check shared "sipp exit" $? 0
settle
check shared "records by code" "$(sql -c "select string_agg(sip_code::text, ' ' order by sip_code) from cdrs where call_id like '$RUN-shared-%'")" "200 402"
#    Once the charge is settled and loaded, what is left can be spent again.
reload
uac rest uac-cutoff.xml 555905551112233 ""
check shared "the rest is usable" $? 0
settle
check shared "balance not overdrawn" "$(sql -c "select balance >= 0 from clients where name = 'Lab SIPp low balance'")" t

# 7a. The engine lists a call in progress and ends it on request.
uac operator uac-cutoff.xml 905551112233 "" &
sleep 2
LIVE=$(engine "http://127.0.0.1:58090/v1/calls?limit=50")
ID=$(echo "$LIVE" | grep -o "\"call_id\":\"$RUN-operator-[^\"]*\"" | cut -d'"' -f4)
check operator "call is listed as answered" "$(echo "$LIVE" | grep -c "$RUN-operator-.*\"answer_at\":\"")" 1
engine -X POST http://127.0.0.1:58090/v1/hangup -d "{\"v\":1,\"call_id\":\"$ID\"}"
wait $!
check operator "ended on request" $? 0
#     The live view saw this run: answered calls with a post-dial delay.
VIEW=$(engine http://127.0.0.1:58090/v1/live)
check live "post-dial delay measured" "$(echo "$VIEW" | grep -c '"country":"TR".*"pdd_samples":[1-9]')" 1

# 6a. Strangers. A known address gets an answer to OPTIONS, a stranger none;
#     a stranger flooding with calls is cut off before the engine sees most
#     of them, and the client's own calls go on.
from_ip() { # from_ip <address> <shell command run inside the caller container>
  "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c "ip addr add $1/24 dev eth0 2>/dev/null; $2" >/dev/null 2>&1
}
from_ip 172.30.0.21 "sed 's/__EXPECT__/answer/' /work/test/sipp/uac-options.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.21 -m 1 -nostdin"
check edge "known address is answered" $? 0
from_ip 172.30.0.99 "sed 's/__EXPECT__/silence/' /work/test/sipp/uac-options.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.99 -m 1 -nostdin"
check edge "stranger's probe gets no answer" $? 0
#     Before the flood the stranger's call is answered (with a challenge);
#     after it, the same call gets nothing: the address is blocked.
stranger_call() { from_ip 172.30.0.98 "sed 's/__CODE__/401/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec timeout 5 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.98 -s 905551112233 -m 1 -nostdin"; }
stranger_call
check edge "stranger's call is challenged before the flood" $? 0
from_ip 172.30.0.98 "sed 's/__CODE__/401/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec timeout 8 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.98 -s 905551112233 -m 600 -r 200 -l 600 -nostdin"
stranger_call
check edge "flooding address is ignored afterwards" $? 124
uac afterflood uac.xml 905551112233 "" -d 500
check edge "the client's calls go on during the block" $? 0

# 6a2. The firewall. The engine has the flooding address on its list of
#      bans, and lifting the ban there makes the SIP core answer it again.
bans() { engine http://127.0.0.1:58090/v1/bans; }
check firewall "the flooding address is on the list of bans" "$(bans | grep -c '"ip":"172.30.0.98","reason":"flood"')" 1
engine -X POST http://127.0.0.1:58090/v1/unban -d '{"v":1,"src_ip":"172.30.0.98"}'
check firewall "the ban is lifted on request" "$(bans | grep -c '"ip":"172.30.0.98"')" 0
stranger_call
check firewall "the address is answered again" $? 0
#      A rule that denies an address: its call gets no answer at all, where
#      a stranger's is at least challenged. A rule that allows one: its
#      probe is answered, where a stranger's is not.
ROOT_ID=$(sql -c "select id from resellers order by id limit 1")
sql -c "insert into firewall_rules (reseller_id, cidr, action) values ($ROOT_ID, '172.30.0.95', 'deny'), ($ROOT_ID, '172.30.0.96/32', 'allow') on conflict do nothing"
reload
from_ip 172.30.0.95 "sed 's/__CODE__/401/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec timeout 5 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.95 -s 905551112233 -m 1 -nostdin"
check firewall "a denied address gets no answer" $? 124
from_ip 172.30.0.96 "sed 's/__EXPECT__/answer/' /work/test/sipp/uac-options.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.96 -m 1 -nostdin"
check firewall "an allowed address is answered" $? 0
#      Denying an address the SIP core already knows (the client's own):
#      the next call is refused, the ones after are ignored, and nothing is
#      recorded. With the rule gone and the ban lifted the client calls again.
sql -c "insert into firewall_rules (reseller_id, cidr, action, note) values ($ROOT_ID, '172.30.0.21', 'deny', 'scenario')"
reload
uac walled uac-refused.xml 905551112233 "s/__CODE__/403/"
check firewall "a known address that is denied is refused" $? 0
"${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c "sed 's/__CODE__/403/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec timeout 5 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.21 -s 905551112233 -cid_str '$RUN-walled2-%u@%s' -m 1 -nostdin" >/dev/null 2>&1
check firewall "and ignored from then on" $? 124
sql -c "delete from firewall_rules where cidr in ('172.30.0.21', '172.30.0.95', '172.30.0.96/32')"
reload
engine -X POST http://127.0.0.1:58090/v1/unban -d '{"v":1,"src_ip":"172.30.0.21"}'
uac unwalled uac.xml 905551112233 "" -d 500
check firewall "with the rule gone and the ban lifted the client calls again" $? 0
settle
check firewall "blocked calls leave no record" "$(sql -c "select count(*) from cdrs where call_id like '$RUN-walled%'")" 0
check firewall "the engine counts what it blocked" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_source_blocked_total" { print ($2 > 0) ? "yes" : "no" }')" yes

# 6b. A trunk that signs in. Calling from an address nobody knows, the right
#     password gets the call through and recorded for its client; a wrong
#     one is only asked again.
digest() { # digest <expected final answer> <password> [number]
  from_ip 172.30.0.97 "sed 's/__FINAL__/$1/' /work/test/sipp/uac-digest.xml > /tmp/s.xml && exec timeout 20 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.97 -s ${3:-905551112233} -cid_str '$RUN-digest$1-%u@%s' -au lab-digest -ap $2 -d 1500 -m 1 -nostdin"
}
digest 200 lab-only-Digest-2026
check digest "right password: call answered" $? 0
digest 401 not-the-password
check digest "wrong password: asked again" $? 0
#     Signed in, to a destination outside the tariff: refused by the engine
#     and recorded for the client (its trace: section 10).
digest 403 lab-only-Digest-2026 445551112233
check digest "signed in, destination outside the tariff: refused" $? 0
settle
check digest "recorded for the client that signed in" "$(sql -c "select string_agg(distinct cl.name, ',') from cdrs c join clients cl on cl.id = c.client_id where c.call_id like '$RUN-digest200-%' and c.sip_code = 200")" "Lab SIPp digest"
check digest "nothing recorded for the wrong password" "$(sql -c "select count(*) from cdrs where call_id like '$RUN-digest401-%'")" 0
check digest "the refusal after signing in is recorded for its client" "$(sql -c "select string_agg(cl.name || ':' || c.sip_code, ',') from cdrs c join clients cl on cl.id = c.client_id where c.call_id like '$RUN-digest403-%'")" "Lab SIPp digest:403"

# 6c. Several addresses and networks per trunk. A call from a trunk's second
#     address is its client's; so is one from inside a trunk's network; a
#     single address goes before the network it lies in; the address just
#     past the network's end is a stranger.
addr_call() { # addr_call <case> <source address>
  from_ip $2 "exec timeout 20 sipp -sf /work/test/sipp/uac.xml 172.30.0.10:5060 -i $2 -s 905551112233 -cid_str '$RUN-$1-%u@%s' -d 1000 -m 1 -nostdin"
}
client_of() { sql -c "select coalesce(string_agg(distinct cl.name || ':' || c.sip_code, ','), '') from cdrs c join clients cl on cl.id = c.client_id where c.call_id like '$RUN-$1-%'"; }
addr_call second 172.30.0.90
check addresses "second address of a trunk: call answered" $? 0
addr_call network 172.30.0.82
check addresses "address inside a trunk's network: call answered" $? 0
addr_call specific 172.30.0.83
check addresses "single address inside another trunk's network: call answered" $? 0
from_ip 172.30.0.84 "sed 's/__CODE__/401/' /work/test/sipp/uac-refused.xml > /tmp/s.xml && exec timeout 5 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.84 -s 905551112233 -cid_str '$RUN-outside-%u@%s' -m 1 -nostdin"
check addresses "address just outside the network: asked to sign in like any stranger" $? 0
from_ip 172.30.0.82 "sed 's/__EXPECT__/answer/' /work/test/sipp/uac-options.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.82 -m 1 -nostdin"
check addresses "probe from inside the network is answered" $? 0
from_ip 172.30.0.84 "sed 's/__EXPECT__/silence/' /work/test/sipp/uac-options.xml > /tmp/s.xml && exec sipp -sf /tmp/s.xml 172.30.0.10:5060 -i 172.30.0.84 -m 1 -nostdin"
check addresses "probe from just outside gets no answer" $? 0
settle
check addresses "second address: recorded for the trunk's client" "$(client_of second)" "Lab SIPp:200"
check addresses "network: recorded for the network's client" "$(client_of network)" "Lab SIPp network:200"
check addresses "single address before the network: recorded for its own client" "$(client_of specific)" "Lab SIPp:200"
check addresses "outside: nothing recorded" "$(client_of outside)" ""

# 6d. A call from a source no trunk matches, taken by an extension on behalf
#     of a client (engine.Config.Incoming, T-04). The lab's engine takes
#     calls from 172.30.0.91 for the client of "lab-nat" (LAB_INBOUND);
#     .91 and .92 are a carrier's connections, so the SIP core knows both
#     and asks the engine. The call from .91 goes through the client's plan
#     and is recorded for it, with no trunk; the one from .92 is refused as
#     from an unknown source and leaves no record; with .91 denied by the
#     firewall the hook is not asked.
inbound_call() { # inbound_call <case> <source address> <scenario> [sed expression] [sipp arguments]
  from_ip $2 "sed '${4:-}' /work/test/sipp/$3 > /tmp/s.xml && exec timeout 20 sipp -sf /tmp/s.xml 172.30.0.10:5060 -i $2 -s 905551112233 -cid_str '$RUN-$1-%u@%s' -m 1 -timeout 15 -nostdin ${5:-}"
}
inbound_call inbound 172.30.0.91 uac.xml "" "-d 1000"
check inbound "taken for a client: call answered" $? 0
inbound_call notinbound 172.30.0.92 uac-refused.xml "s/__CODE__/403/"
check inbound "not taken: refused as from an unknown source" $? 0
sql -c "insert into firewall_rules (reseller_id, cidr, action, note) values ($ROOT_ID, '172.30.0.91', 'deny', 'scenario')"
reload
inbound_call deniedinbound 172.30.0.91 uac-refused.xml "s/__CODE__/403/"
check inbound "denied by the firewall: refused before the hook" $? 0
sql -c "delete from firewall_rules where cidr = '172.30.0.91'"
reload
engine -X POST http://127.0.0.1:58090/v1/unban -d '{"v":1,"src_ip":"172.30.0.91"}'
settle
check inbound "recorded for the client, no trunk, from the carrier" \
  "$(sql -c "select string_agg(cl.name || ':' || coalesce(c.trunk_id::text, 'none') || ':' || c.src_ip || ':' || vc.host || ':' || c.sip_code, ',') from cdrs c join clients cl on cl.id = c.client_id join vendor_connections vc on vc.id = c.connection_id where c.call_id like '$RUN-inbound-%'")" \
  "Lab SIPp NAT:none:172.30.0.91:172.30.0.20:200"
check inbound "charged to the client" "$(sql -c "select sell_amount > 0 from cdrs where call_id like '$RUN-inbound-%'")" t
check inbound "refused calls: nothing recorded" "$(sql -c "select count(*) from cdrs where call_id like '$RUN-notinbound-%' or call_id like '$RUN-deniedinbound-%'")" 0

# 7. Encryption. Signalling: a caller connects over TLS. Media: each side
#    gets what it speaks, with rtpengine translating in between; the vendor
#    scenarios refuse an offer of the wrong kind, so a wrong offer fails the call.
TLS="-t l1 -tls_cert /work/certs/vendor.crt -tls_key /work/certs/vendor.key"
UAC_TARGET=172.30.0.10:5061 uac tls uac.xml 905551112233 "" -d 1000 $TLS
check tls "caller over TLS, sipp exit" $? 0
uac srtpin uac-srtp.xml 905551112233 "" -d 1000
check srtpin "SRTP caller, plain vendor, sipp exit" $? 0
uac srtpout uac.xml 975551112233 "" -d 1000
check srtpout "plain caller, TLS and SRTP vendor, sipp exit" $? 0
UAC_TARGET=172.30.0.10:5061 uac secure uac-srtp.xml 975551112233 "" -d 1000 $TLS
check secure "TLS and SRTP on both sides, sipp exit" $? 0
uac tlsdown uac.xml 965551112233 "" -d 1000
check tlsdown "unreachable TLS vendor, sipp exit" $? 0
settle
check tlsdown "legs" "$(legs tlsdown)" "1:172.30.0.24:503 2:172.30.0.24:200"
check tls "legs" "$(legs tls)" "1:172.30.0.20:200"
check srtpin "legs" "$(legs srtpin)" "1:172.30.0.20:200"
check srtpout "legs" "$(legs srtpout)" "1:172.30.0.24:200"
check secure "legs" "$(legs secure)" "1:172.30.0.24:200"

# 7b. Callers' side rules and options. A trunk may demand TLS, or TLS with a
#     certificate of a given name; a caller may ask for media keys agreed in
#     the media path (DTLS-SRTP) while the vendor gets plain audio.
uac needtls uac-refused.xml 881905551112233 "s/__CODE__/403/"
check needtls "TLS-only trunk refuses a call over UDP" $? 0
UAC_TARGET=172.30.0.10:5061 uac hastls uac.xml 881905551112233 "" -d 1000 $TLS
check hastls "TLS-only trunk takes a call over TLS" $? 0
# SIPp always presents a certificate over TLS, so the caller without one is
# a bare TLS client that sends the request by hand.
NOCERT=$("${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c "{ printf 'INVITE sip:882905551112233@172.30.0.10:5061;transport=tls SIP/2.0\r\nVia: SIP/2.0/TLS 172.30.0.21:5099;branch=z9hG4bK$RUN\r\nFrom: <sip:sipp@172.30.0.21>;tag=1\r\nTo: <sip:882905551112233@172.30.0.10>\r\nCall-ID: $RUN-nocert-1\r\nCSeq: 1 INVITE\r\nContact: <sip:sipp@172.30.0.21:5099;transport=tls>\r\nMax-Forwards: 70\r\nContent-Length: 0\r\n\r\n'; sleep 2; } | timeout 5 openssl s_client -quiet -connect 172.30.0.10:5061 -bind 172.30.0.21 2>/dev/null | grep -ac '^SIP/2.0 403'" 2>/dev/null | some)
check nocert "certificate trunk refuses a caller without one" "$NOCERT" yes
UAC_TARGET=172.30.0.10:5061 uac wrongcert uac-refused.xml 882905551112233 "s/__CODE__/403/" $TLS
check wrongcert "certificate trunk refuses another name" $? 0
UAC_TARGET=172.30.0.10:5061 uac cert uac.xml 882905551112233 "" -d 1000 -t l1 -tls_cert /work/certs/caller.crt -tls_key /work/certs/caller.key
check cert "certificate trunk takes the named certificate" $? 0
uac dtls uac-dtls.xml 905551112233 "" -d 1000
check dtls "DTLS-SRTP caller, plain vendor, sipp exit" $? 0
settle
check needtls "refusal recorded with its reason's code" "$(legs needtls)" "1:-:403"
check hastls "legs" "$(legs hastls)" "1:172.30.0.20:200"
check cert "legs" "$(legs cert)" "1:172.30.0.20:200"
check dtls "legs" "$(legs dtls)" "1:172.30.0.20:200"

# 7c. A replaced certificate is served to new connections within seconds,
#     without a restart: calls in progress are not touched. A certificate
#     that does not match its key is left aside.
served() { "${COMPOSE[@]}" exec -T sipp-uas-tls sh -c "timeout 5 openssl s_client -connect 172.30.0.10:5061 </dev/null 2>/dev/null | openssl x509 -noout -serial"; }
CERTS=$(cd "$(dirname "$0")/../../deploy/compose/certs" && pwd)
BOOT=$(docker inspect -f '{{.State.StartedAt}}' softswitch-lab-opensips-1)
OLD=$(served)
cp "$CERTS/opensips.crt" "$CERTS/opensips.crt.keep"
# A run that is cut short must not leave the lab with a certificate the SIP
# core cannot start with.
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"' EXIT
cp "$CERTS/vendor.crt" "$CERTS/opensips.crt" # a certificate, but not this key's
sleep 8
check renew "mismatched certificate is not loaded" "$(served)" "$OLD"
UAC_TARGET=172.30.0.10:5061 uac held uac.xml 975551112233 "" -d 14000 $TLS &
HELD=$!
sleep 2
openssl req -new -key "$CERTS/opensips.key" -subj "/CN=opensips.lab.test" -out "$CERTS/renew.csr" 2>/dev/null
openssl x509 -req -in "$CERTS/renew.csr" -CA "$CERTS/ca.crt" -CAkey "$CERTS/ca.key" -CAcreateserial -days 30 -out "$CERTS/opensips.crt" 2>/dev/null
rm -f "$CERTS/renew.csr"
sleep 8
NEW=$(served)
check renew "new certificate is served" "$([ -n "$NEW" ] && [ "$NEW" != "$OLD" ] && echo yes)" yes
check renew "SIP core was not restarted" "$(docker inspect -f '{{.State.StartedAt}}' softswitch-lab-opensips-1)" "$BOOT"
# The held call ends after the reload: its BYE crosses connections that
# were open before it. (No other caller may run meanwhile: they share an
# address.)
wait $HELD
check renew "a call in progress over TLS outlives the reload" $? 0
UAC_TARGET=172.30.0.10:5061 uac renewed uac.xml 975551112233 "" -d 1000 $TLS
check renew "calls go on with the new certificate, both sides" $? 0
mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"
sleep 8
check renew "original certificate is back" "$(served)" "$OLD"

# 7d. Weights. Three connections at one price with weights 3, 1 and 0 share
#     sixty calls: the heavier carries more, the lighter some, and the one
#     of weight zero none while the others answer.
uac weights uac.xml 955551112233 "" -m 60 -r 20 -d 200
check weights "sipp exit" $? 0
settle
carried() { sql -c "select count(*) from cdrs c join vendor_connections vc on vc.id = c.connection_id where c.call_id like '$RUN-weights-%' and vc.name = '$1'"; }
W3=$(carried w3); W1=$(carried w1)
check weights "every call answered at the first attempt" "$(sql -c "select count(*) filter (where sip_code = 200 and leg = 1), count(*) from cdrs where call_id like '$RUN-weights-%'" | tr '|' ' ')" "60 60"
check weights "the heavier connection carries more ($W3 to $W1)" "$([ "$W3" -gt "$W1" ] && echo yes || echo no)" yes
check weights "the lighter connection carries some" "$(echo "$W1" | some)" yes
check weights "weight zero carries none" "$(carried w0)" 0

# 7e. Rules about the caller's number. The lab's CLI client (prefix 883)
#     takes only callers on its list; the account refuses one range for
#     everybody, and the longer prefix wins; one range is shown to the vendor
#     in another form, in every header, while the record keeps what arrived.
#     The vendor scenario refuses a "rewritten" caller that arrives unchanged.
from() { echo "s/sipp <sip:sipp@\\([^>]*\\)>;tag/$2 <sip:$1@\\1>;tag/"; }
uac clilist uac.xml 883905551112233 "$(from 902125550000 sipp)" -d 500
check clilist "listed caller is taken" $? 0
uac cliother uac-refused.xml 883905551112233 "$(from 905325550000 sipp);s/__CODE__/403/"
check cliother "caller off the list is refused" $? 0
uac clianon uac-refused.xml 883905551112233 "s/__CODE__/403/"
check clianon "caller without a number is refused" $? 0
uac cliblack uac-refused.xml 883905551112233 "$(from 902126665000 sipp);s/__CODE__/403/"
check cliblack "the account's longer prefix refuses a listed range" $? 0
uac clirewrite uac.xml 883905551112233 "$(from 908505550000 rewritten);s/Max-Forwards: 70/Max-Forwards: 70\\n      P-Asserted-Identity: <sip:908505550000@lab.test>\\n      Remote-Party-ID: <sip:908505550000@lab.test>;party=calling/" -d 500
check clirewrite "vendor is shown the changed number, caller ends the call" $? 0
uac cliend uac-cutoff.xml 883905551112233 "$(from 908505550000 rewritten)" &
sleep 2
ID=$(engine "http://127.0.0.1:58090/v1/calls?limit=50" | grep -o "\"call_id\":\"$RUN-cliend-[^\"]*\"" | cut -d'"' -f4)
engine -X POST http://127.0.0.1:58090/v1/hangup -d "{\"v\":1,\"call_id\":\"$ID\"}"
wait $!
check cliend "a call with a changed number is ended by the switch" $? 0
settle
check clilist "legs" "$(legs clilist)" "1:172.30.0.20:200"
check cliother "refusal recorded" "$(legs cliother)" "1:-:403"
check cliblack "refusal recorded" "$(legs cliblack)" "1:-:403"
check clirewrite "legs" "$(legs clirewrite)" "1:172.30.0.20:200"
check clirewrite "the record keeps the number that arrived" "$(sql -c "select src from cdrs where call_id like '$RUN-clirewrite-%'")" 908505550000
#     Rules on the asserted number (T-16). The client of prefix 878 takes
#     only callers whose asserted number (P-Asserted-Identity, sip: or tel:)
#     is on its list and refuses callers that assert none (433); From plays
#     no part there. The client of prefix 876 refuses callers without a
#     number in From (433) and takes those with one.
asserts() { echo "s/Max-Forwards: 70/Max-Forwards: 70\\n      P-Asserted-Identity: <$1>/"; }
uac paion uac.xml 878905551112233 "$(from 905325550000 sipp);$(asserts tel:+902125550000)" -d 500 -timeout 20
check paion "asserted number on the list: taken though From is off it" $? 0
uac paioff uac-refused.xml 878905551112233 "$(from 902125550000 sipp);$(asserts sip:905325550000@lab.test);s/__CODE__/403/" -timeout 20
check paioff "asserted number off the list: refused though From is on it" $? 0
uac paianon uac-refused.xml 878905551112233 "$(from 902125550000 sipp);s/__CODE__/433/" -timeout 20
check paianon "no asserted number: refused with 433" $? 0
uac fromanon uac-refused.xml 876905551112233 "s/__CODE__/433/" -timeout 20
check fromanon "no number in From: refused with 433" $? 0
uac fromnum uac.xml 876905551112233 "$(from 902125550000 sipp)" -d 500 -timeout 20
check fromanon "a number in From: taken" $? 0
settle
check paion "legs" "$(legs paion)" "1:172.30.0.20:200"
check paioff "refusal recorded" "$(legs paioff)" "1:-:403"
check paianon "refusal recorded" "$(legs paianon) $(legs fromanon)" "1:-:433 1:-:433"
check fromanon "legs" "$(legs fromnum)" "1:172.30.0.20:200"

# 7f. The name in a vendor's certificate. Vendor G is vendor D's address
#     under a name its certificate does not carry, and the cheaper of the
#     two for 94…: the engine has looked at the certificate and gives it no
#     calls, so the call goes straight to vendor D and G is never tried.
#     Where G is the only route (93…) the call has none.
PEERS=$(engine http://127.0.0.1:58090/v1/stats)
G=$(sql -c "select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id where v.name = 'Lab vendor G (wrong name)'")
check peername "the engine refuses the misnamed certificate only" "$(echo "$PEERS" | grep -o '"refused":{[^}]*}' | grep -o '"[0-9]*":' | tr -d '":\n')" "$G"
uac peername uac.xml 945551112233 "" -d 500
check peername "sipp exit" $? 0
uac peeronly uac-refused.xml 935551112233 "s/__CODE__/503/"
check peeronly "a call with no other route is refused" $? 0
settle
check peername "legs: one attempt, on the vendor with the right name" "$(sql -c "select string_agg(c.leg || ':' || v.name || ':' || c.sip_code, ' ' order by c.leg) from cdrs c join vendor_connections vc on vc.id = c.connection_id join vendors v on v.id = vc.vendor_id where c.call_id like '$RUN-peername-%'")" "1:Lab vendor D (TLS):200"
check peeronly "legs" "$(legs peeronly)" "1:-:503"
check peername "the SIP core's own certificate has an expiry on record" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_sip_cert_expires_seconds" { print ($2 > 86400) ? "yes" : "no" }')" yes

# 7g. Rates and routes by the hour (92…). The dearer selling rate's window
#     is put around this moment, as is the answering vendor's route; the
#     cheaper, refusing vendor's route is put two hours away. The call must
#     go straight to the answering vendor and be charged the windowed rate
#     (60 seconds at 0.06 a minute), by the engine's hold and by the rater.
sql -c "
  with n as (select (extract(hour from now() at time zone 'UTC') * 60 + extract(minute from now() at time zone 'UTC'))::int as m)
  update rates r set time_from = (n.m + 1380) % 1440, time_to = (n.m + 60) % 1440 from n
    where r.prefix = '92' and r.time_from <> r.time_to and r.tariff_id = (select id from tariffs where name = 'Lab sell');
  with n as (select (extract(hour from now() at time zone 'UTC') * 60 + extract(minute from now() at time zone 'UTC'))::int as m)
  update routing_entries e set time_from = (n.m + case when vc.host = '172.30.0.20' then 1380 else 120 end) % 1440,
                               time_to = (n.m + case when vc.host = '172.30.0.20' then 60 else 180 end) % 1440
    from n, vendor_connections vc where vc.id = e.connection_id and e.prefix = '92'"
reload
uac bytime uac.xml 925551112233 "" -d 1000
check bytime "sipp exit" $? 0
settle
check bytime "legs: the route outside its hours is not tried" "$(legs bytime)" "1:172.30.0.20:200"
check bytime "charged the rate of this hour" "$(sql -c "select sell_billed_sec || ' ' || sell_amount from cdrs where call_id like '$RUN-bytime-%'")" "60 60000"
# 7g'. Public holidays (T-19, ADR-0044). The same windows limited to one
#     day of the week that is neither today nor yesterday (UTC, the zone of
#     the lab tariff and plan) leave no route; a holiday on both dates,
#     counted as that day, brings the route and the rate of this hour back.
#     Yesterday too, because a window across midnight counts to the day it
#     began on. No other lab window is limited to days, so nothing else
#     notices the holidays, which are removed straight after.
HDAY=$(sql -c "select (extract(isodow from now() at time zone 'UTC')::int + 2) % 7")   # Monday = 0
sql -c "update rates set days = 1 << $HDAY where prefix = '92' and time_from <> time_to;
        update routing_entries set days = 1 << $HDAY where prefix = '92'"
reload
uac noholiday uac-refused.xml 925551112233 "s/__CODE__/503/"
check noholiday "on another day of the week the windows are closed" $? 0
sql -c "insert into holidays (reseller_id, day, counts_as)
          select (select id from resellers order by id limit 1), (now() at time zone 'UTC')::date - d, $HDAY + 1 from generate_series(0, 1) d"
reload
uac holiday uac.xml 925551112233 "" -d 1000
check holiday "sipp exit" $? 0
settle
check holiday "legs: the holiday opens the route of that day" "$(legs holiday)" "1:172.30.0.20:200"
check holiday "charged the rate of that day" "$(sql -c "select sell_billed_sec || ' ' || sell_amount from cdrs where call_id like '$RUN-holiday-%'")" "60 60000"
sql -c "delete from holidays where reseller_id = (select id from resellers order by id limit 1)
          and day >= (now() at time zone 'UTC')::date - 1 and day <= (now() at time zone 'UTC')::date;
        update rates set days = 127 where prefix = '92' and time_from <> time_to;
        update routing_entries set days = 127 where prefix = '92'"
#     With every window moved half a day away no route is left.
sql -c "update rates set time_from = (time_from + 720) % 1440, time_to = (time_to + 720) % 1440 where prefix = '92' and time_from <> time_to;
        update routing_entries set time_from = (time_from + 720) % 1440, time_to = (time_to + 720) % 1440 where prefix = '92'"
reload
uac offhours uac-refused.xml 925551112233 "s/__CODE__/503/"
check offhours "outside every route's hours the call has no route" $? 0

# 7h. Vendors in two currencies (91…). The vendor paid in EUR asks the
#     smaller figure but the larger sum, so the other is tried first and
#     answers: one leg. Its cost is recorded in the account's currency too.
uac currency uac.xml 915551112233 "" -d 1000
check currency "sipp exit" $? 0
settle
check currency "legs: the vendor that is cheaper by worth, not by figure" "$(legs currency)" "1:172.30.0.20:200"
check currency "amounts in the account's currency" "$(sql -c "select sell_base = sell_amount and buy_base = buy_amount and buy_base > 0 from cdrs where call_id like '$RUN-currency-%'")" t
#     With the EUR worth half as much the EUR vendor is the cheaper: it is
#     tried first, refuses, and what it would have cost is not charged.
sql -c "update exchange_rates set rate = 600000 where currency = 'EUR'"
reload
uac currency2 uac.xml 915551112233 "" -d 1000
check currency2 "sipp exit" $? 0
sql -c "update exchange_rates set rate = 1200000 where currency = 'EUR'"
reload
settle
check currency2 "legs after the rate of exchange changed" "$(legs currency2)" "1:172.30.0.23:503 2:172.30.0.20:200"

# 7i. Bundles of minutes (client behind prefix 884: no balance, a bundle
#     towards 90…). A call inside the bundle costs nothing and its seconds
#     are counted; once counted and loaded, the engine lets go of them.
bundle() { sql -c "select $1 from bundles b join clients c on c.id = b.client_id where c.name = 'Lab SIPp bundle'"; }
bundle_set() { # bundle_set <seconds per month> <seconds used> <balance>
  sql -c "update bundles set seconds = $1 where id = $(bundle b.id);
          delete from bundle_usage where bundle_id = $(bundle b.id);
          insert into bundle_usage (bundle_id, period, used_sec) values ($(bundle b.id), to_char(now() at time zone 'UTC', 'YYYY-MM'), $2);
          update clients set balance = $3 where name = 'Lab SIPp bundle'"
  reload
}
bundle_row() { sql -F ' ' -c "select $2 from cdrs where call_id like '$RUN-$1-%'"; }
BUNDLE_HELD="heldsum:b$(bundle b.id):$(date -u +%Y-%m)"
bundle_set 600 0 0
uac bundle uac.xml 884905551112233 "" -d 2000
check bundle "call without a balance is carried by the bundle" $? 0
settle
check bundle "legs" "$(legs bundle)" "1:172.30.0.20:200"
check bundle "billed to the bundle, nothing charged" "$(bundle_row bundle "bundle_sec = sell_billed_sec and bundle_sec between 2 and 3, sell_amount, bundle_id = $(bundle b.id)")" "t 0 t"
check bundle "seconds counted for the month" "$(sql -c "select u.used_sec = c.bundle_sec from bundle_usage u, cdrs c where u.bundle_id = $(bundle b.id) and c.call_id like '$RUN-bundle-%'")" t
check bundle "balance untouched" "$(sql -c "select balance from clients where name = 'Lab SIPp bundle'")" 0
reload
check bundle "seconds let go once counted and loaded" "$("${COMPOSE[@]}" exec -T redis redis-cli exists "$BUNDLE_HELD")" 0
#     The bundle used up and no balance: the call is refused.
bundle_set 600 600 0
uac bundleout uac-refused.xml 884905551112233 "s/__CODE__/402/"
check bundleout "used-up bundle without a balance is refused" $? 0
#     Used up, with a balance: the call is charged what the tariff says.
bundle_set 600 600 1000000
uac bundlepaid uac.xml 884905551112233 "" -d 2000
check bundlepaid "sipp exit" $? 0
settle
check bundlepaid "charged, nothing from the bundle" "$(bundle_row bundlepaid "bundle_sec, sell_amount > 0, bundle_id is null")" "0 t t"
check bundlepaid "balance paid for it" "$(sql -c "select 1000000 - c.balance = r.sell_amount from clients c, cdrs r where c.name = 'Lab SIPp bundle' and r.call_id like '$RUN-bundlepaid-%'")" t
check bundlepaid "the month's count stays at the bundle's size" "$(sql -c "select used_sec from bundle_usage where bundle_id = $(bundle b.id)")" 600
#     Three seconds left and no balance: the engine ends the call as the
#     bundle runs out, and nothing is owed.
bundle_set 600 597 0
mark_ends
uac bundlecut uac-cutoff.xml 884905551112233 ""
check bundlecut "switch hung up" $? 0
settle
DUR=$(bundle_row bundlecut duration_sec)
if [ "${DUR:-0}" -ge 2 ] && [ "${DUR:-0}" -le 3 ]; then echo "PASS  bundlecut: duration ${DUR}s"; else echo "FAIL  bundlecut: duration '${DUR}', want 2-3"; FAILED=1; fi
ended_once bundlecut
check bundlecut "paid by the bundle alone" "$(bundle_row bundlecut "bundle_sec = sell_billed_sec, sell_amount")" "t 0"
check bundlecut "bundle not overdrawn, nothing owed" "$(sql -c "select u.used_sec <= b.seconds and c.balance = 0 from bundle_usage u join bundles b on b.id = u.bundle_id join clients c on c.id = b.client_id where c.name = 'Lab SIPp bundle'")" t
bundle_set 600 0 0

# The management API, for the scenarios that go through it (registrations
# here, call traces further down). It runs for the occasion, from this
# checkout, against the lab's stores and engine, with two keys of its own:
# one of the account that runs the system and one of an account with no
# clients.
TRACE_API=http://127.0.0.1:${TRACE_API_PORT:-58097}
TMP=$(mktemp -d)
ROOT_ID=$(sql -c "select id from resellers order by id limit 1")
trace_cleanup() {
  [ -n "${TRACE_PID:-}" ] && kill "$TRACE_PID" 2>/dev/null
  sql -c "delete from api_keys where name like 'trace scenario%'; delete from resellers where name = 'trace scenario'" >/dev/null 2>&1
  rm -rf "$TMP"
}
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup' EXIT
sql -c "insert into resellers (name) values ('trace scenario');
        insert into api_keys (reseller_id, name, key_hash) values
          ($ROOT_ID, 'trace scenario root', sha256('$RUN-root'::bytea)),
          ((select id from resellers where name = 'trace scenario'), 'trace scenario other', sha256('$RUN-other'::bytea))"
if (cd "$ROOT" && go build -o "$TMP/api" ./cmd/api); then
  LISTEN=${TRACE_API#http://} DATABASE_URL=postgres://softswitch:lab-only@127.0.0.1:56432/softswitch \
    CLICKHOUSE_URL=http://softswitch:lab-only@127.0.0.1:58123/softswitch \
    ENGINE_URL=http://127.0.0.1:58090 ENGINE_TOKEN="$ENGINE_TOKEN" SECRET_KEY="$SECRET_KEY" "$TMP/api" >"$TMP/api.log" 2>&1 &
  TRACE_PID=$!
  for _ in $(seq 1 30); do curl -fsS "$TRACE_API/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
fi
api() { # api <key> <method> <path> [curl arguments]: the API's answer
  local key=$1 method=$2 path=$3; shift 3
  curl -sS -X "$method" -H "Authorization: Bearer $RUN-$key" "$@" "$TRACE_API$path"
}

# 7j. Registration and address translation (NAT). A trunk that signs in
#     registers from behind NAT: it names a private address for itself, and
#     everything meant for it must go to where its packets really come from
#     (test/sipp/natlab.sh plays it). The lab's times are short: a
#     registration through NAT lasts 30 seconds, and 2 is the least a trunk
#     may ask for.
NATPASS=lab-only-Nat-2026
nat() { "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac /work/test/sipp/natlab.sh "$@" 2>/dev/null; }
trunk_id() { sql -c "select id from client_trunks where username = '$1'"; }
bound() { engine "http://127.0.0.1:58090/v1/registrations?trunk_id=$(trunk_id "$1")"; } # bound <user name>: its registrations
count() { grep -o '"contact"' | wc -l | tr -d ' '; }
hangup_when_up() { # hangup_when_up <case>: has the engine end the case's call once it is answered
  local id
  for _ in $(seq 1 40); do
    id=$(engine "http://127.0.0.1:58090/v1/calls?limit=50" | tr '{' '\n' | grep "\"answer_at\":\"" | grep -o "\"call_id\":\"$RUN-$1-[^\"]*\"" | cut -d'"' -f4)
    [ -n "$id" ] && break
    sleep 0.5
  done
  sleep 1
  engine -X POST http://127.0.0.1:58090/v1/hangup -d "{\"v\":1,\"call_id\":\"$id\"}"
}
for user in lab-nat lab-nat-2 lab-nat-tls lab-nat-mix; do
  engine -X POST http://127.0.0.1:58090/v1/unregister -d "{\"v\":1,\"trunk_id\":$(trunk_id $user)}" >/dev/null
done
REGISTERED_BEFORE=$(curl -fsS http://127.0.0.1:58090/metrics | awk 'index($0, "softswitch_engine_register_total{result=\"stored\"}") == 1 { print $NF }')

#     The right password: accepted, though the answer had to find its way
#     back to an address the request did not name, and told to come back
#     within the short interval. The engine and the API show it.
nat register lab-nat $NATPASS 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 'expires=30'
check register "behind NAT, right password: accepted for the short interval" $? 0
check register "the engine has it, with the address it really came from" "$(bound lab-nat | grep -c '"contact":"sip:lab-nat@10.0.0.5:5060","src_ip":"172.30.0.70","src_port":5077,"transport":"udp","nat":true')" 1
check register "the API shows from where, since when and until when" "$(api root GET "/v1/client-trunks/$(trunk_id lab-nat)/registrations" | python3 -c "
import json, sys, datetime
r = json.load(sys.stdin)['registrations']
at = lambda s: datetime.datetime.fromisoformat(s[:19])
now = datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None)
print(len(r), r[0]['src_ip'], r[0]['src_port'], r[0]['nat'], r[0]['user_agent'], at(r[0]['since']) <= now < at(r[0]['until']), (at(r[0]['until']) - at(r[0]['since'])).seconds)" 2>&1)" "1 172.30.0.70 5077 True sipp-lab-pbx True 30"
check register "the API lists it among the client's" "$(api root GET "/v1/registrations?client_id=$(sql -c "select client_id from client_trunks where username = 'lab-nat'")" | count)" 1
check register "another account's key is refused it" "$(api other GET "/v1/client-trunks/$(trunk_id lab-nat)/registrations" -o /dev/null -w '%{http_code}')" 404
#     Its address is no stranger's while it is registered: its device's
#     question whether the switch is there is answered.
nat probe answer
check register "a registered trunk's probe is answered" $? 0
#     A wrong password is asked again; a trunk that may only be used over
#     TLS is refused over UDP; too short a time is refused with the least
#     that may be asked. Nothing of these is stored.
nat register lab-nat-2 not-the-password real real 600 401 'WWW-Authenticate'
check register "wrong password: asked again" $? 0
nat register lab-nat-tls $NATPASS real real 600 403 'tls required'
check register "TLS-only trunk: refused over UDP" $? 0
nat register lab-nat-2 $NATPASS real real 1 423 'Min-Expires: 2'
check register "too short a time: refused with the least allowed" $? 0
check register "none of these is stored" "$(bound lab-nat-2 | count) $(bound lab-nat-tls | count)" "0 0"
#     Over TLS the same trunk is taken. A trunk on a connection is reached
#     through that connection, so it too is told the short interval.
nat register_tls lab-nat-tls $NATPASS real real 600 200 'expires=30'
check register "TLS-only trunk: accepted over TLS" $? 0
check register "kept as reachable through its connection" "$(bound lab-nat-tls | grep -c '"src_ip":"172.30.0.70","src_port":[0-9]*,"transport":"tls","nat":true')" 1
#     A trunk with an address of its own (it names where it really is)
#     keeps the time it asked for. The trunk takes its registration back
#     (Expires: 0); one left alone runs out.
nat register lab-nat-2 $NATPASS real real 600 200 'expires=600'
check register "no NAT: accepted for the time asked" $? 0
check register "kept as reachable where it says it is" "$(bound lab-nat-2 | grep -c '"src_ip":"172.30.0.70","src_port":5077,"transport":"udp","nat":false')" 1
#     One contact per trunk unless more are allowed: a new one replaces the old.
nat register lab-nat-2 $NATPASS real '<sip:moved@172.30.0.70:5099>' 600 200 'expires=600'
check register "a second contact replaces the first" "$(bound lab-nat-2 | grep -o '"contact":"[^"]*"' | tr '\n' ' ')" '"contact":"sip:moved@172.30.0.70:5099" '
nat register lab-nat-2 $NATPASS real '<sip:moved@172.30.0.70:5099>' 0 200 'SIP.2.0 200'
check register "taken back by the trunk: accepted" $? 0
check register "and gone" "$(bound lab-nat-2 | count)" 0
nat register lab-nat-2 $NATPASS real real 3 200 'expires=3[^0-9]'
check register "three seconds: accepted" "$? $(bound lab-nat-2 | count)" "0 1"
sleep 4
check register "run out: gone" "$(bound lab-nat-2 | count)" 0

#     A call from behind NAT (the caller signs in; it is not registered).
#     Its answers reach it, and so does the BYE when the engine ends the call.
nat nat_caller $RUN-natcall 905551112233 hangup quiet
check nat "caller behind NAT: the call completes" $? 0
nat nat_caller $RUN-natend 905551112233 wait quiet &
hangup_when_up natend
wait $!
check nat "caller behind NAT: the switch's BYE reaches it" $? 0
#     Audio. The caller sends 100 packets from a port its SDP did not name,
#     the vendor sends each back. The relay must have taken the place of the
#     private address, found out where the packets really come from, and
#     sent the vendor's audio there.
python3 - "$ROOT/test/load/out/nat-rtp.pcap" <<'PCAP'
import struct, sys
# 100 packets of G.711 silence, 20 ms apart, as a capture file SIPp can play.
out = open(sys.argv[1], 'wb')
out.write(struct.pack('<IHHiIII', 0xa1b2c3d4, 2, 4, 0, 0, 65535, 1))
for i in range(100):
    rtp = struct.pack('!BBHII', 0x80, 8, i + 1, 160 * (i + 1), 0x1234abcd) + b'\xd5' * 160
    udp = struct.pack('!HHHH', 6000, 6000, 8 + len(rtp), 0) + rtp
    ip = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), i, 0, 64, 17, 0, bytes([10, 0, 0, 6]), bytes([10, 0, 0, 1])) + udp
    frame = b'\x02\x00\x00\x00\x00\x02' + b'\x02\x00\x00\x00\x00\x01' + b'\x08\x00' + ip
    out.write(struct.pack('<IIII', i // 50, (i % 50) * 20000, len(frame), len(frame)) + frame)
PCAP
MEDIA=$(nat media $RUN-natmedia 905551112233)
check media "caller behind NAT with audio: the call completes" "$(echo "$MEDIA" | grep -c 'caller=0$')" 1
check media "the relay: told the signalling's address, learned the real port, counted the audio both ways" "$(echo "$MEDIA" | python3 -c "
import re, sys
t = sys.stdin.read()
s = re.findall(r'8:endpointd6:family4:IPv47:address\d+:([\d.]+)4:porti(\d+)ee19:advertised endpointd6:family4:IPv47:address\d+:([\d.]+)4:porti(\d+)ee.*?5:statsd7:packetsi(\d+)e', t)
caller = [x for x in s if x[0] == '172.30.0.71']
vendor = [x for x in s if x[0] == '172.30.0.20']
print(caller[0][:4] if caller else caller, int(caller[0][4]) >= 90 if caller else '', int(vendor[0][4]) >= 90 if vendor else '')" 2>&1)" "('172.30.0.71', '6000', '172.30.0.71', '4000') True True"
BACK=$(echo "$MEDIA" | sed -n 's/^datagrams=\([0-9]*\) .*/\1/p')
if [ "${BACK:-0}" -ge 90 ]; then echo "PASS  media: the vendor's audio reached the caller behind NAT ($BACK datagrams)"; else echo "FAIL  media: datagrams that reached the caller: '${BACK}', want 90 or more"; FAILED=1; fi
settle
check nat "legs" "$(legs natcall) / $(legs natend) / $(legs natmedia)" "1:172.30.0.20:200 / 1:172.30.0.20:200 / 1:172.30.0.20:200"
check nat "recorded for the client that signed in" "$(client_of natcall)" "Lab SIPp NAT:200"

#     Calls to a registered trunk. The lab's engine delivers 8988… to
#     wherever "lab-nat" is registered (a lab hook; an extension does the
#     same for a client's numbers). The PBX registers from behind NAT and
#     gets the call where its REGISTER came from, addressed to the contact
#     it named, with the media relay in the path; the call is ended by the
#     caller, by the PBX, and by the switch. Then with the caller behind
#     NAT as well.
check trunk "caller ends it" "$(nat to_trunk $RUN-totrunk1 89885551112233 plain caller)" "caller=0 pbx=0"
check trunk "the PBX ends it" "$(nat to_trunk $RUN-totrunk2 89885551112233 plain trunk)" "caller=0 pbx=0"
nat to_trunk $RUN-totrunk3 89885551112233 plain switch > "$TMP/totrunk3" &
hangup_when_up totrunk3
wait $!
check trunk "the switch ends it: its BYE reaches both" "$(cat "$TMP/totrunk3")" "caller=0 pbx=0"
check trunk "both sides behind NAT, the PBX ends it" "$(nat to_trunk $RUN-totrunk4 89885551112233 nat trunk)" "caller=0 pbx=0"
check trunk "both sides behind NAT, the caller ends it" "$(nat to_trunk $RUN-totrunk5 89885551112233 nat caller)" "caller=0 pbx=0"
settle
check trunk "recorded as answered legs without a vendor" "$(legs totrunk1) $(legs totrunk2) $(legs totrunk3) $(legs totrunk4) $(legs totrunk5)" "1:-:200 1:-:200 1:-:200 1:-:200 1:-:200"
check trunk "charged to the caller's client at its tariff" "$(sql -c "select bool_and(sell_amount > 0 and buy_amount = 0 and duration_sec >= 1) from cdrs where call_id like '$RUN-totrunk%'")" t
#     The records name the trunk the calls went to (T-05); the calls of
#     the same PBX to a vendor above name none.
check trunk "the records name the trunk they went to" "$(sql -c "select string_agg(distinct coalesce(dst_trunk_id::text, '-'), ',') from cdrs where call_id like '$RUN-totrunk%'")" "$(trunk_id lab-nat)"
check trunk "legs to a vendor name no trunk" "$(sql -c "select count(*), count(dst_trunk_id) from cdrs where call_id like '$RUN-natcall-%' or call_id like '$RUN-natend-%' or call_id like '$RUN-natmedia-%'" | tr '|' ' ')" "3 0"

#     The trunk's own limit and codecs hold for calls delivered to it
#     (T-06). For this part "lab-nat" may take one call at a time and only
#     PCMA (put back afterwards, also if the run is cut short). A first
#     call holds it; a second call meanwhile is refused with 503 and never
#     reaches the PBX; the PBX is offered PCMA alone (and key presses)
#     though the caller offered PCMU first. Then nothing of either call is
#     left in the state store, and the trunk takes a call again.
NAT_TRUNK=$(trunk_id lab-nat)
NAT_SETTINGS=$(sql -c "select max_calls || ' ' || codecs::text from client_trunks where id = $NAT_TRUNK")
nat_back() { sql -c "update client_trunks set max_calls = ${NAT_SETTINGS%% *}, codecs = '${NAT_SETTINGS#* }' where id = $NAT_TRUNK" >/dev/null; reload; }
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup; nat_back' EXIT
sql -c "update client_trunks set max_calls = 1, codecs = '{PCMA}' where id = $NAT_TRUNK" >/dev/null
reload
check trklimit "a second call while the trunk has its one: refused, the PBX offered PCMA alone" \
  "$(nat limit_trunk $RUN-trklimit 89885551112233)" "first=0 second=0 pbx=0 offer=RTP/AVP 8 101"
settle
check trklimit "legs" "$(legs trklimit-a) / $(legs trklimit-b)" "1:-:200 / 1:-:503"
check trklimit "the refused call names no trunk (it went nowhere)" "$(sql -c "select count(dst_trunk_id) from cdrs where call_id like '$RUN-trklimit-b-%'")" 0
trunk_left() { # trunk_left <case> [trunk id...]: what the state store still holds of the case's calls, the trunks' counts among them ("lab-nat" when none is named)
  local n key client keys
  client=$(sql -c "select id from clients where name = 'Lab SIPp'")
  n=$("${COMPOSE[@]}" exec -T redis redis-cli --scan --pattern "call:$RUN-$1-*" | wc -l)
  keys="cc:t:$NAT_TRUNK"
  [ $# -gt 1 ] && keys=$(printf 'cc:t:%s ' "${@:2}")
  for key in cc:all "cc:c:$client" $keys; do
    n=$(( n + $("${COMPOSE[@]}" exec -T redis redis-cli zrange "$key" 0 -1 | grep -c -- "$RUN-$1-") ))
  done
  n=$(( n + $("${COMPOSE[@]}" exec -T redis redis-cli hkeys "held:$client" | grep -c -- "$RUN-$1-") ))
  echo "$n"
}
for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(trunk_left trklimit)" = 0 ] && break; sleep 1; done
check trklimit "nothing left in the state store, the trunk's count included" "$(trunk_left trklimit) $("${COMPOSE[@]}" exec -T redis redis-cli zcard "cc:t:$NAT_TRUNK" | tr -d '\r')" "0 0"
check trklimit "the trunk takes a call again" "$(nat to_trunk $RUN-trkfree 89885551112233 plain caller)" "caller=0 pbx=0"
nat_back
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup' EXIT
check trklimit "the trunk's settings are back" "$(sql -c "select max_calls || ' ' || codecs::text from client_trunks where id = $NAT_TRUNK")" "$NAT_SETTINGS"

#     The limits of a trunk and of a client count the calls they place and
#     the calls delivered to them together (T-45, ADR-0042). "lab-nat" may
#     have one call at a time: while it places a call of its own, a call to
#     it is refused with 503, and while a call delivered to it is up, its
#     own call is refused with 503. Then the trunk has no limit of its own
#     and its client ("Lab SIPp NAT") may have one call at a time: a call
#     placed by the client's other trunk, "lab-nat-2", keeps a call to
#     "lab-nat" out with 503. Both are put back afterwards, also if the run
#     is cut short; nothing of the calls is left in the state store, the
#     counts of the trunk and of the client included.
NAT_CLIENT=$(sql -c "select client_id from client_trunks where id = $NAT_TRUNK")
NAT_LIMITS=$(sql -c "select t.max_calls || ' ' || c.max_calls from client_trunks t join clients c on c.id = t.client_id where t.id = $NAT_TRUNK")
both_back() { sql -c "update client_trunks set max_calls = ${NAT_LIMITS%% *} where id = $NAT_TRUNK; update clients set max_calls = ${NAT_LIMITS#* } where id = $NAT_CLIENT" >/dev/null; reload; }
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup; both_back' EXIT
codes() { sql -c "select coalesce(string_agg(sip_code::text, ',' order by leg), '-') from cdrs where call_id like '$RUN-$1-%'"; } # codes <case>: the legs' answers
sql -c "update client_trunks set max_calls = 1 where id = $NAT_TRUNK" >/dev/null
reload
check bothways "the trunk's own call keeps a call to it out (b), a call to it keeps its own out (d)" \
  "$(nat limit_both $RUN-bothways 89885551112233 8905551112233)" "a=0 b=0 c=0 d=0 pbx=0"
sql -c "update client_trunks set max_calls = 0 where id = $NAT_TRUNK; update clients set max_calls = 1 where id = $NAT_CLIENT" >/dev/null
reload
check bothways "the client's call from its other trunk keeps a call to lab-nat out" \
  "$(nat limit_client $RUN-bothcl 89885551112233 8905551112233)" "a=0 b=0"
settle
check bothways "legs" "$(codes bothways-a) $(codes bothways-b) $(codes bothways-c) $(codes bothways-d) $(codes bothcl-a) $(codes bothcl-b)" "200 503 200 503 200 503"
for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(trunk_left bothways)$(trunk_left bothcl)" = 00 ] && break; sleep 1; done
nat_client_left() { "${COMPOSE[@]}" exec -T redis redis-cli zrange "cc:c:$NAT_CLIENT" 0 -1 | grep -c -- "$RUN-both"; } # the case's calls the client of lab-nat still counts
check bothways "nothing left in the state store, the counts of the trunk and of its client included" \
  "$(trunk_left bothways) $(trunk_left bothcl) $(nat_client_left)" "0 0 0"
both_back
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup' EXIT
check bothways "the limits are back" "$(sql -c "select t.max_calls || ' ' || c.max_calls from client_trunks t join clients c on c.id = t.client_id where t.id = $NAT_TRUNK")" "$NAT_LIMITS"

#     A trunk registered over TLS (T-07). It is reached through the
#     connection it registered over, never through a new one, and offered
#     encrypted audio (SRTP with a key), since the key travels in signalling
#     that is encrypted too; the caller, who offered plain audio, gets plain
#     audio. One SIPp process registers and takes the call (natlab.sh,
#     tls_trunk), and it listens on its own port as well: its connections
#     to the switch during the call must be the one it opened, alone.
TLS_TRUNK=$(trunk_id lab-nat-tls)
MIX_TRUNK=$(trunk_id lab-nat-mix)
check tlstrunk "registered over TLS: the call comes on that connection, SRTP offered, the caller gets plain audio" \
  "$(nat tls_trunk $RUN-tlstrunk 89875551112233)" "caller=0 pbx=0 offer=RTP/SAVP 8 keyed=yes conns=172.30.0.10:5061"
check tlstrunk "the binding is the connection's, from the PBX's port" "$(bound lab-nat-tls | grep -c '"src_ip":"172.30.0.70","src_port":5077,"transport":"tls","nat":true')" 1
#     The connection is gone (the registering process ended). With a second
#     registration over UDP the attempt over TLS fails at once and the call
#     goes there, with plain audio (the PBX there refuses anything else).
#     With none, the caller is refused with 503, and the switch does not
#     open a connection of its own to where the old one came from, though
#     something listens there now.
check tlstrunk "connection gone: the next registration (UDP, plain audio) takes the call" "$(nat gone_tls $RUN-tlsgone 89865551112233 udp)" "caller=0 pbx=0"
check tlstrunk "connection gone and nothing else: 503, no new connection tried" "$(nat gone_tls $RUN-tlsnone 89875551112233 none)" "caller=0 new_connections=0"
settle
check tlstrunk "legs" "$(legs tlstrunk) / $(legs tlsgone) / $(legs tlsnone)" "1:-:200 / 1:-:503 2:-:200 / 1:-:503"
check tlstrunk "every leg names the trunk it was for" "$(sql -c "select string_agg(coalesce(dst_trunk_id::text, '-'), ',' order by call_id, leg) from cdrs where call_id like '$RUN-tlstrunk-%' or call_id like '$RUN-tlsgone-%' or call_id like '$RUN-tlsnone-%'")" "$MIX_TRUNK,$MIX_TRUNK,$TLS_TRUNK,$TLS_TRUNK"
for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(( $(trunk_left tlstrunk "$TLS_TRUNK" "$MIX_TRUNK") + $(trunk_left tlsgone "$TLS_TRUNK" "$MIX_TRUNK") + $(trunk_left tlsnone "$TLS_TRUNK" "$MIX_TRUNK") ))" = 0 ] && break; sleep 1; done
check tlstrunk "nothing left in the state store, the trunks' counts included" \
  "$(( $(trunk_left tlstrunk "$TLS_TRUNK" "$MIX_TRUNK") + $(trunk_left tlsgone "$TLS_TRUNK" "$MIX_TRUNK") + $(trunk_left tlsnone "$TLS_TRUNK" "$MIX_TRUNK") ))" 0
for user in lab-nat-tls lab-nat-mix; do
  engine -X POST http://127.0.0.1:58090/v1/unregister -d "{\"v\":1,\"trunk_id\":$(trunk_id $user)}" >/dev/null
done

#     An operator removes the registration through the API: it is gone at
#     once, the removal is in the audit log, and a call to the trunk has
#     nowhere to go.
nat register lab-nat $NATPASS 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 'expires=30'
check operator "registered again" "$? $(bound lab-nat | count)" "0 1"
check operator "another account cannot remove it" "$(api other DELETE "/v1/client-trunks/$(trunk_id lab-nat)/registrations" -o /dev/null -w '%{http_code}') $(bound lab-nat | count)" "404 1"
check operator "removed through the API" "$(api root DELETE "/v1/client-trunks/$(trunk_id lab-nat)/registrations" -o /dev/null -w '%{http_code}') $(bound lab-nat | count)" "200 0"
check operator "the removal is in the audit log, with where the trunk was" "$(sql -c "select count(*) from audit_log where resource = 'client-trunks' and action = 'unregister' and row_id = $(trunk_id lab-nat) and at >= '$STARTED' and detail::text like '%172.30.0.70:5077%'")" 1
uac unbound uac-refused.xml 89885551112233 "s/__CODE__/480/"
check operator "a call to the trunk is refused: not registered" $? 0
reload
nat probe silence
check operator "its address is a stranger's again: a probe gets no answer" $? 0
settle
check operator "refusal recorded" "$(legs unbound)" "1:-:480"

#     A flood of registrations from an address nobody knows is cut off like
#     a flood of calls: afterwards even a right password gets no answer.
nat flood
check register "after a flood of registrations the address gets no answer" $? 124
check register "the engine counted what it stored" "$(( $(curl -fsS http://127.0.0.1:58090/metrics | awk 'index($0, "softswitch_engine_register_total{result=\"stored\"}") == 1 { print $NF }') > REGISTERED_BEFORE ))" 1

# 7k. The settings of a connection and of a trunk (destinations 87…; the
#     vendors are played by test/sipp/connlab.sh on 172.30.0.60–64 and look
#     at what they are sent). Each setting is one connection of "vendor S",
#     the cheaper route; where a second route exists it is vendor C.
conn() { "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac /work/test/sipp/connlab.sh "$@" 2>/dev/null; }
conn_id() { sql -c "select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id where v.name = 'Lab vendor S (settings)' and vc.name = '$1'"; }
between() { # between <case> <what> <value> <least> <most>
  if [ "${3:-x}" -ge "$4" ] 2>/dev/null && [ "$3" -le "$5" ]; then echo "PASS  $1: $2 ($3)"; else echo "FAIL  $1: $2: got '${3:-}', want $4-$5"; FAILED=1; fi
}
#     Ring timeout: the first vendor rings and never answers; after the
#     connection's five seconds (not the SIP core's thirty) the call moves on.
check ringtime "the vendor that only rings is given up, the next answers" "$(conn ring $RUN-ringtime 87015551112233)" "caller=0 vendors=0"
#     Longest call: five seconds on the connection, and on a trunk; the SIP
#     core ends the call on both sides, the engine's funds check does not.
CUT=$(cuts)
uac connmax uac-cutoff.xml 87025551112233 ""
check connmax "a call over a connection with a longest duration is ended" $? 0
uac trunkmax uac-cutoff.xml 885905551112233 ""
check trunkmax "a call from a trunk with a longest duration is ended" $? 0
#     Which answers move on. 486 ends a call by the standing rule; "always"
#     moves on all the same, "never" stops at a 500 that would move on, and
#     a list of codes moves on for those codes only.
check moveon "standing rule: 486 ends the call" "$(conn refused $RUN-mvstd 87075551112233 486 uac-refused.xml "s/__CODE__/486/")" "caller=0 vendors=0"
check moveon "every failure moves on: 486 goes to the next route" "$(conn refused $RUN-mvall 87035551112233 486)" "caller=0 vendors=0"
uac mvnone uac-refused.xml 87045551112233 "s/__CODE__/500/"
check moveon "no failure moves on: 500 ends the call" $? 0
uac mvlist1 uac-refused.xml 87055551112233 "s/__CODE__/500/"
check moveon "listed codes only: 500 is not listed and ends the call" $? 0
check moveon "listed codes only: 486 is listed and goes to the next route" "$(conn refused $RUN-mvlist2 87065551112233 486)" "caller=0 vendors=0"
#     Number format. Towards the vendor: two digits off, a prefix on, a "+"
#     in front. From the client: a trunk that writes 00… and one that writes
#     numbers the national way reach the same destination as 90….
check numfmt "the vendor gets the number in its own form" "$(conn number $RUN-numout 87085551112233 "[+]77085551112233")" "caller=0 vendors=0"
uac num00 uac.xml 88600905551112233 "" -d 500
check numfmt "a trunk that dials 00…: call answered" $? 0
uac numnat uac.xml 88705551112233 "" -d 500
check numfmt "a trunk that dials the national way: call answered" $? 0
#     One trunk whose client writes 00…, +… and the national way (T-17):
#     each form takes its own rule, 00… the 00 rule rather than the 0 one.
uac nummulti00 uac.xml 87700905551112233 "" -d 500 -timeout 20
check numfmt "a trunk with several rules, 00…: call answered" $? 0
uac nummultiplus uac.xml 877+905551112233 "" -d 500 -timeout 20
check numfmt "a trunk with several rules, +…: call answered" $? 0
uac nummultinat uac.xml 87705551112233 "" -d 500 -timeout 20
check numfmt "a trunk with several rules, national: call answered" $? 0
#     The caller's number towards one vendor: two digits off and a 0 on,
#     and the asserted identity set to the same; another connection removes
#     the identity; after a refusal the next vendor sees its own form, not
#     the first one's.
check shown "caller in the connection's form, asserted identity set to it" "$(conn shown $RUN-shown 87095551112233 902125550000 "sip:02125550000@" must "sip:02125550000@")" "caller=0 vendors=0"
check shown "asserted identity removed, caller untouched" "$(conn shown $RUN-nopai 87105551112233 902125550000 "sip:902125550000@" none)" "caller=0 vendors=0"
check shown "each vendor sees the caller in its own connection's form" "$(conn shown_after $RUN-shown2 87115551112233 902125550000 "sip:22902125550000@")" "caller=0 vendors=0 0"
check shown "a vendor that expects another form refuses (the check itself)" "$(conn shown $RUN-shownx 87095551112233 902125550000 "sip:902125550000@" any)" "caller=1 vendors=1"
#     A caller that asks for privacy (Privacy: id, T-16): no vendor is sent
#     its asserted identity, whether the connection passes it on (plain) or
#     sets it to the number shown (cli); without the request it goes on.
check private "privacy asked: asserted identity withheld" "$(conn private $RUN-privpass 87175551112233 902125550000 "sip:902125550000@" none)" "caller=0 vendors=0"
check private "privacy asked: withheld though the connection would set it" "$(conn private $RUN-privcli 87095551112233 902125550000 "sip:02125550000@" none)" "caller=0 vendors=0"
check private "no privacy asked: asserted identity passed on" "$(conn shown $RUN-privnone 87175551112233 902125550000 "sip:902125550000@" must "sip:902125550000@")" "caller=0 vendors=0"
#     Codecs. The caller offers PCMU, PCMA and key presses. One connection
#     lets only PCMA through, one puts PCMA first, a trunk lets only PCMU
#     through; a connection whose codec the caller does not offer is not
#     sent the call, and a trunk and a connection with nothing in common
#     are no route at all.
check codecs "only the connection's codec is offered" "$(conn codecs $RUN-cdone 87125551112233 "RTP.AVP 8 101" PCMU 8 PCMA)" "caller=0 vendors=0"
check codecs "the connection's order is the offer's order" "$(conn codecs $RUN-cdorder 87135551112233 "RTP.AVP 8 0 101" "no such text" 8 PCMA)" "caller=0 vendors=0"
check codecs "only the trunk's codec is offered" "$(conn codecs $RUN-cdtrunk 88887175551112233 "RTP.AVP 0 101" PCMA 0 PCMU)" "caller=0 vendors=0"
uac cdmiss uac.xml 87145551112233 "" -d 500
check codecs "a connection whose codec the caller lacks is passed over" $? 0
uac cdnone uac-refused.xml 88887125551112233 "s/__CODE__/503/"
check codecs "trunk and connection with no codec in common: no route" $? 0
#     A trunk's own limit: one call at a time, the second is refused.
uac trunkone uac-limited.xml 889905551112233 "s/__REFUSED__/503/" -d 3000 -m 2 -r 2 -l 2
check trunkone "sipp exit" $? 0
settle
check ringtime "legs" "$(legs ringtime)" "1:172.30.0.60:408 2:172.30.0.20:200"
between ringtime "seconds the first vendor was left ringing" "$(sql -c "select round(extract(epoch from end_at - start_at)) from cdrs where call_id like '$RUN-ringtime-%' and leg = 1")" 4 9
between connmax "seconds the call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-connmax-%'")" 5 6
between trunkmax "seconds the call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-trunkmax-%'")" 5 6
check connmax "ended by the SIP core's timer, not by the engine" "$(( $(cuts) - CUT ))" 0
check moveon "legs" "$(legs mvstd) / $(legs mvall) / $(legs mvnone) / $(legs mvlist1) / $(legs mvlist2)" \
  "1:172.30.0.61:486 / 1:172.30.0.61:486 2:172.30.0.20:200 / 1:172.30.0.22:500 / 1:172.30.0.22:500 / 1:172.30.0.61:486 2:172.30.0.20:200"
check numfmt "legs" "$(legs numout) / $(legs num00) / $(legs numnat)" "1:172.30.0.62:200 / 1:172.30.0.20:200 / 1:172.30.0.20:200"
check numfmt "legs, a trunk with several rules" "$(legs nummulti00) / $(legs nummultiplus) / $(legs nummultinat)" "1:172.30.0.20:200 / 1:172.30.0.20:200 / 1:172.30.0.20:200"
check numfmt "the records hold the numbers as tariffs know them" "$(sql -c "select string_agg(distinct dst, ' ') from cdrs where call_id like '$RUN-num00-%' or call_id like '$RUN-numnat-%' or call_id like '$RUN-nummulti%'")" 905551112233
check shown "legs" "$(legs shown) / $(legs nopai) / $(legs shown2)" "1:172.30.0.62:200 / 1:172.30.0.62:200 / 1:172.30.0.61:486 2:172.30.0.62:200"
check shown "the records keep the number that arrived" "$(sql -c "select string_agg(distinct src, ' ') from cdrs where call_id like '$RUN-shown%'")" 902125550000
check private "legs" "$(legs privpass) / $(legs privcli) / $(legs privnone)" "1:172.30.0.62:200 / 1:172.30.0.62:200 / 1:172.30.0.62:200"
check codecs "legs" "$(legs cdone) / $(legs cdorder) / $(legs cdtrunk) / $(legs cdmiss) / $(legs cdnone)" \
  "1:172.30.0.62:200 / 1:172.30.0.62:200 / 1:172.30.0.62:200 / 1:172.30.0.62:488 2:172.30.0.20:200 / 1:-:503"
check trunkone "records by code" "$(sql -c "select string_agg(sip_code::text, ' ' order by sip_code) from cdrs where call_id like '$RUN-trunkone-%'")" "200 503"

#     Signing in to a vendor. The password is set through the API, kept
#     sealed, given back by nothing and written in no log; the vendor that
#     asks (401, then one that asks with 407) gets the call at the second
#     try of the same attempt, with a higher CSeq. A wrong password is a
#     failed attempt like any other; so is no password at all. Each answer
#     is also worked out again by connlab.sh ("digest="), which is the only
#     check of the password behind 407. T-15: with qop=auth (nc, cnonce),
#     with SHA-256 preferred to MD5, and with SHA-512-256 the call goes
#     through; qop=auth-int alone fails (the switch hashes the body it was
#     sent, not the one the media relay rewrote) and so does an algorithm
#     the SIP core does not know: both move on to the next route.
VENDORPASS=lab-only-Vendor-2026
SIGNIN=$(conn_id sign-in)
# The body is put together here and not where it is used: inside "$(…)" this
# bash would take the braces and the comma for a list of alternatives.
CREDS='{"username":"lab-vendor","password":"'$VENDORPASS'"}'
jput() { local key=$1 path=$2 body=$3; shift 3; api "$key" PUT "$path" -H 'Content-Type: application/json' -d "$body" "$@"; }
check signin "another account cannot set it" "$(jput other "/v1/vendor-connections/$SIGNIN/sign-in" "$CREDS" -o /dev/null -w '%{http_code}')" 404
check signin "set through the API" "$(jput root "/v1/vendor-connections/$SIGNIN/sign-in" "$CREDS")" '{"auth_username":"lab-vendor","auth_set":true}'
check signin "the API says there is one and gives nothing back" "$(api root GET "/v1/vendor-connections/$SIGNIN" | python3 -c "
import json, sys
t = sys.stdin.read(); r = json.loads(t)
print(r['auth_set'], r['auth_username'], 'auth_secret' in r, '$VENDORPASS' in t)")" "True lab-vendor False False"
check signin "stored sealed with the installation's key" "$(sql -c "select get_byte(auth_secret, 0), position(convert_to('$VENDORPASS', 'UTF8') in auth_secret) from vendor_connections where id = $SIGNIN")" "1|0"
check signin "the audit log says that it was set, not what" "$(sql -c "select count(*), bool_or(coalesce(detail::text, '') || coalesce(changes::text, '') like '%$VENDORPASS%') from audit_log where resource = 'vendor-connections' and row_id = $SIGNIN and action = 'sign_in_set' and at >= '$STARTED'")" "1|f"
reload
check signin "the vendor asks (401) and gets the right password" "$(conn signin $RUN-signin 87155551112233 401 lab-vendor $VENDORPASS)" "caller=0 vendors=0 digest=ok"
check signin "a vendor that asks with 407 is answered too" "$(conn signin $RUN-signpx 87155551112233 407 lab-vendor $VENDORPASS)" "caller=0 vendors=0 digest=ok"
check signin "a password the vendor does not accept: the call moves on" "$(conn signin $RUN-signbad 87155551112233 401 lab-vendor not-the-password)" "caller=0 vendors=1 digest=bad"
check signin "behind 407 only the digest check sees a wrong password" "$(conn signin $RUN-signbadpx 87155551112233 407 lab-vendor not-the-password MD5 auth)" "caller=0 vendors=0 digest=bad"
check signin "qop=auth with 401: nc and cnonce" "$(conn signin $RUN-signqop 87155551112233 401 lab-vendor $VENDORPASS MD5 auth)" "caller=0 vendors=0 digest=ok"
check signin "qop=auth with 407: nc and cnonce" "$(conn signin $RUN-signqpx 87155551112233 407 lab-vendor $VENDORPASS MD5 auth)" "caller=0 vendors=0 digest=ok"
check signin "SHA-256 before MD5, qop auth or auth-int: SHA-256 and auth" "$(conn signin $RUN-signsha 87155551112233 401 lab-vendor $VENDORPASS "SHA-256 MD5" auth,auth-int)" "caller=0 vendors=0 digest=ok"
check signin "SHA-512-256 with qop=auth (407)" "$(conn signin $RUN-signs5 87155551112233 407 lab-vendor $VENDORPASS SHA-512-256 auth)" "caller=0 vendors=0 digest=ok"
check signin "qop=auth-int alone: the vendor refuses, the call moves on" "$(conn signin $RUN-signai 87155551112233 401 lab-vendor $VENDORPASS MD5 auth-int)" "caller=0 vendors=1 digest=auth-int"
check signin "an algorithm the switch does not know: no answer, the call moves on" "$(conn signin $RUN-signodd 87155551112233 401 lab-vendor $VENDORPASS SHA-1)" "caller=0 vendors=1 digest=none"
check signin "removed through the API" "$(api root DELETE "/v1/vendor-connections/$SIGNIN/sign-in")" '{"auth_username":"","auth_set":false}'
reload
check signin "without a password the vendor's question fails the attempt" "$(conn signin $RUN-signnone 87155551112233 401 lab-vendor $VENDORPASS)" "caller=0 vendors=1 digest=none"
settle
check signin "legs" "$(legs signin) / $(legs signpx) / $(legs signbad) / $(legs signnone)" "1:172.30.0.63:200 / 1:172.30.0.63:200 / 1:172.30.0.63:403 2:172.30.0.20:200 / 1:172.30.0.63:401 2:172.30.0.20:200"
check signin "legs (T-15)" "$(legs signbadpx) / $(legs signqop) / $(legs signqpx) / $(legs signsha) / $(legs signs5) / $(legs signai) / $(legs signodd)" \
  "1:172.30.0.63:200 / 1:172.30.0.63:200 / 1:172.30.0.63:200 / 1:172.30.0.63:200 / 1:172.30.0.63:200 / 1:172.30.0.63:403 2:172.30.0.20:200 / 1:172.30.0.63:401 2:172.30.0.20:200"
check signin "the password is in no log" "$({ docker logs softswitch-lab-opensips-1; docker logs softswitch-lab-route-engine-1; cat "$TMP/api.log"; } 2>&1 | grep -cF "$VENDORPASS")" 0

#     Asking a vendor whether it is there. Nobody is at the address of the
#     connection that is asked: the engine has it down, the call goes
#     straight to the other route, and the API shows the state. Then the
#     vendor comes to life (it answers OPTIONS): the engine notices within
#     its interval, the test of the connection says so at once, and the
#     call is sent there.
ASKED=$(conn_id asked)
peer() { engine http://127.0.0.1:58090/v1/peers | python3 -c "import json, sys; print(json.load(sys.stdin)['connections'].get('$ASKED', {}).get('state', 'none'))"; }
for _ in $(seq 1 30); do [ "$(peer)" == down ] && break; sleep 1; done
check health "a vendor that does not answer is marked down" "$(peer)" down
uac hdown uac.xml 87165551112233 "" -d 500
check health "its calls go to the other route" $? 0
check health "the API shows it as not reachable" "$(api root GET "/v1/vendor-connections/health?vendor_id=$(sql -c "select vendor_id from vendor_connections where id = $ASKED")" | python3 -c "
import json, sys
c = json.load(sys.stdin)['connections']
print(len(c), c['$ASKED']['state'], c['$ASKED']['last']['outcome'])" 2>&1)" "1 down no_answer"
check health "another account sees nothing of it" "$(api other GET "/v1/vendor-connections/health?vendor_id=$(sql -c "select vendor_id from vendor_connections where id = $ASKED")")" '{"connections":{},"registrations":{}}'
check health "the engine counts it" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_vendors_unreachable" { print ($2 >= 1) ? "yes" : "no" }')" yes
try() { api "${2:-root}" POST "/v1/vendor-connections/$1/test" | python3 -c "
import json, sys
r = json.load(sys.stdin)
print(r.get('outcome', r.get('error')), r.get('sip_code', ''))" 2>&1; }
AUDITED=$(sql -c "select count(*) from audit_log where at >= '$STARTED'")
check try "nobody there: no answer" "$(try "$ASKED")" "no_answer "
conn alive $RUN-halive 87165551112233 14 > "$TMP/alive" &
sleep 9
check try "the vendor is there: answered, with its code" "$(try "$ASKED")" "answered 200"
wait $!
check health "once it answers, the call is sent to it" "$(cat "$TMP/alive")" "caller=0 vendors="
check health "the API shows it as reachable" "$(api root GET "/v1/vendor-connections/health?vendor_id=$(sql -c "select vendor_id from vendor_connections where id = $ASKED")" | python3 -c "
import json, sys
c = json.load(sys.stdin)['connections']['$ASKED']
print(c['state'], c['last']['outcome'], c['last']['sip_code'])" 2>&1)" "up answered 200"
#     The test of a connection in the other cases: nothing listens (a TLS
#     vendor whose port is closed), a certificate under another name, a host
#     name nobody knows. Another account may not test it, and none of this
#     leaves a trace in the audit log.
by_vendor() { sql -c "select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id where v.name = '$1' and vc.name = 'lab'"; }
check try "nothing listens: refused" "$(try "$(by_vendor 'Lab vendor E (TLS, down)')")" "refused "
check try "certificate under another name" "$(try "$(by_vendor 'Lab vendor G (wrong name)')")" "certificate "
sql -c "update vendor_connections set host = 'nowhere.lab.invalid' where id = $(conn_id plain)"
check try "a host name nobody knows" "$(try "$(conn_id plain)")" "unresolved "
sql -c "update vendor_connections set host = '172.30.0.62' where id = $(conn_id plain)"
check try "another account may not test it" "$(try "$ASKED" other)" "not found "
check try "tests leave nothing in the audit log" "$(sql -c "select count(*) from audit_log where at >= '$STARTED'")" "$AUDITED"
settle
check health "legs: down, then up" "$(legs hdown) / $(legs halive)" "1:172.30.0.20:200 / 1:172.30.0.64:200"
check health "calls passed over are counted" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_health_skipped_total" { print ($2 >= 1) ? "yes" : "no" }')" yes

#     Registering with a vendor (T-14, ADR-0035). The registration is turned
#     on through the API; the SIP core registers from its own address
#     (uas-registrar.xml checks Via and Contact), as the sign-in name at the
#     domain set on the connection, answers the vendor's 401 with the
#     sign-in password, and renews the registration before the 20 seconds
#     the vendor grants run out (the second REGISTER of the same
#     registration). The engine and the API show it registered; a call to
#     the vendor goes through. A wrong password: the vendor refuses, the
#     state turns to failing and the SIP core tries again (a second attempt
#     reaches the vendor). With the right password back it registers again;
#     turned off, it is no longer listed. The password is in no log and in
#     no audit record.
REGPASS=lab-only-Registrar-2026
REGCONN=$(conn_id registers)
REGVENDOR=$(sql -c "select vendor_id from vendor_connections where id = $REGCONN")
REGCREDS='{"username":"lab-reg","password":"'$REGPASS'"}'
REGWRONG='{"username":"lab-reg","password":"not-the-password"}'
REGON='{"register":true,"register_expires_sec":3600,"register_domain":"reg.lab.test"}'
REGOFF='{"register":false}'
jpatch() { local key=$1 path=$2 body=$3; shift 3; api "$key" PATCH "$path" -H 'Content-Type: application/json' -d "$body" "$@"; }
registration() { # registration [field]: the engine's word on the connection's registration
  engine http://127.0.0.1:58090/v1/peers | python3 -c "import json, sys; print(json.load(sys.stdin)['registrations'].get('$REGCONN', {}).get('${1:-state}', 'none'))"
}
until_registration() { # until_registration <state> <seconds>
  for _ in $(seq 1 "$2"); do [ "$(registration)" == "$1" ] && return; sleep 1; done
}
check vendorreg "the SIP core's list is the engine's secret" "$(ask http://127.0.0.1:58090/v1/vendor-registrations)" 401
conn registrar 1 lab-reg $REGPASS reg.lab.test 20 2 $RUN-regcall 87405551112233 14 > "$TMP/registrar" &
REGPID=$!
sleep 3
check vendorreg "sign-in set through the API" "$(jput root "/v1/vendor-connections/$REGCONN/sign-in" "$REGCREDS" -o /dev/null -w '%{http_code}')" 200
check vendorreg "turned on through the API" "$(jpatch root "/v1/vendor-connections/$REGCONN" "$REGON" | python3 -c "
import json, sys
r = json.load(sys.stdin)
print(r['register'], r['register_expires_sec'], r['register_domain'])")" "True 3600 reg.lab.test"
until_registration registered 30
check vendorreg "the engine shows it registered, for the time the vendor granted" "$(registration) $(registration expires)" "registered 20"
check vendorreg "the API shows it" "$(api root GET "/v1/vendor-connections/health?vendor_id=$REGVENDOR" | python3 -c "
import json, sys
r = json.load(sys.stdin)['registrations']['$REGCONN']
print(r['state'], r['reason'])" 2>&1)" "registered registered"
check vendorreg "another account sees nothing of it" "$(api other GET "/v1/vendor-connections/health?vendor_id=$REGVENDOR")" '{"connections":{},"registrations":{}}'
check vendorreg "the engine counts it" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_vendor_registrations{state=\"registered\"}" { print $2 }')" 1
wait $REGPID
check vendorreg "registered, renewed before it ran out, and a call to the vendor goes through" "$(cat "$TMP/registrar")" "registrar=0 caller=0"
check vendorreg "still registered after the renewal" "$(registration)" registered
#     A wrong password. The change reaches the SIP core at once and is used
#     at the next renewal; every attempt that reaches the vendor is refused.
check vendorreg "a wrong password set" "$(jput root "/v1/vendor-connections/$REGCONN/sign-in" "$REGWRONG" -o /dev/null -w '%{http_code}')" 200
conn registrar 2 lab-reg $REGPASS reg.lab.test 20 1 > "$TMP/registrar" &
REGPID=$!
until_registration failing 50
check vendorreg "a wrong password: failing, refused credentials" "$(registration) $(registration reason)" "failing credentials"
check vendorreg "the engine counts it" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 == "softswitch_engine_vendor_registrations{state=\"failing\"}" { print $2 }')" 1
wait $REGPID
check vendorreg "the SIP core tries again: two attempts reach the vendor" "$(cat "$TMP/registrar")" "registrar=0"
check vendorreg "still failing" "$(registration)" failing
#     The right password again: registered at the next try.
conn registrar 1 lab-reg $REGPASS reg.lab.test 20 1 > "$TMP/registrar" &
REGPID=$!
sleep 3
jput root "/v1/vendor-connections/$REGCONN/sign-in" "$REGCREDS" -o /dev/null
until_registration registered 30
check vendorreg "the right password again: registered" "$(registration)" registered
wait $REGPID
check vendorreg "the vendor took it" "$(cat "$TMP/registrar")" "registrar=0"
#     Turned off: the SIP core lets it go and the engine lists it no more.
jpatch root "/v1/vendor-connections/$REGCONN" "$REGOFF" -o /dev/null
until_registration none 10
check vendorreg "turned off: not listed" "$(registration)" none
check vendorreg "the SIP core holds no registration" "$(engine http://127.0.0.1:58090/v1/vendor-registrations | python3 -c "import json, sys; print(len(json.load(sys.stdin)['registrations']))")" 0
api root DELETE "/v1/vendor-connections/$REGCONN/sign-in" -o /dev/null
check vendorreg "the audit log says what changed, never the password" "$(sql -c "select count(*) > 0, bool_or(coalesce(detail::text, '') || coalesce(changes::text, '') like '%$REGPASS%') from audit_log where resource = 'vendor-connections' and row_id = $REGCONN and at >= '$STARTED'")" "t|f"
settle
check vendorreg "legs" "$(legs regcall)" "1:172.30.0.93:200"
check vendorreg "the password is in no log" "$({ docker logs softswitch-lab-opensips-1; docker logs softswitch-lab-route-engine-1; cat "$TMP/api.log"; } 2>&1 | grep -cF "$REGPASS")" 0

# 7l. A caller that gives up (CANCEL). Whatever the moment, the call must
#     leave nothing behind in the state store (call, counters, money held)
#     and be recorded once, with 487 when it was never answered.
#     - while the vendor rings (8701: a next route exists, and the standing
#       rule moves on after a timeout), and where every failure moves on
#       (8718): the vendor gets the CANCEL and no next route is tried;
#     - too late, after the answer: the call goes on and is ended by BYE;
#     - while the SIP core still waits for the engine's decision (8719: the
#       lab engine answers 0.8 s late; LAB_DECIDE_DELAY).
LAB_CLIENT=$(sql -c "select id from clients where name = 'Lab SIPp'")
left_of() { # left_of <case> [client id]: what the state store still holds for the case's calls
  local n=0 key client=${2:-$LAB_CLIENT}
  n=$(( n + $("${COMPOSE[@]}" exec -T redis redis-cli --scan --pattern "call:$RUN-$1-*" | wc -l) ))
  for key in cc:all "cc:c:$client"; do
    n=$(( n + $("${COMPOSE[@]}" exec -T redis redis-cli zrange "$key" 0 -1 | grep -c -- "$RUN-$1-") ))
  done
  n=$(( n + $("${COMPOSE[@]}" exec -T redis redis-cli hkeys "held:$client" | grep -c -- "$RUN-$1-") ))
  echo "$n"
}
check cancel "while ringing: the vendor gets the CANCEL, the caller 487" "$(conn give_up $RUN-cxring 87015551112233)" "caller=0 vendors=0"
check cancel "every failure moves on: a caller who gives up is not moved on" "$(conn give_up $RUN-cxall 87185551112233)" "caller=0 vendors=0"
uac cxlate uac-cancel-late.xml 905551112233 "" -timeout 20
check cancel "too late, after the answer: the call goes on and ends with BYE" $? 0
uac cxwait uac-cancel.xml 87195551112233 "/__PROVISIONAL__/d; s/__OPTIONAL_100__/false/; s/__WAIT__/200/" -timeout 20
check cancel "before the engine has decided: CANCEL answered, the caller 487" $? 0
# The engine's late decision lands 0.8 s after the call came in.
sleep 3
settle
check cancel "legs" "$(legs cxring) / $(legs cxall) / $(legs cxlate) / $(legs cxwait)" \
  "1:172.30.0.60:487 / 1:172.30.0.60:487 / 1:172.30.0.20:200 / 1:172.30.0.20:487"
between cancel "seconds the late-cancelled call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-cxlate-%'")" 1 2
for c in cxring cxall cxlate cxwait; do
  check cancel "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7m. An answer the caller never acknowledges (ADR-0026). The SIP core ends
#     the call 32 seconds after the answer with a BYE to both sides; the
#     record keeps 200 and the real duration, and nothing stays behind. A
#     caller whose ACK comes three seconds late keeps its call past the 32
#     seconds and hangs up itself. Both calls run at once (8720, 8721; the
#     vendors at .65 and .66 check that each gets its BYE).
check ackwait "the switch ends the unacknowledged call on both sides; the late ACK keeps its call" \
  "$(conn ack_wait $RUN-acknever $RUN-acklate)" "never=0 late=0 vendors=0 0"
settle
check ackwait "legs" "$(legs acknever) / $(legs acklate)" "1:172.30.0.65:200 / 1:172.30.0.66:200"
between ackwait "seconds the unacknowledged call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-acknever-%'")" 31 34
between ackwait "seconds the late-acknowledged call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-acklate-%'")" 35 38
# The money held for a call is let go once the rater has settled its
# charge, a moment after the record: the late call ended only just now.
for c in acknever acklate; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check ackwait "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7n. New offers within a call (re-INVITE). The engine is not told of them:
#     the call keeps its duration, its money held and its one record. One
#     call (8722, the connection lets only PCMA through) has every kind:
#     hold and resume from the caller, an offer the vendor refuses (488)
#     between them, the vendor making the call inactive, and an offer of
#     the vendor's that the caller refuses; every offer and answer must name
#     the media relay and carry the connection's codecs only. A call of at
#     most five seconds (8724) held after two is still ended five seconds
#     after the answer. Towards a side behind address translation the new
#     offer goes where that side was heard from: the caller "lab-nat-2"
#     (8723) and the registered PBX "lab-nat" (a call delivered to it).
event_count() { curl -fsS http://127.0.0.1:58090/metrics | awk -v e="softswitch_engine_events_total{event=\"$1\"}" '$1 == e { print $2 }'; }
FAILED_EVENTS=$(event_count failed)
ANSWERED_EVENTS=$(event_count answered)
check reinvite "both ways, held and resumed, refused once each way; the call goes on" "$(conn reoffer $RUN-reoffer 87225551112233)" "caller=0 vendors=0"
check reinvite "a new offer does not lengthen a call of at most five seconds" "$(conn reoffer_short $RUN-reshort 87245551112233)" "caller=0 vendors=0"
check reinvite "the vendor's new offer reaches the caller behind address translation" "$(nat reoffer_caller $RUN-renat 87235551112233)" "caller=0 vendor=0"
check reinvite "the caller's new offer reaches the registered PBX behind address translation" "$(nat reoffer_trunk $RUN-retrunk 89885551112233)" "caller=0 pbx=0"
settle
check reinvite "the engine heard of the answers only: no failure, one answer per call" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count answered) - ANSWERED_EVENTS ))" "0 4"
check reinvite "one record per call" "$(legs reoffer) / $(legs reshort) / $(legs renat) / $(legs retrunk)" \
  "1:172.30.0.67:200 / 1:172.30.0.69:200 / 1:172.30.0.68:200 / 1:-:200"
#     The caller of 8722 talks 0.3 s, then the new offers, then 0.8 s.
between reinvite "seconds the call with every kind of new offer lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-reoffer-%'")" 1 3
between reinvite "seconds the call of at most five seconds lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-reshort-%'")" 5 6
check reinvite "charged for the time talked" "$(sql -c "select bool_and(sell_amount > 0) from cdrs where call_id like '$RUN-reoffer-%' or call_id like '$RUN-reshort-%' or call_id like '$RUN-renat-%' or call_id like '$RUN-retrunk-%'")" t
NAT_CLIENT=$(sql -c "select id from clients where name = 'Lab SIPp NAT'")
for c in reoffer reshort renat retrunk; do
  client=$LAB_CLIENT; [ $c = renat ] && client=$NAT_CLIENT
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c $client)" = 0 ] && break; sleep 1; done
  check reinvite "$c: nothing left in the state store" "$(left_of $c $client)" 0
done

# 7o. Dead calls (ADR-0027). The media relay ends a call it has heard
#     nothing from, either way, for a minute (RTP_TIMEOUT, the production
#     default in the lab too): the SIP core sends BYE to both sides and the
#     engine is told. Four calls at once (8725–8728, vendors at .73–.76 that
#     echo the audio), three seconds of audio each: one then goes silent and
#     must be ended about 63 seconds after the answer; one talks on for 70
#     seconds and is hung up by its caller after 65; two are held (sendonly,
#     inactive) with nothing sent by either side, and are hung up by their
#     callers after 68.5: a held call has the longer timeout
#     (RTP_HOLD_TIMEOUT). Every call asks for session timers (RFC 4028):
#     the headers pass both ways unchanged and nobody refreshes. A fifth
#     call (8733 at .86, a connection that may ring 90 s) rings with a 183
#     with early media and no media either way: the relay's timeout gives
#     it up while it rings, though the connection's ring timeout is longer
#     (ADR-0038): the vendor gets CANCEL, the caller 487, the record is 487.
python3 - "$ROOT/test/load/out" <<'PCAP'
import struct, sys
# G.711 silence, 20 ms apart, as capture files SIPp can play: 3 s, 70 s
# and (for the reservations, 7s) 140 s.
for name, count in (('short', 150), ('long', 3500), ('reserve', 7000)):
    out = open(sys.argv[1] + '/media-' + name + '.pcap', 'wb')
    out.write(struct.pack('<IHHiIII', 0xa1b2c3d4, 2, 4, 0, 0, 65535, 1))
    for i in range(count):
        rtp = struct.pack('!BBHII', 0x80, 8, (i + 1) & 0xffff, 160 * (i + 1), 0x5678abcd) + b'\xd5' * 160
        udp = struct.pack('!HHHH', 6000, 6000, 8 + len(rtp), 0) + rtp
        ip = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), i & 0xffff, 0, 64, 17, 0, bytes([10, 0, 0, 6]), bytes([10, 0, 0, 1])) + udp
        frame = b'\x02\x00\x00\x00\x00\x02' + b'\x02\x00\x00\x00\x00\x01' + b'\x08\x00' + ip
        out.write(struct.pack('<IIII', i // 50, (i % 50) * 20000, len(frame), len(frame)) + frame)
PCAP
timeouts() { curl -fsS http://127.0.0.1:52225/metrics | awk 'index($0, "rtpengine_closed_sessions_total{reason=\"timeout\"}") == 1 { print $NF }'; }
TIMEOUTS=$(timeouts)
check deadcall "the dead call is ended on both sides; the talking and the held calls are not" \
  "$(conn media_timeout $RUN-mdead $RUN-mtalk $RUN-msend $RUN-minact $RUN-msilent)" "dead=0 talk=0 sendonly=0 inactive=0 silent=0 vendors=0 0 0 0 0"
settle
check deadcall "legs" "$(legs mdead) / $(legs mtalk) / $(legs msend) / $(legs minact) / $(legs msilent)" \
  "1:172.30.0.73:200 / 1:172.30.0.74:200 / 1:172.30.0.75:200 / 1:172.30.0.76:200 / 1:172.30.0.86:487"
between deadcall "seconds the silent early media rang before it was given up (ring timeout 90)" \
  "$(sql -c "select extract(epoch from end_at - start_at)::int from cdrs where call_id like '$RUN-msilent-%'")" 58 66
between deadcall "seconds the dead call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-mdead-%'")" 62 66
between deadcall "seconds the talking call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-mtalk-%'")" 65 68
between deadcall "seconds the held calls lasted (the shorter)" "$(sql -c "select min(duration_sec) from cdrs where call_id like '$RUN-msend-%' or call_id like '$RUN-minact-%'")" 68 71
check deadcall "it was the media relay's timeout, for the dead and the silent ringing call" "$(( $(timeouts) - TIMEOUTS >= 2 ))" 1
check deadcall "charged for the time up to the end" "$(sql -c "select bool_and(sell_amount > 0) from cdrs where call_id like '$RUN-mdead-%' or call_id like '$RUN-mtalk-%' or call_id like '$RUN-msend-%' or call_id like '$RUN-minact-%'")" t
for c in mdead mtalk msend minact msilent; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check deadcall "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7p. Fax and key presses (ADR-0031). A new offer of T.38 (m=image udptl
#     t38) passes the media relay as it was made, from either side, and so
#     does the way back to audio; the connection's codec list (8729: PCMA
#     only) shapes the audio offers and leaves the fax offer alone. Key
#     presses sent as SIP INFO pass both ways unchanged; nothing turns them
#     into telephone-event or back, and telephone-event stays in the audio
#     offers. A fax call (8730) that carries nothing but fax datagrams for 70
#     seconds is not ended as a dead call (7o): the relay counts them as
#     media. Both calls run at once (vendors at .77 and .78).
python3 - "$ROOT/test/load/out" <<'PCAP'
import struct, sys
# UDPTL datagrams (T.38 annex D), five a second for 72 s: a sequence number,
# one primary IFP packet (a "no signal" indicator), no redundant ones.
out = open(sys.argv[1] + '/fax-udptl.pcap', 'wb')
out.write(struct.pack('<IHHiIII', 0xa1b2c3d4, 2, 4, 0, 0, 65535, 1))
for i in range(360):
    udptl = struct.pack('!HB', i & 0xffff, 2) + b'\x00\x00' + b'\x00'
    udp = struct.pack('!HHHH', 6000, 6000, 8 + len(udptl), 0) + udptl
    ip = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), i & 0xffff, 0, 64, 17, 0, bytes([10, 0, 0, 6]), bytes([10, 0, 0, 1])) + udp
    frame = b'\x02\x00\x00\x00\x00\x02' + b'\x02\x00\x00\x00\x00\x01' + b'\x08\x00' + ip
    out.write(struct.pack('<IIII', i // 5, (i % 5) * 200000, len(frame), len(frame)) + frame)
PCAP
FAILED_EVENTS=$(event_count failed)
ANSWERED_EVENTS=$(event_count answered)
check fax "T.38 both ways and back, key presses both ways; the fax call outlives the media timeout" \
  "$(conn fax $RUN-fax $RUN-faxkeep)" "full=0 keep=0 vendors=0 0"
settle
check fax "the engine heard of the answers only: no failure, one answer per call" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count answered) - ANSWERED_EVENTS ))" "0 2"
check fax "one record per call" "$(legs fax) / $(legs faxkeep)" "1:172.30.0.77:200 / 1:172.30.0.78:200"
between fax "seconds the call with fax and key presses lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-fax-%'")" 1 4
between fax "seconds the call with fax datagrams only lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-faxkeep-%'")" 70 73
for c in fax faxkeep; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check fax "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7p2. Codec translation (transcoding, T-09). The lab client's trunk 880
#      speaks PCMA only; connection "transcode" (8737, at .100) PCMU only and
#      lets the media relay translate, "pcmu-only" (8738, at .101) does not;
#      trunk 879 speaks PCMA only and may be translated. Vendor C (.20)
#      answers when the first route is passed over. The vendor is offered
#      PCMU and key presses only, the caller gets PCMA back, and audio
#      crosses the relay's translators both ways (its own counters); a new
#      offer of the vendor's reaches the caller in PCMA. Without translation
#      the route is passed over as before; so it is with as many calls being
#      translated as the engine allows (TRANSCODE_MAX), which the scenario
#      makes up with placeholders in the engine's count.
xcoded() { curl -fsS http://127.0.0.1:52225/metrics | awk -v p="rtpengine_transcode_packets_total{chain=\"$1\"}" 'index($0, p) == 1 { n = $NF } END { print n + 0 }'; }
emetric() { curl -fsS http://127.0.0.1:58090/metrics | awk -v p="$1" '$1 == p { print $2 }'; }
TO_PCMU=$(xcoded "PCMA/8000 -> PCMU/8000")
TO_PCMA=$(xcoded "PCMU/8000 -> PCMA/8000")
TRANSCODED=$(emetric softswitch_engine_transcoded_total)
TRANSCODE_FULL=$(emetric softswitch_engine_transcode_full_total)
check transcode "the connection translates: PCMU to the vendor, PCMA to the caller, both ways and within the call" \
  "$(conn transcode $RUN-xcode 88087375551112233 full 172.30.0.100)" "caller=0 vendors=0"
check transcode "the trunk lets a connection that does not translate be translated" \
  "$(conn transcode $RUN-xtrunk 87987385551112233 plain 172.30.0.101)" "caller=0 vendors=0"
check transcode "the audio went through the relay's translators both ways" \
  "$(( $(xcoded "PCMA/8000 -> PCMU/8000") - TO_PCMU >= 250 )) $(( $(xcoded "PCMU/8000 -> PCMA/8000") - TO_PCMA >= 250 ))" "1 1"
check transcode "no translation: the connection without a codec in common is passed over" \
  "$(conn transcode $RUN-xoff 88087385551112233 plain)" "caller=0 vendors="
XMAX=$(emetric softswitch_engine_transcoding_max)
XFILL="" XNAMES="" XUNTIL=$(( $(date +%s) + 600 ))
for i in $(seq 1 "$XMAX"); do XFILL="$XFILL $XUNTIL $RUN-xfill-$i"; XNAMES="$XNAMES $RUN-xfill-$i"; done
"${COMPOSE[@]}" exec -T redis redis-cli zadd cc:x $XFILL >/dev/null
check transcode "at the engine's limit ($XMAX) the translating connection is passed over" \
  "$(conn transcode $RUN-xfull 88087375551112233 plain)" "caller=0 vendors="
"${COMPOSE[@]}" exec -T redis redis-cli zrem cc:x $XNAMES >/dev/null
settle
check transcode "legs" "$(legs xcode) / $(legs xtrunk) / $(legs xoff) / $(legs xfull)" \
  "1:172.30.0.100:200 / 1:172.30.0.101:200 / 1:172.30.0.20:200 / 1:172.30.0.20:200"
check transcode "the engine counted the translated calls and the route passed over at the limit" \
  "$(( $(emetric softswitch_engine_transcoded_total) - TRANSCODED )) $(( $(emetric softswitch_engine_transcode_full_total) - TRANSCODE_FULL ))" "2 1"
check transcode "no call is counted as translated any more" "$(emetric softswitch_engine_transcoding_calls)" 0
for c in xcode xtrunk xoff xfull; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check transcode "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7q. Early media, PRACK and transfers (ADR-0028). A vendor's 183 that
#     requires reliability (Require: 100rel, RSeq) reaches the caller with
#     its early media through the media relay; the caller's PRACK reaches the
#     vendor with its RAck, and the answer to it comes back (8731, at .79).
#     Once answered, each side asks for a transfer (REFER): the SIP core
#     refuses both with 403, neither reaches the other side, the call goes on
#     and the refusals are counted. A 183 with early media and no 100rel
#     passes the same way (8732, at .85). Both calls run at once.
refused_refers() { curl -fsS http://127.0.0.1:58888/metrics | awk '$1 == "opensips_refer_refused" { print $2 }'; }
REFERS=$(refused_refers)
FAILED_EVENTS=$(event_count failed)
RINGING_EVENTS=$(event_count ringing)
ANSWERED_EVENTS=$(event_count answered)
check early "183 with 100rel and PRACK both ways, a REFER refused each way; 183 without 100rel" \
  "$(conn early $RUN-prack $RUN-early)" "reliable=0 early=0 vendors=0 0"
settle
check early "the SIP core counted both refused transfers" "$(( $(refused_refers) - REFERS ))" 2
check early "the engine heard of the progress and the answers only" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count ringing) - RINGING_EVENTS )) $(( $(event_count answered) - ANSWERED_EVENTS ))" "0 2 2"
check early "one record per call" "$(legs prack) / $(legs early)" "1:172.30.0.79:200 / 1:172.30.0.85:200"
between early "seconds the call with the refused transfers lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-prack-%'")" 2 4
between early "seconds the call with unreliable early media lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-early-%'")" 0 2
for c in prack early; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check early "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7q2. Progress reported more than once (T-46; RFC 3261 17.1.1). The first
#      vendor (8745 at .105) sends 180, 183 and 180, then refuses with 503;
#      the next (.106) sends 180 and 183, then answers. The engine hears
#      "ringing" once per attempt: once for the first, once again for the
#      second, which is a new attempt. Both attempts are recorded.
FAILED_EVENTS=$(event_count failed)
RINGING_EVENTS=$(event_count ringing)
ANSWERED_EVENTS=$(event_count answered)
check progress "two attempts, each reporting progress more than once" "$(conn progress $RUN-progress 87455551112233)" "caller=0 vendors=0 0"
settle
check progress "the engine heard of the first progress of each attempt only" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count ringing) - RINGING_EVENTS )) $(( $(event_count answered) - ANSWERED_EVENTS ))" "1 2 1"
check progress "legs" "$(legs progress)" "1:172.30.0.105:503 2:172.30.0.106:200"
for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of progress)" = 0 ] && break; sleep 1; done
check progress "nothing left in the state store" "$(left_of progress)" 0

# 7r. Without the media relay (ADR-0030). A call whose offer the relay
#     cannot take is refused with 503 and no other route is tried, though
#     8734's connection moves on after every failure: the fault is the
#     switch's own. Nothing is sent to the vendor (.87, where nobody
#     listens); the engine is told the call ended (not failed) with 503,
#     records it once with that code and keeps nothing; the SIP core counts
#     the refusals. The event carries the cause "media" (ADR-0039): the
#     record still names 8734's connection, but the engine's live view does
#     not count the attempts against it. Two ways: the relay has no ports
#     left (every port of its range is taken by another process inside its
#     container: "Ran out of ports"), then it is stopped (no answer: the first call waits for the
#     module's 1 s x 5 tries). The relay is shared by every call, so this
#     section runs alone; whatever happens, the relay is given back its
#     ports and started again, and the section ends when a call goes
#     through it (the SIP core asks a relay it set aside again every 10 s).
media_refused() { curl -fsS http://127.0.0.1:58888/metrics | awk '$1 == "opensips_media_refused" { print $2 }'; }
live_conn() { # live_conn <connection id>: "attempts failed_network code_503" of the last minute in the engine's live view
  engine http://127.0.0.1:58090/v1/live | python3 -c "
import json, sys
want = int(sys.argv[1])
found = [c for c in json.load(sys.stdin)['connections'] if c['connection_id'] == want]
print(*([found[0]['attempts'], found[0]['failed_network'], found[0]['code_503']] if found else [0, 0, 0]))" "$1"
}
relay_back() { # relay_back: the relay with all its ports, answering; false if no call gets through in 30 tries
  local i
  "${COMPOSE[@]}" exec -T rtpengine rm -f /tmp/hold >/dev/null 2>&1
  "${COMPOSE[@]}" start rtpengine >/dev/null 2>&1
  for i in $(seq 1 30); do
    uac "nmback$i" uac.xml 905551112233 "" -timeout 10 && return 0
    sleep 1
  done
  return 1
}
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup; relay_back' EXIT
FAILED_EVENTS=$(event_count failed)
ENDED_EVENTS=$(event_count ended)
REFUSED_MEDIA=$(media_refused)
ANSWERED_NOMEDIA=$(event_count answered)
# Holds every port of the relay's range (30000-39999) on its address for
# at most a minute, or until /tmp/hold is removed; /tmp/held says how many.
"${COMPOSE[@]}" exec -T rtpengine sh -c 'cat > /tmp/hold.pl && rm -f /tmp/held && touch /tmp/hold' <<'HOLD'
use Socket;
my ($n, @held) = (0);
for my $port (30000 .. 39999) {
  socket(my $s, PF_INET, SOCK_DGRAM, 0) or die "socket: $!";
  if (bind($s, sockaddr_in($port, inet_aton("172.30.0.11")))) { push @held, $s; $n++ }
}
open(my $out, ">", "/tmp/held") or die; print $out "$n\n"; close $out;
my $until = time + 60;
sleep 1 while -e "/tmp/hold" && time < $until;
HOLD
"${COMPOSE[@]}" exec -d rtpengine perl /tmp/hold.pl
for _ in $(seq 1 20); do "${COMPOSE[@]}" exec -T rtpengine test -s /tmp/held && break; sleep 0.5; done
#     The relay may still keep sessions of earlier calls (an offer that was
#     never answered stays until its offer timeout); their ports count too.
RELAY_USED=$(curl -fsS http://127.0.0.1:52225/metrics | awk 'index($0, "rtpengine_ports_used{") == 1 { n += $NF } END { print n + 0 }')
check nomedia "every port of the relay's range is taken" "$(( $("${COMPOSE[@]}" exec -T rtpengine cat /tmp/held 2>/dev/null) + RELAY_USED ))" 10000
uac nmports uac-refused.xml 87345551112233 "s/__CODE__/503/" -timeout 20
check nomedia "no ports left: the caller gets 503" $? 0
"${COMPOSE[@]}" exec -T rtpengine rm -f /tmp/hold
"${COMPOSE[@]}" stop -t 2 rtpengine >/dev/null 2>&1
SINCE=$(date +%s)
uac nmdown uac-refused.xml 87345551112233 "s/__CODE__/503/" -timeout 20
check nomedia "the relay does not answer: the caller gets 503" $? 0
between nomedia "seconds the first caller waited for it" "$(( $(date +%s) - SINCE ))" 1 10
uac nmdown2 uac-refused.xml 87345551112233 "s/__CODE__/503/" -timeout 20
check nomedia "set aside: the next caller gets 503 at once" $? 0
#     An INVITE without an offer (T-43, ADR-0043) reaches the vendor
#     (8744 at .94), whose offer in the 200 the relay cannot take: the 200
#     reaches the caller without SDP and both sides get BYE; the engine is
#     told the call ended with 503 and the cause "media", not that it was
#     answered.
check nomedia "an offer in the 200 the relay cannot take: no SDP to the caller, BYE to both" \
  "$(conn late $RUN-nmlate 87445551112233 refused 172.30.0.94 take bare)" "caller=0 vendors=0"
settle
check nomedia "the engine was told the calls ended, never that they failed" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count ended) - ENDED_EVENTS )) $(( $(event_count answered) - ANSWERED_NOMEDIA ))" "0 4 0"
check nomedia "one record per call, 503, no other route tried" "$(legs nmports) / $(legs nmdown) / $(legs nmdown2) / $(legs nmlate)" \
  "1:172.30.0.87:503 / 1:172.30.0.87:503 / 1:172.30.0.87:503 / 1:172.30.0.94:503"
check nomedia "the SIP core counted the refusals, once a call" "$(( $(media_refused) - REFUSED_MEDIA ))" 4
NOMEDIA_CONN=$(sql -c "select id from vendor_connections where host = '172.30.0.87'")
check nomedia "the vendor never asked is not blamed: its connection counted no attempt, failure or 503" "$(live_conn "$NOMEDIA_CONN")" "0 0 0"
NOMEDIA_LATE=$(sql -c "select id from vendor_connections where host = '172.30.0.94'")
check nomedia "nor is the vendor whose offer the relay refused" "$(live_conn "$NOMEDIA_LATE")" "0 0 0"
check nomedia "nothing was charged" "$(sql -c "select count(*) from cdrs where (call_id like '$RUN-nmports-%' or call_id like '$RUN-nmdown-%' or call_id like '$RUN-nmdown2-%' or call_id like '$RUN-nmlate-%') and (sell_amount <> 0 or buy_amount <> 0)")" 0
for c in nmports nmdown nmdown2 nmlate; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check nomedia "$c: nothing left in the state store" "$(left_of $c)" 0
done
relay_back
check nomedia "the relay is back: calls go through it again" $? 0
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup' EXIT

# 7s. Reservations past the first step (T-03, ADR-0005), with the default
#     RESERVE_SEC (60). Two calls at once, with audio all along so the
#     media relay ends neither (connlab.sh "reservation"):
#     - the lab client's call of 130 s (8735): the money held is extended
#       50 and 110 seconds after the answer (two steps counted); when the
#       call ends it falls to the call's charge and stays held until the
#       rater has settled it, then nothing is held. The rater is paused at
#       the end of the call so that moment can be seen;
#     - the client "Lab SIPp reserve" (8736, tech prefix 554) has 200
#       seconds' money when the call starts, so the call's limit on the SIP
#       core is 200 s; once 60 are held the operator lowers the balance to
#       90 seconds' money (it went elsewhere). The second step finds only
#       30, so the engine holds those and ends the call through the SIP
#       core's management interface a second before they run out (at
#       89 s); it is billed no more than was held. Only the engine can end
#       it there: with the limit equal to the end of the funds the SIP
#       core's timer may come first (T-29, ended_once).
#     Each call leaves one record, and the balances move by exactly what
#     the records say.
RS_CLIENT=$(sql -c "select id from clients where name = 'Lab SIPp reserve'")
VENDOR_S=$(sql -c "select id from vendors where name = 'Lab vendor S (settings)'")
sql -c "update clients set balance = 200000 where id = $RS_CLIENT"; reload
extended() { engine http://127.0.0.1:58090/v1/stats | sed 's/.*"extended":\([0-9]*\).*/\1/'; }
held_of() { # held_of <case> <client id>: the money held for the case's call, nothing if none
  "${COMPOSE[@]}" exec -T redis redis-cli --raw hgetall "held:$2" | awk -v p="$RUN-$1-" 'NR % 2 == 1 { k = $0 } NR % 2 == 0 && index(k, p) == 1 { print $0 }'
}
upto() { # upto <seconds> <shell condition>: waits until it holds; false if it never does
  local i
  for i in $(seq 1 "$1"); do eval "$2" && return 0; sleep 1; done
  return 1
}
settle
BALANCES=$(sql -F ' ' -c "select (select balance from clients where id = $LAB_CLIENT), (select balance from vendors where id = $VENDOR_S)")
EXTENDED=$(extended); CUT=$(cuts)
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup; "${COMPOSE[@]}" unpause rater >/dev/null 2>&1' EXIT
conn reservation $RUN-rslong $RUN-rsshort > "$TMP/reservation.out" &
RESERVATION=$!
upto 20 '[ -n "$(held_of rsshort $RS_CLIENT)" ]'
check reserve "the short call holds its first step" "$(held_of rsshort $RS_CLIENT)" 60000
sql -c "update clients set balance = 90000 where id = $RS_CLIENT"; reload
upto 100 '[ $(( $(extended) - EXTENDED )) -ge 1 ]'
check reserve "the first step is extended" $? 0
sleep 2
check reserve "held after the first step: the long call two steps, the short one all its client has" \
  "$(held_of rslong $LAB_CLIENT) $(held_of rsshort $RS_CLIENT)" "40000 90000"
upto 80 '[ $(( $(extended) - EXTENDED )) -ge 2 ]'
check reserve "the second step is extended" $? 0
check reserve "held after the second step: three steps" "$(held_of rslong $LAB_CLIENT)" 60000
"${COMPOSE[@]}" pause rater >/dev/null 2>&1
wait $RESERVATION
check reserve "the long call lasts; the short one is ended by the switch on both sides" "$(cat "$TMP/reservation.out")" "long=0 short=0 vendors=0 0"
upto 5 '[ "$(held_of rslong $LAB_CLIENT)" != 60000 ]'
HELD_END=$(held_of rslong $LAB_CLIENT)
"${COMPOSE[@]}" unpause rater >/dev/null 2>&1
trap '[ -f "$CERTS/opensips.crt.keep" ] && mv "$CERTS/opensips.crt.keep" "$CERTS/opensips.crt"; trace_cleanup' EXIT
for c in rslong rsshort; do
  client=$LAB_CLIENT; [ $c = rsshort ] && client=$RS_CLIENT
  upto 10 '[ "$(left_of $c $client)" = 0 ]'
  check reserve "$c: nothing left in the state store" "$(left_of $c $client)" 0
done
check reserve "one record per call" "$(legs rslong) / $(legs rsshort)" "1:172.30.0.88:200 / 1:172.30.0.89:200"
between reserve "seconds the long call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-rslong-%'")" 129 132
between reserve "seconds the short call lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-rsshort-%'")" 88 90
check reserve "two steps extended in all, one call ended by the engine" "$(( $(extended) - EXTENDED )) $(( $(cuts) - CUT ))" "2 1"
check reserve "when the long call ended, what was held fell to its charge" "$HELD_END" "$(sql -c "select sell_amount from cdrs where call_id like '$RUN-rslong-%'")"
check reserve "the short call is billed no more than was held" "$(sql -c "select sell_amount > 0 and sell_amount <= 90000 from cdrs where call_id like '$RUN-rsshort-%'")" t
check reserve "balances moved by exactly the charges" \
  "$(sql -F ' ' -c "select (select balance from clients where id = $LAB_CLIENT), (select balance from clients where id = $RS_CLIENT), (select balance from vendors where id = $VENDOR_S)")" \
  "$(sql -F ' ' -c "select ${BALANCES% *} - sum(sell_amount) filter (where call_id like '$RUN-rslong-%'), 90000 - sum(sell_amount) filter (where call_id like '$RUN-rsshort-%'), ${BALANCES#* } + sum(buy_amount) from cdrs where call_id like '$RUN-rslong-%' or call_id like '$RUN-rsshort-%'")"

# 7t. Offers made in an answer (T-43, ADR-0043; RFC 3261 13.2.1, 14.1;
#     RFC 3264 5-6). An INVITE without an offer: the vendor's offer in the
#     200 and the caller's answer in the ACK both go through the media
#     relay, each side sees only the relay's address, the connection's
#     codecs hold (8741 at .102 lets only PCMA through; the vendor offers
#     PCMU and PCMA), and audio crosses the relay both ways (the vendor
#     echoes it; the relay's own count, asked during the call). The same
#     with a vendor over TLS (.24): it offers encrypted media with a key,
#     the caller (plain UDP) is offered plain RTP without one, and the
#     vendor gets an encrypted answer with a key (it answers the BYE 488
#     otherwise). New INVITEs without an offer within a call, both ways
#     (8742 at .103): the offer in each 200 and the answer in each ACK go
#     through the relay, with the connection's codecs, then audio. A caller
#     whose ACK does not answer the offer (8743 at .104) has its call ended
#     by the switch with a BYE to both sides. The engine hears one answer
#     per call, no failure; one record each, charged for the time it
#     lasted, nothing left behind. (The relay refusing such an offer: 7r.)
relay_saw() { # relay_saw: each side's RTP in the relay's reply to "query": "address:audio|<packets>:profile:codec", sorted
  python3 -c "
import re, sys
t = sys.stdin.read()
out = []
for m in re.finditer(r'8:protocol\d+:([A-Z/]+)5:codec\d+:([^\d][^e]*?)7:streams.*?8:endpointd6:family4:IPv47:address\d+:([\d.]+)4:porti\d+e.*?5:statsd7:packetsi(\d+)e', t):
    out.append('%s:%s:%s:%s' % (m.group(3), 'audio' if int(m.group(4)) >= 100 else m.group(4), m.group(1), m.group(2)))
print(' '.join(sorted(out)))"
}
FAILED_EVENTS=$(event_count failed)
ANSWERED_EVENTS=$(event_count answered)
LATE=$(conn late $RUN-late 87415551112233 plain 172.30.0.102)
check late "INVITE without an offer: offer in the 200 and answer in the ACK through the relay, PCMA only" "$(echo "$LATE" | head -1)" "caller=0 vendors=0"
check late "its audio crossed the relay both ways, in PCMA" "$(echo "$LATE" | sed -n 2p | relay_saw)" \
  "172.30.0.102:audio:RTP/AVP:PCMA/8000 172.30.0.21:audio:RTP/AVP:PCMA/8000"
uac latetls uac-late.xml 975551112233 "s/__PLAN__/plain/; s|__RTP__|/work/out/media-short.pcap|; s/__WAIT__/1000/" -timeout 20
check late "a TLS vendor's encrypted offer: plain RTP to the caller, an encrypted answer with a key to the vendor" $? 0
RELATE=$(conn late $RUN-relate 87425551112233 relate 172.30.0.103 reoffer)
check late "new INVITEs without an offer within the call, both ways: through the relay, PCMA only" "$(echo "$RELATE" | head -1)" "caller=0 vendors=0"
check late "the audio after them crossed the relay both ways" "$(echo "$RELATE" | sed -n 2p | relay_saw)" \
  "172.30.0.103:audio:RTP/AVP:PCMA/8000 172.30.0.21:audio:RTP/AVP:PCMA/8000"
check late "an ACK that does not answer the offer: the switch ends the call on both sides" \
  "$(conn late $RUN-lateack 87435551112233 noanswer 172.30.0.104 take bare)" "caller=0 vendors=0"
settle
check late "the engine heard of the answers only: no failure, one answer per call" \
  "$(( $(event_count failed) - FAILED_EVENTS )) $(( $(event_count answered) - ANSWERED_EVENTS ))" "0 4"
check late "one record per call" "$(legs late) / $(legs latetls) / $(legs relate) / $(legs lateack)" \
  "1:172.30.0.102:200 / 1:172.30.0.24:200 / 1:172.30.0.103:200 / 1:172.30.0.104:200"
between late "seconds the call whose ACK did not answer lasted" "$(sql -c "select duration_sec from cdrs where call_id like '$RUN-lateack-%'")" 1 2
check late "charged for the time talked" "$(sql -c "select bool_and(sell_amount > 0) from cdrs where call_id like '$RUN-late-%' or call_id like '$RUN-latetls-%' or call_id like '$RUN-relate-%' or call_id like '$RUN-lateack-%'")" t
for c in late latetls relate lateack; do
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ "$(left_of $c)" = 0 ] && break; sleep 1; done
  check late "$c: nothing left in the state store" "$(left_of $c)" 0
done

# 7u. IPv6 (T-28; RFC 3261 19.1.1, RFC 5118, RFC 6157). The SIP core, the
#     media relay, the engine and the caller are on the lab's IPv6 network
#     (fd30::/64) as well; vendor V6 answers at fd30::20 (8601…), vendor C
#     at 172.30.0.20 (8602…). test/sipp/v6lab.sh plays the IPv6 sides. Each
#     call plays 100 packets the far end sends back; the relay is asked how
#     many it got from each side (a cookie of its own per question), and
#     the caller counts what came back to it.
python3 - "$ROOT/test/load/out/v6-rtp.pcap" <<'PCAP'
import struct, sys
# 100 packets of G.711 silence, 20 ms apart, as a capture file SIPp can play.
out = open(sys.argv[1], 'wb')
out.write(struct.pack('<IHHiIII', 0xa1b2c3d4, 2, 4, 0, 0, 65535, 1))
for i in range(100):
    rtp = struct.pack('!BBHII', 0x80, 8, i + 1, 160 * (i + 1), 0x1234abcd) + b'\xd5' * 160
    udp = struct.pack('!HHHH', 6000, 6000, 8 + len(rtp), 0) + rtp
    ip = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), i, 0, 64, 17, 0, bytes([10, 0, 0, 6]), bytes([10, 0, 0, 1])) + udp
    frame = b'\x02\x00\x00\x00\x00\x02' + b'\x02\x00\x00\x00\x00\x01' + b'\x08\x00' + ip
    out.write(struct.pack('<IIII', i // 50, (i % 50) * 20000, len(frame), len(frame)) + frame)
PCAP
v6() { "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac /work/test/sipp/v6lab.sh "$@" 2>/dev/null; }
# flows: the relay's streams as "<family> <address> audio" when 90 packets
# or more came from that side, then what reached the caller and SIPp's result.
flows() {
  awk '$1 ~ /^IPv/ { print $1, $2, ($3 >= 90 ? "audio" : $3) }
       $1 ~ /^back=/ { n = substr($1, 6) + 0; print "back", (n >= 90 ? "audio" : n), $2 }' | LC_ALL=C sort | paste -sd ' ' -
}
PROBE6='{"v":1,"host":"fd30::20","port":5060,"transport":"udp"}'
check ipv6 "the engine asks an IPv6 vendor whether it is there" \
  "$(engine -X POST http://127.0.0.1:58090/v1/probe -d "$PROBE6" | grep -o '"outcome":"[a-z_]*","sip_code":[0-9]*')" \
  '"outcome":"answered","sip_code":200'
check ipv6 "IPv6 caller to IPv6 vendor: audio both ways through the relay's IPv6 address" \
  "$(v6 call '[fd30::10]:5060' fd30::21 8601555111 $RUN-v6v6 audio | flows)" "IPv6 fd30::20 audio IPv6 fd30::21 audio back audio caller=0"
check ipv6 "IPv6 caller to IPv4 vendor: the relay bridges the families" \
  "$(v6 call '[fd30::10]:5060' fd30::21 8602555111 $RUN-v6v4 audio | flows)" "IPv4 172.30.0.20 audio IPv6 fd30::21 audio back audio caller=0"
check ipv6 "IPv4 caller to IPv6 vendor: the relay bridges the families" \
  "$(v6 call 172.30.0.10:5060 172.30.0.21 8601555111 $RUN-v4v6 audio | flows)" "IPv4 172.30.0.21 audio IPv6 fd30::20 audio back audio caller=0"
#     An offer made in the 200 (no offer in the INVITE) from the IPv6 vendor
#     reaches the IPv4 caller with the relay's IPv4 address, and the
#     caller's answer reaches the vendor with the IPv6 one.
check ipv6 "offer in the answer across the families" "$(v6 late $RUN-v6late 8604555111)" "offer=c=IN IP4 172.30.0.11 caller=0 vendor=0"
#     Networks. fd30::8:0/112 is a client's, fd30::8:80/121 inside it the
#     lab client's: the narrower network wins; an address of neither is a
#     stranger, asked to sign in, and its probe gets no answer.
v6 from fd30::8:5 8601555111 $RUN-v6net
check ipv6 "address in an IPv6 network: call answered" $? 0
v6 from fd30::8:85 8601555111 $RUN-v6narrow
check ipv6 "address in a narrower IPv6 network of another trunk: call answered" $? 0
v6 stranger fd30::9:1 $RUN-v6out
check ipv6 "IPv6 address of no trunk: asked to sign in" $? 0
v6 probe fd30::8:5 answer
check ipv6 "probe from inside the network is answered" $? 0
v6 probe fd30::9:1 silence
check ipv6 "stranger's probe gets no answer" $? 0
#     Registration over IPv6 (8603… is delivered to "lab-v6" wherever it is
#     registered): the IPv4 caller reaches the PBX at the address in
#     brackets it named, the PBX is offered the relay's IPv6 address, and
#     the audio goes both ways.
engine -X POST http://127.0.0.1:58090/v1/unregister -d "{\"v\":1,\"trunk_id\":$(trunk_id lab-v6)}" >/dev/null
TOV6=$(v6 to_trunk $RUN-v6trunk 8603555111)
check ipv6 "registered over IPv6: the call reaches the PBX at its contact, through the relay" \
  "$(echo "$TOV6" | grep '^uri=')" "uri=sip:lab-v6@[fd30::72]:5077 media=c=IN IP6 fd30::11 pbx=0"
check ipv6 "registered over IPv6: audio both ways" "$(echo "$TOV6" | grep -v '^uri=' | flows)" \
  "IPv4 172.30.0.21 audio IPv6 fd30::72 audio back audio caller=0"
check ipv6 "the engine keeps the registration with its IPv6 address" \
  "$(bound lab-v6 | grep -c '"contact":"sip:lab-v6@\[fd30::72\]:5077","src_ip":"fd30::72","src_port":5077,"transport":"udp","nat":false')" 1
engine -X POST http://127.0.0.1:58090/v1/unregister -d "{\"v\":1,\"trunk_id\":$(trunk_id lab-v6)}" >/dev/null
settle
check ipv6 "recorded: client, source in its short form, vendor" "$(sql -c "select string_agg(split_part(c.call_id, '-', 2) || ':' || cl.name || ':' || c.src_ip || ':' || coalesce(vc.host, 'trunk') || ':' || c.sip_code, ',' order by c.call_id)
  from cdrs c join clients cl on cl.id = c.client_id left join vendor_connections vc on vc.id = c.connection_id where c.call_id like '$RUN-v6%' or c.call_id like '$RUN-v4v6-%'")" \
  "v4v6:Lab SIPp:172.30.0.21:fd30::20:200,v6late:Lab SIPp:172.30.0.21:fd30::64:200,v6narrow:Lab SIPp:fd30::8:85:fd30::20:200,v6net:Lab SIPp network:fd30::8:5:fd30::20:200,v6trunk:Lab SIPp:172.30.0.21:trunk:200,v6v4:Lab SIPp:fd30::21:172.30.0.20:200,v6v6:Lab SIPp:fd30::21:fd30::20:200"

# 8. A change notification makes the engine load the configuration at once,
#    without waiting for its interval (three rounds, so the interval reload
#    cannot pass for it by chance). The engine gives the time of its last
#    load in whole seconds, and a load in the same second as the one before
#    does not show (T-29): a first notification restarts the interval
#    timer (RELOAD_SEC, 5 s in the lab), so no interval reload comes during
#    the rounds; each round waits until the last load is over a second old
#    and then gives the engine three seconds to load (the machine is
#    shared, a load can take a while).
loaded() { engine http://127.0.0.1:58090/v1/stats | sed 's/.*"snapshot_loaded_at":"\([^"]*\)".*/\1/'; }
epoch() { python3 -c 'import sys, datetime; print(datetime.datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00")).timestamp())' "$1"; }
notify() { sql -c "select pg_notify('softswitch_config', '')" >/dev/null; }
PROMPT=0
notify; sleep 1
for _ in 1 2 3; do
  BEFORE=$(loaded)
  python3 -c 'import sys, time; time.sleep(max(0, float(sys.argv[1]) + 1.2 - time.time()))' "$(epoch "$BEFORE")"
  notify
  for _ in $(seq 1 15); do sleep 0.2; [ "$(loaded)" != "$BEFORE" ] && { PROMPT=$((PROMPT + 1)); break; }; done
done
check notify "reloads on notification" "$PROMPT" 3

# 9. Every record of this run has reached ClickHouse with the same amounts.
ch() { "${COMPOSE[@]}" exec -T clickhouse clickhouse-client --user softswitch --password lab-only -d softswitch -q "$1"; }
sleep 3
check export "records and amounts in ClickHouse" \
  "$(ch "select count(), sum(sell_amount), sum(buy_amount) from cdrs final where call_id like '$RUN-%' format TSV" | tr '\t' ' ')" \
  "$(sql -F ' ' -c "select count(*), sum(sell_amount), sum(buy_amount) from cdrs where call_id like '$RUN-%'")"
check export "queue drained" "$(sql -c "select count(*) from cdr_export_queue")" 0

# Monitoring. Every part of the call path answers Prometheus, the numbers
# moved with this run's calls, and the calls' signalling was copied out.
metric() { # metric <url> <line prefix>: the value on the first line that starts with it
  curl -fsS "$1" | awk -v p="$2" 'index($0, p) == 1 { print $NF; exit }'
}
in_lab() { "${COMPOSE[@]}" exec -T hep-sink sh -c "$1"; }
check metrics "engine counts allowed calls" "$(( $(metric http://127.0.0.1:58090/metrics 'softswitch_engine_authorize_total{result="allowed"}') > ALLOWED_BEFORE ))" 1
check metrics "engine times its decisions" "$(( $(metric http://127.0.0.1:58090/metrics softswitch_engine_decision_seconds_count) > 0 ))" 1
check metrics "engine reports calls in progress" "$(metric http://127.0.0.1:58090/metrics softswitch_engine_calls_active)" 0
check metrics "rater counts stored records" "$(( $(metric http://127.0.0.1:58092/metrics 'softswitch_rater_records_total{result="stored"}') > STORED_BEFORE ))" 1
check metrics "rater's queue is empty" "$(metric http://127.0.0.1:58092/metrics softswitch_rater_queue_pending)" 0
check metrics "SIP core answers Prometheus" "$(curl -fsS http://127.0.0.1:58888/metrics | grep -c '^opensips_' | some)" yes
check metrics "media relay answers Prometheus" "$(curl -fsS http://127.0.0.1:52225/metrics | grep -c '^rtpengine_' | some)" yes
check trace "a collector of the operator's own still gets its copy" "$(in_lab "grep -ac '$RUN-failover' /tmp/hep.bin" | some)" yes

# 10. Call traces. The signalling of this run's calls was stored by the
#     trace collector; the API (started above) gives a call's messages in
#     order and the same as a capture file.
call_id() { sql -c "select call_id from cdrs where call_id like '$RUN-$1-%' order by leg limit 1"; }
# A case of several calls (limit:503): the one recorded with that code.
case_id() { case $1 in *:*) sql -c "select call_id from cdrs where call_id like '$RUN-${1%%:*}-%' and sip_code = ${1#*:} limit 1" ;; *) call_id "$1" ;; esac; }
trace() { # trace <key> <case> [path]: the API's answer for the case's call
  curl -sS -G -H "Authorization: Bearer $RUN-$1" --data-urlencode "call_id=$(case_id "$2")" "$TRACE_API/v1/trace${3:-}"
}
# look <case> <python expression over m, the list of messages>
look() { trace root "$1" | python3 -c "
import json, re, sys
m = json.load(sys.stdin)['messages']
sent = lambda method: [x['dst_ip'] for x in m if x['direction'] == 'out' and x['line'].startswith(method + ' ')]
got = lambda: [x['line'].split()[1] for x in m if x['direction'] == 'in' and x['line'].startswith('SIP/2.0') and ' INVITE' in re.search(r'(?im)^CSeq:.*$', x['raw']).group(0)]
flow = lambda: ' '.join(x['direction'] + ':' + (x['line'].split()[1] if x['line'].startswith('SIP/2.0') else x['line'].split()[0]) + ':' + re.search(r'(?im)^CSeq:\\s*(\\d+)', x['raw']).group(1) for x in m)
print($2)" 2>&1; }
sleep 2 # the collector writes once a second
#     Failover: one call, every attempt in it, in the order they were made.
check trace "the call's attempts, in order" "$(look failover "' '.join(sent('INVITE'))")" "172.30.0.22 172.30.0.23 172.30.0.20"
check trace "each vendor's answer" "$(look failover "' '.join(x for x in got() if x >= '200')")" "500 503 200"
check trace "from the caller's INVITE to the last answer, in time order" "$(look failover "m[0]['line'].startswith('INVITE') and m[0]['direction'] == 'in' and m[-1]['line'] == 'SIP/2.0 200 OK' and [x['at'] for x in m] == sorted(x['at'] for x in m)")" True
check trace "both sides hang up" "$(look failover "len(sent('BYE'))")" 1
#     TLS on both sides: every message is there in clear text.
check trace "a call over TLS is stored readable" "$(look secure "len(m) >= 12 and all(x['transport'] == 'tls' for x in m) and m[0]['raw'].startswith('INVITE sip:975551112233@') and 'm=audio' in m[0]['raw']")" True
#     A trunk that signs in: the answer to the challenge is not kept.
check trace "credentials are stored without the digest" "$(look digest200 "[re.search(r'response=\"([^\"]*)\"', x['raw']).group(1) for x in m if 'Authorization: Digest' in x['raw']]")" "['***']"
check trace "no digest in what is stored" "$(ch "select countIf(match(raw, 'response=\"[0-9a-f]{32}\"')), countIf(position(raw, 'Authorization:') > 0) > 0 from sip_messages where call_id like '$RUN-digest%' format TSV" | tr '\t' ' ')" "0 1"
#     A known client's call the engine refused (ADR-0046): the INVITE and
#     the refusal, whatever identified the client (its address, its
#     password) and whatever the reason (the tariff, a limit, the balance, a
#     caller rule, a caller who gave up while the engine decided).
check trace "refused: outside the tariff" "$(look blocked "flow()")" "in:INVITE:1 out:403:1 in:ACK:1"
check trace "refused: the client's call limit" "$(look limit:503 "flow()")" "in:INVITE:1 out:503:1 in:ACK:1"
check trace "refused: no balance left in the bundle or the account" "$(look bundleout "flow()")" "in:INVITE:1 out:402:1 in:ACK:1"
check trace "refused: a caller rule" "$(look cliblack "flow()")" "in:INVITE:1 out:403:1 in:ACK:1"
check trace "refused: given up while the engine decided" "$(look cxwait "flow()")" "in:INVITE:1 out:487:1 in:ACK:1"
#     A trunk that signs in is traced from the INVITE that carries its
#     credentials: the first INVITE and its challenge came before anybody
#     knew whose they were. The digest is masked there too.
check trace "refused after signing in: from the signed INVITE on" "$(look digest403 "flow()")" "in:INVITE:2 out:403:2 in:ACK:2"
check trace "refused after signing in: the digest is masked" "$(look digest403 "[re.search(r'response=\"([^\"]*)\"', x['raw']).group(1) for x in m if 'Authorization: Digest' in x['raw']]")" "['***']"
#     Nobody's calls are traced: strangers asked to sign in, a wrong
#     password, a source the engine did not recognise, addresses the
#     firewall shuts out, a flood.
check trace "not traced: strangers, wrong passwords, unknown sources, blocked addresses" \
  "$(ch "select count() from sip_messages where match(call_id, '^$RUN-(unknown|outside|digest401|notinbound|deniedinbound|walled|walled2)-') format TSV")" 0
check trace "not traced: the flood" "$(ch "select count() from sip_messages where (src_ip = '172.30.0.98' or dst_ip = '172.30.0.98') and at > now() - interval 2 hour format TSV")" 0
#     Another account's key gets nothing, nor does no key at all.
other_refused() { curl -sS -G -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $RUN-$1" --data-urlencode "call_id=$(call_id blocked)" "$TRACE_API/v1/trace"; }
check trace "a refused call's trace: its owner's account gets it, another is refused" "$(other_refused root) $(other_refused other)" "200 404"
status() { trace "$@" -o /dev/null -w '%{http_code}'; }
other() { curl -sS -G -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $RUN-other" --data-urlencode "call_id=$(call_id failover)" "$TRACE_API/v1/trace$1"; }
check trace "another account is refused the trace" "$(other "")" 404
check trace "and the capture file" "$(other /pcap)" 404
check trace "no key, no trace" "$(curl -sS -G -o /dev/null -w '%{http_code}' --data-urlencode "call_id=$(call_id failover)" "$TRACE_API/v1/trace")" 401
#     The capture file: tcpdump reads one frame per message, and the SIP in them.
trace root failover /pcap > "$TMP/failover.pcap"
trace root secure /pcap > "$TMP/secure.pcap"
if command -v tcpdump >/dev/null; then
  check trace "capture file: a frame per message" "$(tcpdump -nn -r "$TMP/failover.pcap" 2>/dev/null | wc -l | tr -d ' ')" "$(look failover "len(m)")"
  check trace "capture file: the attempts are in it" "$(tcpdump -nn -r "$TMP/failover.pcap" 2>/dev/null | grep -c 'SIP: INVITE')" 4
  check trace "capture file: TLS messages as readable TCP" "$(tcpdump -nn -A -r "$TMP/secure.pcap" 2>/dev/null | grep -c 'INVITE sip:975551112233@' | some)" yes
else
  check trace "capture file: a pcap of Ethernet frames (tcpdump is not installed)" "$(od -An -tx1 -N4 "$TMP/failover.pcap" | tr -d ' ')" d4c3b2a1
fi
#     The collector kept everything it was sent.
check trace "collector: stored what it received during the run, dropped nothing" "$(echo "$TRACE_BEFORE $(trace_counts)" | awk '{ r = $4 - $1; s = $5 - $2; d = $6 - $3; print (r > 0 && r == s && d == 0) ? "yes" : "no: received " r ", stored " s ", dropped " d }')" yes
trace_cleanup
trap - EXIT

# No worker of the SIP core may have died along the way. A worker that
# frees memory twice aborts only seconds after the call that caused it, so
# this is checked at the very end.
check core "workers that died during the run" "$(docker logs --since "$STARTED" softswitch-lab-opensips-1 2>&1 | grep -cE 'segfault|exited by a signal')" 0

# Nothing may be left in the state store once every call is over.
LEFT=$("${COMPOSE[@]}" exec -T redis redis-cli --scan --pattern 'call:*' | wc -l | tr -d ' ')
check state "calls left in Redis" "$LEFT" 0

[ $FAILED -eq 0 ] && echo "RESULT: PASS" || { echo "RESULT: FAIL"; exit 1; }
