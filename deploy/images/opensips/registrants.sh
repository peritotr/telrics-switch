#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Keeps the SIP core's registrations with vendors (ADR-0035) in the table
# the uac_registrant module reads. The engine says which registrations there
# are to be (GET /v1/vendor-registrations); this script writes them the way
# the module wants them, with this server's own address as the binding, and
# has the module reload the table when it has changed.
#   opensips-registrants write   ask once and write the table (before the start)
#   opensips-registrants watch   ask every REG_SYNC_SEC seconds; reload on change
# The table holds the sign-in passwords in the clear: the module reads them
# from there and nowhere else. It is readable by root only, and lives under
# /run/opensips, which the compose files put in memory (tmpfs). Nothing
# here prints a password: answers go from curl to jq to the file.
set -eu
DIR=/run/opensips/regdb
FIFO=/run/opensips/opensips_fifo
AUTH=/run/opensips/engine-auth
umask 077

: "${SIP_PORT:=5060}" "${SIP_TLS_PORT:=5061}"
# Where vendors reach this server: the public address when there is one.
host=${SIP_ADVERTISED_IP:-$SIP_IP}
case $host in *:*) host="[$host]" ;; esac
local=$SIP_IP
case $local in *:*) local="[$local]" ;; esac

header() {
  echo 'id(int,auto) registrar(string) proxy(string,null) aor(string) third_party_registrant(string,null) username(string,null) password(string,null) binding_URI(string) binding_params(string,null) expiry(int,null) forced_socket(string,null) cluster_shtag(string,null) state(int) '
}

# The table's layout version, which the module checks before reading it.
version_file() {
  [ -f "$DIR/version" ] || printf 'table_name(string) table_version(int) \nregistrant:3\n' > "$DIR/version"
}

# ask: the engine's answer in $DIR/answer, given the version held. Fails
# when the engine cannot be reached.
ask() {
  held=$(cat "$DIR/held" 2>/dev/null || true)
  set -- -fsS --max-time 5 -o "$DIR/answer"
  [ -s "$AUTH" ] && set -- "$@" -H "@$AUTH"
  curl "$@" "$ENGINE_URL/v1/vendor-registrations?version=$held"
}

# render: the table from the answer, into $DIR/registrant.new. A value as the
# table wants it: its separator and escape character escaped.
render() {
  header > "$DIR/registrant.new"
  jq -r --arg udp "sip:%s@$host:$SIP_PORT" --arg tls "sip:%s@$host:$SIP_TLS_PORT;transport=tls" \
    --arg sudp "udp:$local:$SIP_PORT" --arg stls "tls:$local:$SIP_TLS_PORT" '
    def cell: tostring | gsub("\\\\"; "\\\\") | gsub(":"; "\\:");
    .registrations[] | .username as $u |
    (if .transport == "tls" then [$tls, $stls] else [$udp, $sudp] end) as [$bind, $sock] |
    [ (.id | tostring), (.registrar | cell), "", (.aor | cell), "",
      ($u | cell), (.password | cell), ($bind | sub("%s"; $u) | cell), "",
      (.expires | tostring), ($sock | cell), "", "0" ] | join(":")' "$DIR/answer" >> "$DIR/registrant.new"
}

reload() {
  [ -p "$FIFO" ] || return 1
  rm -f /tmp/reg_reload && mkfifo /tmp/reg_reload
  printf ':reg_reload:{"jsonrpc":"2.0","method":"reg_reload","id":1}\n' > "$FIFO"
  timeout 10 cat /tmp/reg_reload | grep -q '"result"'
}

# sync <reload|noreload>: asks the engine and puts a changed list in place.
sync() {
  mkdir -p "$DIR"
  version_file
  [ -f "$DIR/registrant" ] || header > "$DIR/registrant"
  if ! ask; then
    rm -f "$DIR/answer"
    return 1
  fi
  if [ "$(jq -r '.unchanged // false' "$DIR/answer")" = true ]; then
    rm -f "$DIR/answer"
    return 0
  fi
  version=$(jq -r '.version' "$DIR/answer")
  count=$(jq -r '.registrations | length' "$DIR/answer")
  if ! render; then
    rm -f "$DIR/answer" "$DIR/registrant.new"
    echo "registrants: the engine's answer could not be read" >&2
    return 1
  fi
  rm -f "$DIR/answer"
  if cmp -s "$DIR/registrant.new" "$DIR/registrant"; then
    rm -f "$DIR/registrant.new"
  else
    mv "$DIR/registrant.new" "$DIR/registrant"
    if [ "$1" = reload ]; then
      reload || { echo "registrants: reload failed; tried again in ${REG_SYNC_SEC:-10} s" >&2; return 1; }
    fi
    echo "registrants: $count registration(s) with vendors" >&2
  fi
  printf '%s\n' "$version" > "$DIR/held"
}

case "${1:-}" in
write)
  # The engine may not be up yet: the table stays empty until it is.
  sync noreload || echo "registrants: the engine cannot be asked yet; no registrations for now" >&2 ;;
watch)
  while sleep "${REG_SYNC_SEC:-10}"; do
    sync reload || true
  done ;;
*) echo "usage: $0 write|watch" >&2; exit 2 ;;
esac
