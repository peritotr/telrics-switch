#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Writes a backup of the database to backups/ and removes those older than
# KEEP_DAYS (14). PostgreSQL holds everything that cannot be made again:
# configuration, balances and call records. The copy of the call records in
# ClickHouse is rebuilt from it by restore.sh. Settings (.env) and
# certificates (certs/) are not in the file: keep a copy of them elsewhere.
#
#   ./backup.sh            run by hand, or from cron every night
. "$(dirname "$0")/lib.sh"
need_env
DIR=${BACKUP_DIR:-$HERE/backups}
mkdir -p "$DIR"; chmod 700 "$DIR"
FILE="$DIR/softswitch-$(date -u +%Y%m%dT%H%M%SZ).dump"
# Custom format: compressed, and restorable table by table.
dc exec -T postgres pg_dump -U softswitch -d softswitch -Fc > "$FILE.part"
# A dump that cannot be listed is not a backup.
dc exec -T postgres pg_restore -l < "$FILE.part" > /dev/null || die "the dump is not readable; kept as $FILE.part"
mv "$FILE.part" "$FILE"
chmod 600 "$FILE"
find "$DIR" -name 'softswitch-*.dump' -mtime +"${KEEP_DAYS:-14}" -delete
echo "$FILE"
