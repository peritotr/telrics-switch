#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Replaces the database with a backup. Everything written after the backup
# was taken is lost, and calls in progress are dropped.
#
#   ./restore.sh backups/softswitch-<time>.dump --yes
. "$(dirname "$0")/lib.sh"
need_env
FILE=${1:-}
[ -f "$FILE" ] || die "usage: restore.sh <backup file> --yes"
[ "${2:-}" = "--yes" ] || die "this replaces the whole database; add --yes to go on"

echo "stopping the services that use the database"
dc stop opensips route-engine rater api >/dev/null
dc up -d postgres redis nats clickhouse >/dev/null
wait_for "PostgreSQL" 120 dc exec -T postgres pg_isready -U softswitch -d softswitch

echo "restoring $FILE"
dc exec -T postgres psql -U softswitch -d postgres -v ON_ERROR_STOP=1 -q \
  -c "drop database if exists softswitch with (force)" -c "create database softswitch owner softswitch"
dc exec -T postgres pg_restore -U softswitch -d softswitch --no-owner --exit-on-error < "$FILE"

# Calls and reserved funds in Redis belonged to the old state.
dc exec -T redis redis-cli flushall >/dev/null
# The copy in ClickHouse is made again from the restored records: emptied,
# then every record is queued for the rater to send.
CH_PASSWORD=$(sed -n 's/^CLICKHOUSE_PASSWORD=//p' "$ENV_FILE" | tail -1)
dc exec -T clickhouse clickhouse-client --user softswitch --password "$CH_PASSWORD" -d softswitch \
  -q "truncate table if exists cdrs"
psql_c -c "insert into cdr_export_queue (cdr_id) select id from cdrs on conflict do nothing"

dc up -d >/dev/null
wait_for "the API" 180 api_up
wait_for "the SIP core" 180 sip_up
echo "restored; call records are being copied to ClickHouse in the background"
