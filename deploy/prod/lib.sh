# SPDX-License-Identifier: AGPL-3.0-only
# Shared by the scripts in this directory.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$HERE"
ENV_FILE=${ENV_FILE:-$HERE/.env}
# Build records attached to an image change its id on every build, and a new
# id makes compose restart the service: without this, an upgrade would drop
# the calls in progress even when nothing in the SIP core changed.
export BUILDX_NO_DEFAULT_ATTESTATIONS=1

die() { echo "error: $*" >&2; exit 1; }
need_env() { [ -f "$ENV_FILE" ] || die "$ENV_FILE not found; run install.sh first"; }
dc() { docker compose --env-file "$ENV_FILE" "$@"; }
psql_c() { dc exec -T postgres psql -U softswitch -d softswitch -v ON_ERROR_STOP=1 -qtA "$@"; }

# set_env <name> <value>: sets the value in .env, adding the line if missing.
set_env() {
  if grep -q "^$1=" "$ENV_FILE"; then
    local tmp; tmp=$(mktemp)
    awk -v k="$1" -v v="$2" 'BEGIN { FS = OFS = "=" } $1 == k { print k, v; next } { print }' "$ENV_FILE" > "$tmp"
    cat "$tmp" > "$ENV_FILE"; rm -f "$tmp"
  else
    echo "$1=$2" >> "$ENV_FILE"
  fi
}
# add_profile <name>: switches an optional part on, keeping the others.
add_profile() {
  local have; have=$(get_env COMPOSE_PROFILES)
  case ",$have," in *",$1,"*) return ;; esac
  set_env COMPOSE_PROFILES "${have:+$have,}$1"
}
get_env() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -1; }

# wait_for <what> <seconds> <command...>
wait_for() {
  local what=$1 limit=$2 waited=0; shift; shift
  until "$@" >/dev/null 2>&1; do
    waited=$((waited + 2))
    [ "$waited" -ge "$limit" ] && die "$what did not come up in ${limit}s; see: docker compose logs"
    sleep 2
  done
}
api_port() { sed -n 's/^API_PORT=//p' "$ENV_FILE" | tail -1; }
# The management pipe appears once every module of the SIP core is up.
sip_up() { dc exec -T opensips test -p /run/opensips/opensips_fifo; }
api_up() { curl -fsS "http://127.0.0.1:$(api_port)/healthz"; }
