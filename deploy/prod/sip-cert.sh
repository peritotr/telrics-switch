#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Keeps the SIP core's certificate equal to the one the HTTPS front (Caddy)
# obtains and renews for SIP_DOMAIN: whenever Caddy's copy differs from the
# one in /certs, it is copied over. The SIP core notices the new files and
# loads them without a restart. Without SIP_DOMAIN this does nothing and the
# files in certs/ are yours to manage.
set -eu
[ -n "${SIP_DOMAIN:-}" ] || { echo "sip-cert: SIP_DOMAIN is not set; nothing to do"; exec sleep infinity; }
while :; do
  for dir in /data/caddy/certificates/*/"$SIP_DOMAIN"; do
    crt=$dir/$SIP_DOMAIN.crt key=$dir/$SIP_DOMAIN.key
    [ -s "$crt" ] && [ -s "$key" ] || continue
    cmp -s "$crt" /certs/opensips.crt && cmp -s "$key" /certs/opensips.key && continue
    # Copies first, then two renames: the SIP core never reads a half-written
    # file, and a pair caught between the renames fails its check and is
    # read again a moment later.
    cp "$crt" /certs/opensips.crt.new && cp "$key" /certs/opensips.key.new
    chmod 644 /certs/opensips.crt.new /certs/opensips.key.new # read by the SIP core's container
    mv /certs/opensips.key.new /certs/opensips.key && mv /certs/opensips.crt.new /certs/opensips.crt
    echo "sip-cert: installed the certificate for $SIP_DOMAIN from $dir"
  done
  sleep "${SIP_CERT_CHECK_SEC:-300}"
done
