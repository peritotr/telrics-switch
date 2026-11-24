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

# ---- releases (ADR-0051) ----------------------------------------------------
# The repository releases are fetched from: SOFTSWITCH_REPO from the
# environment (a first install has no .env yet), then from .env, then the
# public repository. A mirror or a fork goes there.
DEFAULT_REPO=https://github.com/peritotr/telrics-switch.git
repo_url() {
  local u=${SOFTSWITCH_REPO:-}
  [ -z "$u" ] && [ -f "$ENV_FILE" ] && u=$(get_env SOFTSWITCH_REPO)
  echo "${u:-$DEFAULT_REPO}"
}
# The checkout's top directory; empty when this is not a git checkout (an
# installation copied without .git: see "Sürümler" in docs/09-kurulum.md).
checkout_top() { git -C "$HERE" rev-parse --show-toplevel 2>/dev/null || true; }
# What the images are built as: the release tag when one is checked out,
# otherwise the commit; "-dirty" when tracked files were changed.
source_version() {
  local v=""
  [ -n "$(checkout_top)" ] && v=$(git -C "$HERE" describe --tags --always --dirty 2>/dev/null || true)
  echo "${v:-dev}"
}
# Computed here, not taken from the environment: after upgrade.sh checks out
# another release and runs that release's script, the value must be the new
# one. The compose files pass it to the image builds.
SOFTSWITCH_VERSION=$(source_version)
export SOFTSWITCH_VERSION

# checkout_release <tag>: fetches the tag from the release repository and
# checks it out (detached HEAD). Refuses a name that is not a release tag, a
# tag the repository does not have, a local tag that differs from the
# repository's (git does not overwrite it), and changes to tracked files
# (the checkout would lose them or stop half way). .env, certs/, backups/ and
# monitoring/alertmanager.yml are not tracked and stay as they are.
checkout_release() {
  local tag=$1 top url commit
  printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$' \
    || die "$tag is not a release tag (vMAJOR.MINOR.PATCH)"
  command -v git >/dev/null || die "git is not installed"
  top=$(checkout_top)
  [ -n "$top" ] || [ ! -e "$HERE/../../.git" ] || die "git does not accept $(cd "$HERE/../.." && pwd) as a checkout; git -C $HERE status says why (often: owned by another user, see safe.directory)"
  [ -n "$top" ] || die "$(cd "$HERE/../.." && pwd) is not a git checkout; see \"Sürümler\" in docs/09-kurulum.md for the one-time move to one"
  [ -z "$(git -C "$top" status --porcelain --untracked-files=no)" ] \
    || die "tracked files were changed in $top (git -C $top status); undo or move them first"
  url=$(repo_url)
  echo "fetching $tag from $url"
  git -C "$top" fetch --quiet --no-tags "$url" "refs/tags/$tag:refs/tags/$tag" \
    || die "could not fetch $tag from $url (no such tag, no network, or a local tag of that name that differs)"
  commit=$(git -C "$top" rev-parse --verify --quiet "refs/tags/$tag^{commit}") || die "$tag is not a commit"
  git -C "$top" -c advice.detachedHead=false checkout --quiet --detach "$commit" || die "could not check out $tag"
  echo "checked out $tag ($(git -C "$top" rev-parse --short HEAD))"
}
