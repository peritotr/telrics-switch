#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Moves the installation to another release: takes a backup, builds the
# images and restarts what changed. The API applies new schema steps as it
# starts. Calls in progress are dropped when the SIP core's image changed.
#
#   ./upgrade.sh <tag>                    fetch the release (e.g. v0.2.0) and move to it
#   ./upgrade.sh                          move to what is checked out here already
#   ./upgrade.sh <tag> --restore <file>   back to an earlier release, with the
#                                         backup taken when it was left (rollback)
#
# Releases come from SOFTSWITCH_REPO in .env, the public repository when it is
# empty. Nothing is upgraded unless this script is run. Every move is noted in
# upgrades.log with the backup taken before it. Guide: docs/09-kurulum.md.
. "$(dirname "$0")/lib.sh"

main() {
  local tag="" restore=""
  while [ $# -gt 0 ]; do
    case $1 in
      --restore) [ $# -ge 2 ] || die "--restore needs a backup file"; restore=$2; shift 2 ;;
      -*) die "unknown option $1" ;;
      *) [ -z "$tag" ] || die "give one release tag"; tag=$1; shift ;;
    esac
  done
  need_env
  if [ -n "$restore" ]; then
    # Schema steps only go forward: an earlier release runs on the database
    # as it was when that release was left, never on a newer one.
    [ -n "$tag" ] || die "--restore goes with the release the backup was taken on: upgrade.sh <tag> --restore <file>"
    [ -f "$restore" ] || die "$restore not found"
    restore="$(cd "$(dirname "$restore")" && pwd)/$(basename "$restore")"
  fi

  if [ -n "$tag" ]; then
    local from=$SOFTSWITCH_VERSION backup
    backup=$(./backup.sh)
    echo "backup: $backup"
    checkout_release "$tag"
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $from -> $tag backup=$backup${restore:+ restore=$restore}" >> "$HERE/upgrades.log"
    # The rest is the release's own business: run its script, which may
    # build and start things differently from this one.
    UPGRADE_FROM=$from UPGRADE_BACKUP=$backup UPGRADE_RESTORE=$restore exec "$HERE/upgrade.sh"
  fi

  # The route-engine's interface is closed with a secret (ENGINE_TOKEN). An
  # installation from before that gets one here, made as install.sh makes it
  # and never shown; the engine, the SIP core and the API pick it up as they
  # are restarted below.
  if [ -z "$(get_env ENGINE_TOKEN)" ]; then
    command -v openssl >/dev/null || die "openssl is not installed"
    set_env ENGINE_TOKEN "$(openssl rand -hex 24)"
    echo "added ENGINE_TOKEN to $ENV_FILE: the route-engine now answers only its own services"
  fi
  # Two-step sign-in secrets and vendors' sign-in passwords are sealed with
  # SECRET_KEY. An installation from before that gets one here; secrets stored
  # before a key was set stay readable.
  if [ -z "$(get_env SECRET_KEY)" ]; then
    command -v openssl >/dev/null || die "openssl is not installed"
    set_env SECRET_KEY "$(openssl rand -hex 32)"
    echo "added SECRET_KEY to $ENV_FILE: two-step sign-in secrets are encrypted in the database from now on"
  fi
  if [ -n "${UPGRADE_BACKUP:-}" ]; then
    echo "backup: $UPGRADE_BACKUP (taken before the checkout)"
  else
    echo "backup: $(./backup.sh)"
  fi
  echo "building images ($SOFTSWITCH_VERSION)"
  dc build -q
  if [ -n "${UPGRADE_RESTORE:-}" ]; then
    # Back to an earlier release: the database goes back to the backup taken
    # on that release before its services start on it (restore.sh stops the
    # services, restores, and starts them with the images just built).
    ./restore.sh "$UPGRADE_RESTORE" --yes
  else
    # The API first: it brings the schema up to date before the others start.
    # The rater with it: it must know every field of a call record before the
    # engine starts sending it.
    dc up -d api rater
    wait_for "the API" 180 api_up
    dc up -d --remove-orphans
    wait_for "the API" 180 api_up
    wait_for "the SIP core" 180 sip_up
  fi
  echo "now at $SOFTSWITCH_VERSION${UPGRADE_FROM:+ (was $UPGRADE_FROM)}"
  if [ -n "${UPGRADE_FROM:-}" ] && [ -z "${UPGRADE_RESTORE:-}" ] && printf '%s' "$UPGRADE_FROM" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
    echo "to go back (whatever was written since is lost): ./upgrade.sh $UPGRADE_FROM --restore $UPGRADE_BACKUP"
  fi
  dc ps --format 'table {{.Service}}\t{{.Status}}'
}

# One line, read whole before it runs: checking out a release rewrites this
# file under the running shell.
main "$@"; exit $?
