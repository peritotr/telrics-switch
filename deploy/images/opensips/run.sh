#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Writes the configuration from the template and the environment, then
# starts OpenSIPS in the foreground. See sip/opensips/opensips.cfg for the
# variables; the ones with defaults are set here.
set -eu
: "${SIP_PORT:=5060}" "${SIP_TLS_PORT:=5061}" "${SIP_REALM:=softswitch}" "${UDP_WORKERS:=16}" "${TCP_WORKERS:=8}"
: "${TLS_CERT:=/etc/opensips-tls/opensips.crt}" "${TLS_KEY:=/etc/opensips-tls/opensips.key}"
# Behind one-to-one address translation (most clouds) the server listens on
# its private address and names the public one in what it sends.
SIP_UDP_SOCKET="udp:${SIP_IP:?SIP_IP is not set}:$SIP_PORT"
SIP_TLS_SOCKET="tls:$SIP_IP:$SIP_TLS_PORT"
if [ -n "${SIP_ADVERTISED_IP:-}" ]; then
  SIP_UDP_SOCKET="$SIP_UDP_SOCKET as $SIP_ADVERTISED_IP:$SIP_PORT"
  SIP_TLS_SOCKET="$SIP_TLS_SOCKET as $SIP_ADVERTISED_IP:$SIP_TLS_PORT"
fi
# The longest value the SIP core reads from its tables (db_text's
# buffer_size, T-50): the TLS table holds whole files (certificate chain,
# key, lists of authorities). 512 KiB takes a 4096-bit RSA certificate with
# three intermediates and its key (under 12 KiB together) many times over,
# and even the whole system bundle of authorities (~220 KiB) as TLS_CA_LIST;
# it is taken from a process's own memory (OPENSIPS_PKG_MB) only while a
# table is read. Not a setting: tls-table.sh refuses larger files.
DB_TEXT_BUFFER=524288
export SIP_REALM UDP_WORKERS TCP_WORKERS TLS_CERT TLS_KEY SIP_UDP_SOCKET SIP_TLS_SOCKET DB_TEXT_BUFFER
mkdir -p /run/opensips
# IPv6 (T-28): with SIP_IP6 the server listens on that address too, with
# the same ports (and names SIP_ADVERTISED_IP6 in what it sends, when set).
# Without it, nothing: an installation that has no IPv6 stays as it was.
# Written here, not in the template, because a socket line cannot be empty.
: > /run/opensips/sockets-v6.cfg
if [ -n "${SIP_IP6:-}" ]; then
  for value in "$SIP_IP6" "${SIP_ADVERTISED_IP6:-}"; do
    case $value in
      *[!0-9A-Fa-f:.]*) echo "opensips-run: SIP_IP6 and SIP_ADVERTISED_IP6 are IPv6 addresses without brackets" >&2; exit 1 ;;
    esac
  done
  udp6="udp:[$SIP_IP6]:$SIP_PORT"
  tls6="tls:[$SIP_IP6]:$SIP_TLS_PORT"
  if [ -n "${SIP_ADVERTISED_IP6:-}" ]; then
    udp6="$udp6 as [$SIP_ADVERTISED_IP6]:$SIP_PORT"
    tls6="$tls6 as [$SIP_ADVERTISED_IP6]:$SIP_TLS_PORT"
  fi
  printf 'socket=%s\nsocket=%s\n' "$udp6" "$tls6" > /run/opensips/sockets-v6.cfg
