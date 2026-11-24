#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Runs a SIPp load test through the lab and prints a pass/fail report.
# Usage: run.sh <cps> <concurrent-calls> <ramp+hold seconds>
# Call hold time is CC/CPS seconds so the steady state sits at CC calls.
# With TRANSCODE=1 every call has its audio translated (T-09): the caller
# speaks PCMU only (trunk 888) and plays G.711 audio for the whole call,
# the vendor (8739, C's address) takes PCMA only and echoes it, so the
# media relay translates both ways. Keep CC under the engine's
# TRANSCODE_MAX (200), or the calls past it find no route.
set -euo pipefail

CPS=${1:?cps}
CC=${2:?concurrent calls}
DURATION=${3:-60}

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
COMPOSE=(docker compose -f "$ROOT/deploy/compose/docker-compose.yml")
OUT="$ROOT/test/load/out"
mkdir -p "$OUT"
STAT="load-${CPS}cps-${CC}cc.csv"
rm -f "$OUT/$STAT" "$OUT/errors.log"

# Calls an interrupted run left in the engine, and the media relay's
# sessions of earlier runs (a call the relay still has keeps four of its
# 10,000 ports until it ends or its media times out), would take room this
# run needs (T-29).
"$ROOT/test/lab-clean.sh" || { echo "RESULT: FAIL (the lab is not clean)"; exit 1; }
RELAY=http://127.0.0.1:52225/metrics
relay_sessions() { curl -fsS -m 3 "$RELAY" 2>/dev/null | awk '$1 == "rtpengine_sessions{type=\"own\"}" { print $2 }'; }
for _ in $(seq 1 90); do [ "$(relay_sessions)" = 0 ] && break; sleep 1; done
RELAY_LEFT=$(relay_sessions || true)
RELAY_STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)

HOLD_MS=$(( CC * 1000 / CPS ))
TOTAL=$(( CPS * DURATION ))
RUN="r$(date +%s)"

