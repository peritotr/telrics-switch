#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# No call record is lost when the engine dies while NATS is down, and a call
# whose end the engine never hears of still gets one (ADR-0029, T-33).
#
# It stops NATS, kills the engine and the SIP core: run it alone
# (`make test-journal`), never next to `make test-routing` or a load test.
# It takes about six minutes, most of them spent waiting for the state of
# two calls to expire (their 5-second limit plus the engine's 300 seconds of
# setup slack). Whatever happens, NATS, the engine and the SIP core are
# started again on the way out.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
COMPOSE=(docker compose -f "$ROOT/deploy/compose/docker-compose.yml")
. "$ROOT/deploy/compose/lab.env"
RUN="j$(date +%s)"
FAILED=0

sql() { "${COMPOSE[@]}" exec -T postgres psql -U softswitch -d softswitch -v ON_ERROR_STOP=1 -qtA "$@"; }
redis() { "${COMPOSE[@]}" exec -T redis redis-cli "$@"; }
engine() { curl -fsS -m 5 -H "Authorization: Bearer $ENGINE_TOKEN" "$@"; }
metric() { curl -fsS -m 5 http://127.0.0.1:58090/metrics | awk -v n="$1" '$1 == n { print $2 }'; }
check() { # check <case> <what> <got> <want>
  if [ "$3" == "$4" ]; then echo "PASS  $1: $2"; else echo "FAIL  $1: $2: got '$3', want '$4'"; FAILED=1; fi
}
# uac <case> <scenario> <number> [extra sipp args...]: one call (or more,
# with -m) from the lab client.
uac() {
  local name=$1 scenario=$2 number=$3; shift 3
  "${COMPOSE[@]}" run --rm -T --entrypoint sh sipp-uac -c \
    "exec sipp -sf /work/test/sipp/$scenario 172.30.0.10:5060 -i 172.30.0.21 -s $number \
     -cid_str '$RUN-$name-%u@%s' -nostdin -timeout 30s -timeout_error $*" >/dev/null 2>&1
}
# until_ok <seconds> <command...>: waits for the command to succeed.
until_ok() {
  local n=$1; shift
  while ! "$@" >/dev/null 2>&1; do
    n=$((n - 1)); [ "$n" -le 0 ] && return 1
    sleep 1
  done
}
engine_up() { curl -fsS -m 2 http://127.0.0.1:58090/healthz; }
sip_up() { curl -fsS -m 2 http://127.0.0.1:58888/metrics; }
count() { sql -c "select count(*) from cdrs where call_id like '$RUN-$1-%'"; }
journal_empty() { [ "$(metric softswitch_engine_cdr_journal_records)" = 0 ]; }
stored() { [ "$(sql -c "select count(*) from cdrs where call_id like '$RUN-$1-%'")" = "$2" ]; }
found_on_disk() { [ "$(at_least "$(metric softswitch_engine_cdr_journal_records)" 3)" = yes ]; }
at_least() { awk -v n="$1" -v m="$2" 'BEGIN { print (n >= m) ? "yes" : "no" }'; }

restore() {
  "${COMPOSE[@]}" start nats route-engine opensips >/dev/null 2>&1
}
trap restore EXIT

sql -f - < "$ROOT/test/seed/lab-routing.sql"
sql -c "update clients set max_calls = 0 where name = 'Lab SIPp'"
engine -X POST http://127.0.0.1:58090/v1/reload
BALANCE=$(sql -c "select balance from clients where name = 'Lab SIPp'")

# 1. The SIP core dies during an answered call (a limit of 5 s: the trunk
#    with 885 in front) and comes back without it. The engine is told
#    nothing more about the call.
uac lost-sip uac-nobye.xml 885905551112233 -m 1
check lost-sip "answered" $? 0
"${COMPOSE[@]}" kill opensips >/dev/null 2>&1
docker start softswitch-lab-opensips-1 >/dev/null 2>&1
until_ok 60 sip_up
check lost-sip "SIP core back" $? 0
EXPIRE_AT=$(( $(date +%s) + 305 ))

# 2. NATS is down: calls go on and their records wait on the engine's disk.
"${COMPOSE[@]}" stop nats >/dev/null 2>&1
uac down uac.xml 905551112233 -d 300 -m 3 -r 10
check down "calls while NATS is down" $? 0
sleep 1
check down "records on the engine's disk" "$(metric softswitch_engine_cdr_journal_records)" 3
check down "none stored yet" "$(count down)" 0

# 3. An answered call ends (its 5 s limit) while the engine is gone: the end
#    the SIP core reports reaches nobody. The engine dies with the records
#    of step 2 not yet in NATS.
uac lost-engine uac-nobye.xml 885905551112233 -m 1
check lost-engine "answered" $? 0
"${COMPOSE[@]}" kill route-engine >/dev/null 2>&1
sleep 7
# Not `compose start`: it would start NATS too, which the engine depends on.
docker start softswitch-lab-route-engine-1 >/dev/null 2>&1
until_ok 60 engine_up
check restart "engine back while NATS is down" "$? $(docker inspect -f '{{.State.Running}}' softswitch-lab-nats-1)" "0 false"
# At least those of step 2: with them, those of the last minute before it,
# already in NATS (whole segments are kept until all in them are confirmed).
until_ok 10 found_on_disk
check restart "the records found on disk" "$(at_least "$(metric softswitch_engine_cdr_journal_records)" 3)" yes
EXPIRED=$(metric softswitch_engine_cdr_expired_total)

# 4. A call through the engine that came back, NATS still down.
uac after uac.xml 905551112233 -d 300 -m 1
check after "call after the restart" $? 0

# 5. NATS is back: every record arrives, once.
"${COMPOSE[@]}" start nats >/dev/null 2>&1
until_ok 60 journal_empty
check nats "the journal empties" $? 0
until_ok 60 stored down 3; until_ok 30 stored after 1
check nats "records stored" "$(count down) $(count after)" "3 1"
check nats "sent again after the restart" "$(at_least "$(metric softswitch_engine_cdr_journal_replayed_total)" 3)" yes
check nats "legs" "$(sql -c "select string_agg(distinct sip_code::text || ':' || (duration_sec > 0)::text, ' ') from cdrs where call_id like '$RUN-down-%' or call_id like '$RUN-after-%'")" "200:true"
check nats "journal errors" "$(curl -fsS http://127.0.0.1:58090/metrics | awk '$1 ~ /^softswitch_engine_cdr_journal_errors_total/ { n += $2 } END { print n + 0 }')" 0

# 6. Both lost calls get their record once their state expires: answered,
#    ending at the answer, nothing charged (ADR-0029).
echo "      waiting $(( EXPIRE_AT - $(date +%s) + 10 )) s for the state of the lost calls to expire"
sleep $(( EXPIRE_AT - $(date +%s) > 0 ? EXPIRE_AT - $(date +%s) : 0 ))
until_ok 60 stored lost-sip 1; until_ok 30 stored lost-engine 1
for c in lost-sip lost-engine; do
  check $c "record" "$(sql -c "select leg || ':' || sip_code || ':' || (answer_at = end_at) || ':' || duration_sec || ':' || sell_amount from cdrs where call_id like '$RUN-$c-%'")" "1:200:true:0:0"
  ID=$(sql -c "select call_id from cdrs where call_id like '$RUN-$c-%'")
  check $c "state gone" "$(redis exists "call:$ID") $(redis zscore expiry "$ID")" "0 "
done
check expired "counted" "$(( $(metric softswitch_engine_cdr_expired_total) - EXPIRED ))" 2
# What was charged in all is what the stored records say: none twice.
check money "balance" "$(sql -c "select $BALANCE - balance = (select coalesce(sum(sell_amount), 0) from cdrs where call_id like '$RUN-%') from clients where name = 'Lab SIPp'")" t

echo
[ "$FAILED" = 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"
exit "$FAILED"
