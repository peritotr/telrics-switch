#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Tries the installation end to end on a private Docker network, next to
# whatever else runs on this machine: install, a real call, a crashed SIP
# worker, backup and restore, upgrade. Everything it creates belongs to the
# project "softswitch-smoke" and is removed at the end (KEEP=1 leaves it up).
#
#   make prod-smoke
export ENV_FILE="$(cd "$(dirname "$0")" && pwd)/.env.smoke"
. "$(dirname "$0")/lib.sh"
ROOT=$(cd "$HERE/../.." && pwd)
NET=softswitch-smoke_default
FAILED=0
set +e # a failed step is reported, not fatal
check() { if [ "$2" == "$3" ]; then echo "PASS  $1"; else echo "FAIL  $1: got '$2', want '$3'"; FAILED=1; fi; }
cleanup() {
  docker rm -f softswitch-smoke-uas >/dev/null 2>&1 || true
  [ "${KEEP:-0}" = 1 ] && return
  dc down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f "$ENV_FILE"; rm -rf "$HERE/backups-smoke" "$HERE/certs-smoke"
}
trap cleanup EXIT
cleanup

# A settings file of its own, away from the real one and from the ports the
# lab and a development API use.
( umask 077; cp env.example "$ENV_FILE" )
sed -i.bak 's/^API_PORT=.*/API_PORT=58180/' "$ENV_FILE" && rm -f "$ENV_FILE.bak"
echo "COMPOSE_PROJECT_NAME=softswitch-smoke" >> "$ENV_FILE"
printf 'PROMETHEUS_PORT=59190\nALERTMANAGER_PORT=59193\n' >> "$ENV_FILE"
# Certificates of its own, so a real installation's are never touched, and
# short intervals so the hand-over of a new certificate is seen in seconds.
printf 'CERTS_DIR=./certs-smoke\nSIP_CERT_CHECK_SEC=3\nTLS_WATCH_SEC=3\n' >> "$ENV_FILE"
sed -i.bak 's/^COMPOSE_PROFILES=.*/COMPOSE_PROFILES=https/' "$ENV_FILE" && rm -f "$ENV_FILE.bak"
export BACKUP_DIR="$HERE/backups-smoke"