fi
: "${SIP_METRICS:=127.0.0.1:8888}"
SIP_METRICS_IP=${SIP_METRICS%:*}
SIP_METRICS_PORT=${SIP_METRICS##*:}
export SIP_METRICS_IP SIP_METRICS_PORT

# Tracing: a copy of every call's signalling goes to our own collector
# (TRACE_TARGET, which stores it for the panel), to a collector of the
# operator's (HEP_TARGET, a Homer say), to both or to neither.
: > /run/opensips/trace-modules.cfg
: > /run/opensips/trace-call.cfg
: > /run/opensips/trace-refused.cfg
if [ -n "${TRACE_TARGET:-}${HEP_TARGET:-}" ]; then
  # proto_hep sends from a socket of its own, which also listens (T-51).
  # What arrives there is never acted on: proto_hep hands a received
  # packet's SIP to the script only when a module asked for HEP (sipcapture,
  # which must never be loaded here), and would then take the sender's
  # address from the packet itself. It still parses what it gets, so the
  # socket is kept off the public address: on 127.0.0.1 when every
  # collector is on this host (production's own collector), otherwise on
  # SIP_IP (a private network in the lab; with an outside HEP_TARGET, set
  # HEP_IP to an address of this server facing that collector, or keep the
  # port shut in the firewall).
  if [ -z "${HEP_IP:-}" ]; then
    HEP_IP=127.0.0.1
    for target in ${TRACE_TARGET:-} ${HEP_TARGET:-}; do
      case $target in 127.*) ;; *) HEP_IP=$SIP_IP ;; esac
    done
  fi
  cat > /run/opensips/trace-modules.cfg <<EOF
socket=hep_udp:$HEP_IP:${HEP_PORT:-9061}
loadmodule "proto_hep.so"
loadmodule "tracer.so"
modparam("tracer", "trace_on", 1)
EOF
  hep_to() { # hep_to <name> <address:port>
    [ -n "$2" ] || return 0
    cat >> /run/opensips/trace-modules.cfg <<EOF
modparam("proto_hep", "hep_id", "[$1]$2;transport=udp;version=3")
modparam("tracer", "trace_id", "[calls]uri=hep:$1")
EOF
  }
  hep_to store "${TRACE_TARGET:-}"
  hep_to collector "${HEP_TARGET:-}"
  echo 'trace("calls", "d", "sip");' > /run/opensips/trace-call.cfg
  # A refused call has no dialog: its transaction is traced (ADR-0046).
  echo 'trace("calls", "t", "sip");' > /run/opensips/trace-refused.cfg
fi
# The secret the engine expects with every question (ENGINE_TOKEN), as the
# line of script that adds it to a request; nothing without one. Kept out of
# the template so that the value is never passed through a substitution.
case ${ENGINE_TOKEN:-} in
  *[!A-Za-z0-9._~+/=-]*) echo "opensips-run: ENGINE_TOKEN may only contain letters, digits and - . _ ~ + / =" >&2; exit 1 ;;
esac
( umask 077; : > /run/opensips/engine-token.cfg; : > /run/opensips/engine-auth )
if [ -n "${ENGINE_TOKEN:-}" ]; then
  printf 'rest_append_hf("Authorization: Bearer %s");\n' "$ENGINE_TOKEN" > /run/opensips/engine-token.cfg
  # The same header for registrants.sh's questions (curl -H @file: the
  # secret stays off command lines).
  printf 'Authorization: Bearer %s\n' "$ENGINE_TOKEN" > /run/opensips/engine-auth
else
  echo "opensips-run: WARNING: ENGINE_TOKEN is not set: questions to the engine carry no secret" >&2
fi
# TLS settings live in a table the core re-reads on demand; a watcher
# rewrites it and asks for a reload when the certificate files change.
opensips-tls-table write
opensips-tls-table watch &
# Registrations with vendors (ADR-0035): the engine says which, a watcher
# keeps the module's table in step. The module looks at its registrations
# every REG_TIMER_SEC (at least 10) and renews each that many seconds
# before it runs out; one that failed is tried again after REG_RETRY_SEC.
: "${REG_TIMER_SEC:=30}" "${REG_RETRY_SEC:=60}"
export SIP_PORT SIP_TLS_PORT REG_TIMER_SEC REG_RETRY_SEC
opensips-registrants write
opensips-registrants watch &
render-config /etc/opensips/opensips.cfg /run/opensips/opensips.cfg
exec /usr/local/sbin/opensips -F -f /run/opensips/opensips.cfg -m "${OPENSIPS_SHM_MB:-512}" -M "${OPENSIPS_PKG_MB:-32}"
