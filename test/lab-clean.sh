#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Clears what an interrupted run left behind in the lab, before a suite
# starts (make test-routing, make load). Calls whose end never reached the
# engine stay in its state store until their longest duration has passed
# (ADR-0029) and meanwhile count against the lab clients' limits and
# balances, fill the list of calls in progress and fail the suites' own
# "nothing left" checks.
#
# Only the engine's database of the lab Redis (0) is read; nothing is
# deleted there by hand. A call never answered is ended through the engine
# with the "ended" event (487). An answered one is hung up through the
# engine (POST /v1/hangup) if the SIP core still carries it; otherwise it
# is handed to the engine's own clean-up of calls whose end never came (the
# "expiry" set, scored to the past): recorded as answered with no talk time
# and charged nothing (ADR-0040). Either way the engine releases what was
# held for it.
#
# Callers are SIPp containers of the lab ("sipp-uac" runs). One that has
# run for more than 15 minutes is left over from an interrupted run (no
# scenario lasts that long) and is removed; a younger one means another
# suite is placing calls right now, and then nothing is touched and the
# script fails. Also removed: the media relay's port holder of an
# interrupted test-routing (section 7r), which otherwise keeps every port
# of the relay for up to a minute.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
. "$ROOT/deploy/compose/lab.env" # the lab's engine token
ENGINE=${ENGINE:-http://127.0.0.1:58090}
redis() { docker exec softswitch-lab-redis-1 redis-cli -n 0 "$@"; }

# Callers still running: "<id> <seconds running>".
callers() {
  docker ps -q --filter name=softswitch-lab-sipp-uac-run | while read -r id; do
    started=$(docker inspect -f '{{.State.StartedAt}}' "$id" 2>/dev/null) || continue
    echo "$id $(python3 -c 'import sys, datetime; t = datetime.datetime.fromisoformat(sys.argv[1][:26].rstrip("Z") + "+00:00"); print(int((datetime.datetime.now(datetime.timezone.utc) - t).total_seconds()))' "$started")"
  done
}
OLD=$(callers | awk '$2 > 900 { print $1 }')
if [ -n "$OLD" ]; then
  echo "lab-clean: removing $(echo "$OLD" | wc -l | tr -d ' ') caller container(s) left over from an interrupted run"
  docker rm -f $OLD >/dev/null
fi
if [ -n "$(callers)" ]; then
  echo "lab-clean: a caller is running (another suite?): not touching the lab" >&2
  exit 1
fi

if docker exec softswitch-lab-rtpengine-1 test -e /tmp/hold 2>/dev/null; then
  echo "lab-clean: releasing the media relay's ports held by an interrupted run"
  docker exec softswitch-lab-rtpengine-1 rm -f /tmp/hold
  sleep 2 # the holder looks once a second
fi

IDS=$(redis --scan --pattern 'call:*' | sed 's/^call://')
[ -z "$IDS" ] && exit 0
N=$(echo "$IDS" | wc -l | tr -d ' ')
post() { curl -fsS -m 5 -o /dev/null -H "Authorization: Bearer $ENGINE_TOKEN" -X POST "$ENGINE/v1/$1" -d "$2" 2>/dev/null; }
# An answered call the SIP core still carries is ended there (BYE to both
# sides; its end then reaches the engine as usual). One it no longer knows
# (it restarted, or the call ended but the engine was never told) goes to
# the engine's expiry, after a moment for the hang-ups to report back.
ANSWERED=""
for id in $IDS; do
  if [ -n "$(redis hget "call:$id" a)" ]; then
    post hangup "{\"v\":1,\"call_id\":\"$id\"}"
    ANSWERED="$ANSWERED $id"
  else
    post events "{\"v\":1,\"call_id\":\"$id\",\"event\":\"ended\",\"sip_code\":487}" ||
      echo "lab-clean: the engine did not take the end of $id" >&2
  fi
done
[ -n "$ANSWERED" ] && sleep 3
for id in $ANSWERED; do
  [ "$(redis exists "call:$id")" = 1 ] && redis zadd expiry 0 "$id" >/dev/null
done
ANSWERED=$(echo $ANSWERED | wc -w | tr -d ' ')
for _ in $(seq 1 30); do
  LEFT=$(redis --scan --pattern 'call:*' | wc -l | tr -d ' ')
  [ "$LEFT" = 0 ] && break
  sleep 1
done
echo "lab-clean: $N call(s) left by an earlier run ended through the engine ($ANSWERED answered: hung up, or recorded free if the SIP core had lost them; $((N - ANSWERED)) never answered: 487); $LEFT still there"
[ "$LEFT" = 0 ]