./install.sh --smoke --monitoring --sip-domain sip.localhost >/dev/null 2>&1 || die "install.sh failed; run it by hand with ENV_FILE=$ENV_FILE to see why"
check "install: API answers" "$(curl -fsS -o /dev/null -w '%{http_code}' http://127.0.0.1:58180/healthz)" 200
check "install: panel is served" "$(curl -fsS http://127.0.0.1:58180/ | grep -c '<div id="root"')" 1
KEY=$(sed -n 's/^BOOTSTRAP_API_KEY=//p' "$ENV_FILE")
check "install: first API key works" "$(curl -fsS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $KEY" http://127.0.0.1:58180/v1/clients)" 200
# The release the checkout is (lib.sh) is what the services were built as.
check "version: the API reports the checkout's" "$(curl -fsS -H "Authorization: Bearer $KEY" http://127.0.0.1:58180/v1/session | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])')" "$SOFTSWITCH_VERSION"
check "version: in the engine's metrics" "$(dc exec -T caddy wget -q -O - http://172.30.8.40:58090/metrics 2>/dev/null | grep -c "^softswitch_build_info{version=\"$SOFTSWITCH_VERSION\"")" 1
# The audit log: a change made through the API is recorded with who made it.
# The installer made the key that seals two-step sign-in secrets.
api_json() { curl -fsS -o /dev/null -w '%{http_code}' -X "$1" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' ${3:+-d "$3"} "http://127.0.0.1:58180$2"; }
check "audit: a change is accepted" "$(api_json POST /v1/exchange-rates '{"currency":"XTS","rate":1000000}')" 201
check "audit: and recorded with who made it" "$(curl -fsS -H "Authorization: Bearer $KEY" 'http://127.0.0.1:58180/v1/audit-log?resource=exchange-rates' | python3 -c 'import json,sys; r=json.load(sys.stdin); print(len(r), r[0]["action"], r[0]["actor_type"], r[0]["actor_name"], r[0]["label"], r[0]["changes"]["rate"])')" "1 create key bootstrap XTS {'new': 1000000}"
check "audit: the log cannot be written to" "$(api_json DELETE /v1/audit-log/1)" 405
check "audit: the change is undone" "$(api_json DELETE "/v1/exchange-rates/$(curl -fsS -H "Authorization: Bearer $KEY" 'http://127.0.0.1:58180/v1/exchange-rates?currency=XTS' | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])')")" 204
secret_key() { sed -n 's/^SECRET_KEY=//p' "$ENV_FILE" | tail -1; }
check "secret key: made by the installer" "$(secret_key | grep -cE '^[0-9a-f]{64}$')" 1
check "secret key: the API has it" "$(dc logs --no-color api 2>&1 | grep -c 'SECRET_KEY is not set')" 0
# The tools' system page: inside a Linux container the machine's own figures
# can be read, and every part of the switch answers.
SYSTEM=$(curl -fsS -H "Authorization: Bearer $KEY" http://127.0.0.1:58180/v1/system)
check "system: the machine's figures are read" "$(echo "$SYSTEM" | python3 -c 'import json,sys; m=json.load(sys.stdin)["machine"]; print(m["supported"] and m["cores"] > 0 and m["uptime_sec"] > 0 and len(m["disks"]) > 0)')" True
check "system: seven services, all answering" "$(echo "$SYSTEM" | python3 -c 'import json,sys; s=json.load(sys.stdin)["services"]; print(len(s), sum(1 for x in s if x["up"]))')" "7 7"
check "install: API reaches the engine" "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $KEY" http://127.0.0.1:58180/v1/live)" 200
# The engine's interface is closed with a secret the installer made: the API
# and the SIP core have it, anyone else on the network is refused, and it is
# not given out by the system page.
engine_token() { sed -n 's/^ENGINE_TOKEN=//p' "$ENV_FILE" | tail -1; }
engine_says() { dc exec -T caddy wget -q -S -O /dev/null "$@" http://172.30.8.40:58090/v1/stats 2>&1 | sed -n 's/^ *HTTP[^ ]* \([0-9]*\).*/\1/p' | head -1; }
check "engine token: made by the installer" "$(engine_token | grep -cE '^[0-9a-f]{48}$')" 1
check "engine token: without it the engine refuses" "$(engine_says)" 401
check "engine token: a wrong one is refused" "$(engine_says --header "Authorization: Bearer $(engine_token | tr 0-9a-f 1-9a-f0)")" 401
check "engine token: the right one is answered" "$(engine_says --header "Authorization: Bearer $(engine_token)")" 200
check "engine token: not in the system page" "$(echo "$SYSTEM" | grep -cF "$(engine_token)")" 0

# The panel behind the HTTPS front; for "localhost" it signs its own certificate.
sleep 3
check "install: panel over HTTPS" "$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:58443/healthz)" 200

# The SIP core started with a self-signed certificate; the one the HTTPS
# front obtains for the SIP name (signed by its own authority here, as the
# name is local) must replace it without a restart, chain included.
sip_cert() { dc exec -T opensips sh -c "timeout 5 openssl s_client -connect 172.30.8.10:5061 -showcerts </dev/null 2>/dev/null"; }
issued() { sip_cert | openssl x509 -noout -issuer 2>/dev/null | grep -q 'Caddy Local Authority'; }
wait_for "the SIP certificate" 60 issued
check "certificate: the SIP core serves the issued one" "$(sip_cert | openssl x509 -noout -subject -ext subjectAltName 2>/dev/null | grep -c 'sip.localhost')" 1
check "certificate: with its chain" "$(sip_cert | grep -c 'BEGIN CERTIFICATE')" 2
check "certificate: loaded without a restart" "$(docker inspect -f '{{.RestartCount}}' softswitch-smoke-opensips-1)" 0

# The lab's customer, vendor and routes, moved to this network's addresses.
sed 's/172\.30\.0\./172.30.8./g' "$ROOT/test/seed/lab-routing.sql" | psql_c -f - >/dev/null
psql_c -c "select pg_notify('softswitch_config', '')" >/dev/null
docker build -q -t softswitch-smoke-sipp "$ROOT/deploy/compose/images/sipp" >/dev/null
docker run -dt --name softswitch-smoke-uas --network $NET --ip 172.30.8.20 -v "$ROOT/test:/work/test:ro" \
  softswitch-smoke-sipp -sf /work/test/sipp/uas.xml -i 172.30.8.20 -p 5060 >/dev/null
sleep 3

call() { # call <name>
  docker run --rm --network $NET --ip 172.30.8.21 -v "$ROOT/test:/work/test:ro" softswitch-smoke-sipp \
    -sf /work/test/sipp/uac.xml 172.30.8.10:5060 -i 172.30.8.21 -s 905551112233 -cid_str "smoke-$1-%u@%s" -d 1000 -m 1 -nostdin >/dev/null 2>&1
}
answered() { psql_c -c "select count(*) from cdrs where call_id like 'smoke-%' and sip_code = 200"; }
ch_count() {
  dc exec -T clickhouse clickhouse-client --user softswitch --password "$(sed -n 's/^CLICKHOUSE_PASSWORD=//p' "$ENV_FILE")" \
    -d softswitch -q "select count() from cdrs final where call_id like 'smoke-%'"
}

call first; check "call: answered" $? 0
sleep 4
check "call: recorded" "$(answered)" 1
check "call: copied to ClickHouse" "$(ch_count)" 1
# The call's signalling was stored on the way, and the API gives it back as
# messages and as a capture file.
TRACE=$(curl -fsS -G -H "Authorization: Bearer $KEY" --data-urlencode "call_id=$(psql_c -c "select call_id from cdrs where call_id like 'smoke-first-%'")" http://127.0.0.1:58180/v1/trace)
check "trace: the call's messages, both sides, from INVITE to the end" "$(echo "$TRACE" | python3 -c 'import json,sys; m=json.load(sys.stdin)["messages"]; print(len(m) >= 11, m[0]["line"].startswith("INVITE"), sorted({x["direction"] for x in m}), sum(x["line"].startswith("BYE") for x in m))')" "True True ['in', 'out'] 2"
check "trace: capture file" "$(curl -fsS -G -H "Authorization: Bearer $KEY" --data-urlencode "call_id=$(psql_c -c "select call_id from cdrs where call_id like 'smoke-first-%'")" http://127.0.0.1:58180/v1/trace/pcap | od -An -tx1 -N4 | tr -d ' ')" d4c3b2a1

# A worker of the SIP core dies: the container must come back by itself and
# take calls again.
sleep 20 # so Prometheus has seen the SIP core's counters before they start over
BEFORE=$(docker inspect -f '{{.RestartCount}}' softswitch-smoke-opensips-1)
dc exec -T opensips sh -c 'for s in /proc/[0-9]*/stat; do set -- $(cat $s); [ "$4" = 1 ] && { kill -9 "$1"; break; }; done'
sleep 5
wait_for "the SIP core" 120 sip_up
check "crash: the SIP core was restarted" "$(( $(docker inspect -f '{{.RestartCount}}' softswitch-smoke-opensips-1) > BEFORE ))" 1
call second; check "crash: calls go on afterwards" $? 0
sleep 4
check "crash: recorded" "$(answered)" 2

# Backup, one more call, restore: the database is back at the backup.
FILE=$(./backup.sh)
check "backup: file written" "$([ -s "$FILE" ] && echo yes)" yes
call third; sleep 4
check "backup: a later call exists before the restore" "$(answered)" 3
./restore.sh "$FILE" --yes >/dev/null
check "restore: records are those of the backup" "$(answered)" 2
sleep 8
check "restore: ClickHouse rebuilt from them" "$(ch_count)" 2
call fourth; check "restore: calls go on afterwards" $? 0

# Monitoring: every service is being read, the rules are loaded, and a
# stopped service raises an alert that reaches Alertmanager.
prom() { curl -fsS "http://127.0.0.1:59190/api/v1/$1"; }
sleep 20
check "monitoring: all six services are read" "$(curl -fsS -G http://127.0.0.1:59190/api/v1/query --data-urlencode 'query=count(up{job="softswitch"}==1)' | sed 's/.*"value":\[[^,]*,"\([0-9]*\)".*/\1/')" 6
check "monitoring: alert rules loaded" "$(( $(prom rules | grep -o '"type":"alerting"' | wc -l) >= 10 ))" 1
firing() { # firing <alert name>: waits up to two minutes for it
  for _ in $(seq 1 24); do
    curl -fsS -G http://127.0.0.1:59190/api/v1/query --data-urlencode "query=ALERTS{alertname=\"$1\",alertstate=\"firing\"}" \
      | grep -q '"alertname"' && { echo yes; return; }
    sleep 5
  done
  echo no
}
check "monitoring: the crash above raised an alert" "$(firing SIPCoreRestarted)" yes
check "monitoring: no service is reported down" "$(prom alerts | grep -c '"alertname":"ServiceDown"')" 0
dc stop rater >/dev/null 2>&1
FIRED=no
for _ in $(seq 1 40); do
  curl -fsS http://127.0.0.1:59193/api/v2/alerts | grep -q '"alertname":"ServiceDown"' && { FIRED=yes; break; }
  sleep 5
done
check "monitoring: a stopped service raises an alert" "$FIRED" yes
dc start rater >/dev/null 2>&1

SIP_BEFORE=$(docker inspect -f '{{.Id}}' softswitch-smoke-opensips-1)
./upgrade.sh >/dev/null 2>&1
check "upgrade: with nothing changed, the SIP core keeps running" "$([ "$(docker inspect -f '{{.Id}}' softswitch-smoke-opensips-1)" = "$SIP_BEFORE" ] && echo yes)" yes
check "upgrade: completes and the API answers" "$(curl -fsS -o /dev/null -w '%{http_code}' http://127.0.0.1:58180/healthz)" 200
call fifth; check "upgrade: calls go on afterwards" $? 0
# An installation from before the engine had a secret: the upgrade makes
# one, the services take it up and calls go on.
TOKEN_BEFORE=$(engine_token)
sed -i.bak '/^ENGINE_TOKEN=/d;/^SECRET_KEY=/d' "$ENV_FILE" && rm -f "$ENV_FILE.bak"
./upgrade.sh >/dev/null 2>&1
check "upgrade: an installation without a secret key gets one" "$(secret_key | grep -cE '^[0-9a-f]{64}$')" 1
check "upgrade: an installation without an engine token gets one" "$(engine_token | grep -cE '^[0-9a-f]{48}$') $([ "$(engine_token)" != "$TOKEN_BEFORE" ] && echo new)" "1 new"
check "upgrade: the engine wants the new token" "$(engine_says --header "Authorization: Bearer $TOKEN_BEFORE") $(engine_says --header "Authorization: Bearer $(engine_token)")" "401 200"
check "upgrade: API reaches the engine with it" "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $KEY" http://127.0.0.1:58180/v1/live)" 200
sleep 3
call sixth; check "upgrade: calls go on with the new token" $? 0
check "secret key: in no service's log" "$(dc logs --no-color 2>&1 | grep -cF "$(secret_key)")" 0
check "engine token: in no service's log" "$(dc logs --no-color 2>&1 | grep -cF -e "$(engine_token)" -e "$TOKEN_BEFORE")" 0
check "all services running" "$(dc ps --status running --services | wc -l | tr -d ' ')" 14

[ $FAILED -eq 0 ] && echo "RESULT: PASS" || { echo "RESULT: FAIL"; exit 1; }
