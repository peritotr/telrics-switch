#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Installs the platform on this server, or brings an existing installation
# up again. Safe to run twice: settings and secrets already in .env are kept.
#
#   ./install.sh --ip <address> [--advertised-ip <public address>] [--panel-domain <name>] [--sip-domain <name>] [--monitoring]
#   ./install.sh --smoke        private Docker network, for trying it out
#
# Guide: docs/09-kurulum.md
. "$(dirname "$0")/lib.sh"

IP="" ADVERTISED="" DOMAIN="" SIP_DOMAIN_ARG="" SMOKE=0 MONITORING=0
while [ $# -gt 0 ]; do
  case $1 in
    --ip) IP=$2; shift 2 ;;
    --advertised-ip) ADVERTISED=$2; shift 2 ;;
    --panel-domain) DOMAIN=$2; shift 2 ;;
    --sip-domain) SIP_DOMAIN_ARG=$2; shift 2 ;;
    --monitoring) MONITORING=1; shift ;;
    --smoke) SMOKE=1; shift ;;
    *) die "unknown option $1" ;;
  esac
done

command -v docker >/dev/null || die "docker is not installed"
docker compose version >/dev/null 2>&1 || die "the docker compose plugin is not installed"
command -v openssl >/dev/null || die "openssl is not installed"

if [ ! -f "$ENV_FILE" ]; then
  if [ "$SMOKE" -eq 0 ]; then
    [ -n "$IP" ] || die "a first installation needs --ip <the address SIP is served on>"
    [ "$(uname -s)" = Linux ] || die "the production layout uses the host's network and needs Linux; use --smoke elsewhere"
  fi
  ( umask 077; cp env.example "$ENV_FILE" )
  echo "created $ENV_FILE"
fi
chmod 600 "$ENV_FILE"

# Secrets are made once and never shown; they stay in .env.
for name in PG_PASSWORD CLICKHOUSE_PASSWORD ENGINE_TOKEN; do
  [ -n "$(get_env $name)" ] || set_env "$name" "$(openssl rand -hex 24)"
done
[ -n "$(get_env BOOTSTRAP_API_KEY)" ] || set_env BOOTSTRAP_API_KEY "tsk_$(openssl rand -hex 24)"
# Seals the panel users' two-step sign-in secrets in the database (32 bytes).
[ -n "$(get_env SECRET_KEY)" ] || set_env SECRET_KEY "$(openssl rand -hex 32)"

if [ "$SMOKE" -eq 1 ]; then
  set_env COMPOSE_FILE docker-compose.yml:docker-compose.bridge.yml
else
  if [ -n "$IP" ]; then
    set_env SIP_IP "$IP"
    if [ -n "$ADVERTISED" ]; then
      set_env SIP_ADVERTISED_IP "$ADVERTISED"
      set_env RTP_INTERFACE "$IP!$ADVERTISED"
    else
      set_env RTP_INTERFACE "$IP"
    fi
  fi
  [ -n "$(get_env SIP_IP)" ] || die "SIP_IP is empty in $ENV_FILE; pass --ip"
  if [ -n "$DOMAIN" ]; then
    set_env PANEL_DOMAIN "$DOMAIN"
    set_env PANEL_URL "https://$DOMAIN"
    # The HTTPS front passes the caller's address on; the audit log records it.
    set_env TRUST_PROXY 1
    add_profile https
  fi
fi
# A certificate for the SIP name is obtained by the HTTPS front.
if [ -n "$SIP_DOMAIN_ARG" ]; then
  set_env SIP_DOMAIN "$SIP_DOMAIN_ARG"
  add_profile https
fi
[ "$MONITORING" -eq 1 ] && add_profile monitoring

# The SIP core will not start without a certificate. Until a real one is put
# in certs/ (opensips.crt, opensips.key), a self-signed one stands in.
CERTS=$(get_env CERTS_DIR); CERTS=${CERTS:-certs}
mkdir -p "$CERTS"
if [ ! -f "$CERTS/opensips.crt" ] || [ ! -f "$CERTS/opensips.key" ]; then
  NAME=$(get_env SIP_ADVERTISED_IP); [ -n "$NAME" ] || NAME=$(get_env SIP_IP); [ -n "$NAME" ] || NAME=softswitch.invalid
  openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=$NAME" \
    -keyout "$CERTS/opensips.key" -out "$CERTS/opensips.crt" 2>/dev/null
  chmod 644 "$CERTS/opensips.key" # read by the SIP core's container
  if [ -n "$(get_env SIP_DOMAIN)" ]; then
    echo "note: a self-signed SIP certificate stands in until the one for $(get_env SIP_DOMAIN) is issued"
  else
    echo "note: wrote a self-signed SIP certificate to $CERTS/; replace it (or pass --sip-domain) before offering TLS to customers"
  fi
fi

# Yours to edit; never overwritten.
[ -f monitoring/alertmanager.yml ] || cp monitoring/alertmanager.example.yml monitoring/alertmanager.yml

echo "building images (the first build compiles the SIP core and takes several minutes)"
dc build -q
dc up -d --remove-orphans
wait_for "the API" 180 api_up
wait_for "the SIP core" 180 sip_up
echo
echo "The platform is running."
echo "  Panel and API : http://127.0.0.1:$(api_port)  (local only)"
[ -n "$(get_env PANEL_DOMAIN)" ] && echo "  Panel (HTTPS) : https://$(get_env PANEL_DOMAIN)"
echo "  First API key : BOOTSTRAP_API_KEY in $ENV_FILE"
echo "  State         : docker compose ps   (in $HERE)"