# The trace collector's counters, to see afterwards what it did with the
# signalling of this run. Nothing when it is not running.
TRACE_METRICS=${TRACE_METRICS:-http://127.0.0.1:58093/metrics}
trace_count() { curl -fsS -m 3 "$TRACE_METRICS" 2>/dev/null | awk -v p="$1" 'index($1, p) == 1 { n += $2 } END { printf "%d", n }'; }
T_RECEIVED=$(trace_count softswitch_trace_received_total); T_STORED=$(trace_count softswitch_trace_stored_total); T_DROPPED=$(trace_count softswitch_trace_dropped_total)

SCENARIO=/work/test/sipp/uac.xml
NUMBER=905551112233
if [ "${TRANSCODE:-0}" = 1 ]; then
  # Audio for the whole call: µ-law silence, 20 ms apart.
  python3 - "$OUT/load-pcmu.pcap" $(( HOLD_MS / 20 + 50 )) <<'PCAP'
import struct, sys
out = open(sys.argv[1], 'wb')
out.write(struct.pack('<IHHiIII', 0xa1b2c3d4, 2, 4, 0, 0, 65535, 1))
for i in range(int(sys.argv[2])):
    rtp = struct.pack('!BBHII', 0x80, 0, (i + 1) & 0xffff, 160 * (i + 1), 0x0f0f0f0f) + b'\xff' * 160
    udp = struct.pack('!HHHH', 6000, 6000, 8 + len(rtp), 0) + rtp
    ip = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), i & 0xffff, 0, 64, 17, 0, bytes([10, 0, 0, 6]), bytes([10, 0, 0, 1])) + udp
    frame = b'\x02\x00\x00\x00\x00\x02' + b'\x02\x00\x00\x00\x00\x01' + b'\x08\x00' + ip
    out.write(struct.pack('<IIII', i // 50, (i % 50) * 20000, len(frame), len(frame)) + frame)
PCAP
  perl -pe 's/RTP\/AVP 8$/RTP\/AVP 0/; s/a=rtpmap:8 PCMA\/8000/a=rtpmap:0 PCMU\/8000/;
      s|^  <pause />|  <nop><action><exec play_pcap_audio="/work/out/load-pcmu.pcap" /></action></nop>\n  <pause />|' \
      "$ROOT/test/sipp/uac.xml" > "$OUT/uac-load-transcode.xml"
  SCENARIO=/work/out/uac-load-transcode.xml
  NUMBER=88887395551112233
fi
XCODED() { curl -fsS -m 3 http://127.0.0.1:52225/metrics 2>/dev/null | awk '/^rtpengine_transcode_packets_total/ { n += $NF } END { printf "%d", n }'; }
X_BEFORE=$(XCODED)

# The media relay's processor use and the machine's load while SIPp runs
# (the Mac is shared: other projects' containers count in the load).
LOADAVG_BEFORE=$( (sysctl -n vm.loadavg 2>/dev/null || cut -d' ' -f1-3 /proc/loadavg) | tr -d '{}' | xargs)
( while :; do docker stats --no-stream --format '{{.CPUPerc}}' softswitch-lab-rtpengine-1 2>/dev/null | tr -d '%'; sleep 1; done ) > "$OUT/rtpengine-cpu.txt" &
CPU_PID=$!
# The relay's ports in use (IPv4 address), once a second.
( while :; do curl -fsS -m 2 "$RELAY" 2>/dev/null | awk 'index($0, "rtpengine_ports_used{name=\"default\",address=\"172.30.0.11\"}") == 1 { print $NF }'; sleep 1; done ) > "$OUT/rtpengine-ports.txt" &
PORTS_PID=$!

set +e
"${COMPOSE[@]}" run --rm -T sipp-uac \
  -sf "$SCENARIO" 172.30.0.10:5060 -i 172.30.0.21 -s "$NUMBER" \
  -cid_str "$RUN-%u-%p@%s" \
  -r "$CPS" -l "$CC" -d "$HOLD_MS" -m "$TOTAL" \
  -trace_stat -stf "/work/out/$STAT" -fd 1 \
  -trace_err -error_file /work/out/errors.log \
  -nostdin >/dev/null 2>"$OUT/sipp-stderr.log"
RC=$?
set -e
kill $CPU_PID $PORTS_PID 2>/dev/null; wait $CPU_PID $PORTS_PID 2>/dev/null || true
LOADAVG_AFTER=$( (sysctl -n vm.loadavg 2>/dev/null || cut -d' ' -f1-3 /proc/loadavg) | tr -d '{}' | xargs)
CPU_LINE=$(awk '$1 + 0 > 0 { n++; s += $1; if ($1 > m) m = $1 } END { if (n) printf "mean %.0f%%, peak %.0f%% of one core (%d samples)", s / n, m, n; else print "not measured" }' "$OUT/rtpengine-cpu.txt")
X_PACKETS=$(( $(XCODED) - X_BEFORE ))
PORTS_LINE=$(awk -v left="${RELAY_LEFT:-?}" '$1 + 0 > m { m = $1 } END { printf "peak %d of 10000 in use, %s sessions of earlier runs at the start", m, left }' "$OUT/rtpengine-ports.txt")
NO_PORTS=$(docker logs --since "$RELAY_STARTED" softswitch-lab-rtpengine-1 2>&1 | grep -c "Ran out of ports" || true)

# Calls the engine still has once every call is over: an end it was never
# told (its event timed out). Up to 30 s for the last ones to report.
for _ in $(seq 1 30); do
  LEFT=$(docker exec softswitch-lab-redis-1 redis-cli -n 0 --scan --pattern "call:$RUN-*" | wc -l | tr -d ' ')
  [ "$LEFT" = 0 ] && break
  sleep 1
done

[ -s "$OUT/$STAT" ] || { echo "FAIL: SIPp produced no statistics (exit $RC)"; cat "$OUT/sipp-stderr.log"; exit 1; }

# Decision time as route-engine measured it, since it started.
. "$ROOT/deploy/compose/lab.env" # the lab's engine token
STATS=$(curl -fsS -H "Authorization: Bearer $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats || echo '{}')
P99=$(printf '%s' "$STATS" | sed -n 's/.*"p99_us":\([0-9]*\).*/\1/p')
P50=$(printf '%s' "$STATS" | sed -n 's/.*"p50_us":\([0-9]*\).*/\1/p')

# What the trace collector made of it: packets it got, messages it wrote,
# and how many of the calls have their signalling stored from the caller's
# INVITE to the answer to the BYE on both sides. (A call has twelve messages
# here, or eleven when the vendor's 180 arrived with its 200 and was not
# passed on.) Reported, not judged: traces are not on the call path.
if curl -fsS -m 3 -o /dev/null "$TRACE_METRICS" 2>/dev/null; then
  sleep 5 # the last batches
  TRACED=$("${COMPOSE[@]}" exec -T clickhouse clickhouse-client --user softswitch --password lab-only -d softswitch -q \
    "select countIf(n >= 11 and opened and closed), count(), sum(n), sum(bytes) from (select call_id, count() as n, sum(length(raw)) as bytes, max(direction = 'in' and startsWith(raw, 'INVITE ')) as opened, countIf(startsWith(raw, 'SIP/2.0 200') and match(raw, 'CSeq: *[0-9]+ BYE')) >= 2 as closed from sip_messages where startsWith(call_id, '$RUN-') group by call_id) format TSV" 2>/dev/null || true)
  LATE180=$("${COMPOSE[@]}" exec -T clickhouse clickhouse-client --user softswitch --password lab-only -d softswitch -q \
    "select countIf(p > f), countIf(p > 0 and p < f) from (select maxIf(toUnixTimestamp64Micro(at), direction = 'out' and dst_ip = '172.30.0.21' and startsWith(raw, 'SIP/2.0 18')) as p, minIf(toUnixTimestamp64Micro(at), direction = 'out' and dst_ip = '172.30.0.21' and startsWith(raw, 'SIP/2.0 200') and match(raw, 'CSeq: *1 INVITE')) as f from sip_messages where startsWith(call_id, '$RUN-') group by call_id having f > 0) format TSV" 2>/dev/null || true)
  TRACE_LINE=$(printf 'received %d, stored %d, dropped %d; complete traces %s of %d calls, %s bytes of SIP per call' \
    $(( $(trace_count softswitch_trace_received_total) - T_RECEIVED )) $(( $(trace_count softswitch_trace_stored_total) - T_STORED )) \
    $(( $(trace_count softswitch_trace_dropped_total) - T_DROPPED )) \
    "$(echo "$TRACED" | cut -f1)" "$TOTAL" "$(echo "$TRACED" | awk -F'\t' '{ print ($2 > 0) ? int($4 / $2) : 0 }')")
else
  TRACE_LINE="not running"
fi
# Provisional replies the caller was sent after the 200 (the SIP core
# decides under the transaction's lock whether a reply is passed on, and
# passes no provisional reply after the final one, RFC 3261 16.7 step 5;
# but a 180 decided just before the 200 can leave just after it) and before
# it. The caller ignores the former (uac.xml matches replies to the
# INVITE's transaction).
LATE_LINE=$(echo "${LATE180:-}" | awk -F'\t' 'NF == 2 { printf "%d after the 200, %d before it", $1, $2; next } { print "not traced" }')

awk -F';' -v left="$LEFT" -v ports="$PORTS_LINE" -v noports="$NO_PORTS" -v late="$LATE_LINE" -v trace="$TRACE_LINE" -v cpu="$CPU_LINE" -v xpk="$X_PACKETS" -v la0="$LOADAVG_BEFORE" -v la1="$LOADAVG_AFTER" -v p50="${P50:-0}" -v p99="${P99:-0}" -v cps="$CPS" -v cc="$CC" -v total="$TOTAL" -v rc="$RC" '
NR==1 { for (i=1;i<=NF;i++) col[$i]=i; next }
{
  if ($col["CurrentCall"]+0 > peak) peak=$col["CurrentCall"]+0
  ok=$col["SuccessfulCall(C)"]; failed=$col["FailedCall(C)"]
  retrans=$col["Retransmissions(C)"]; elapsed=$col["ElapsedTime(C)"]
}
END {
  printf "target            : %d CPS, %d concurrent, %d calls\n", cps, cc, total
  printf "elapsed           : %s\n", elapsed
  printf "peak concurrent   : %d\n", peak
  printf "successful calls  : %d\n", ok
  printf "failed calls      : %d\n", failed
  printf "retransmissions   : %d\n", retrans
  printf "decision time     : p50 %.1f ms, p99 %.1f ms (budget 10 ms)\n", p50/1000, p99/1000
  printf "trace collector   : %s\n", trace
  printf "media relay CPU   : %s\n", cpu
  printf "media relay ports : %s; \"Ran out of ports\" %d times\n", ports, noports
  printf "180/183 to caller : %s\n", late
  printf "calls left (engine): %d\n", left
  printf "packets translated: %d\n", xpk
  printf "load average      : %s before, %s after\n", la0, la1
  pass = (failed==0 && ok==total && rc==0 && p99>0 && p99<10000 && left==0 && noports==0)
  print (pass ? "RESULT: PASS" : "RESULT: FAIL (sipp exit " rc ")")
  exit !pass
}' "$OUT/$STAT"
