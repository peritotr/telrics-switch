#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Writes the SIP core's TLS settings as a table it can read again while it
# runs, which is what lets a renewed certificate take effect without a
# restart (settings written in the configuration file are read only once).
#   opensips-tls-table write   check the certificate and write the table
#   opensips-tls-table watch   rewrite and reload whenever the files change
# Files: TLS_CERT, TLS_KEY (this server's), TLS_CA_LIST (authorities a vendor
# may be signed by; empty: those in the directory TLS_CA_DIR, by default the
# system's), TLS_CLIENT_CA (authorities a
# caller's certificate may be signed by; empty: callers are not asked for one).
set -eu
DIR=/run/opensips/tlsdb
FIFO=/run/opensips/opensips_fifo

# A value as the table wants it: one line, with its separator escaped.
cell() { awk 'BEGIN { ORS = "\\n" } { gsub(/\\/, "\\\\"); gsub(/:/, "\\:"); print }' "$1"; }

fingerprint() { cat "$TLS_CERT" "$TLS_KEY" ${TLS_CA_LIST:-} ${TLS_CLIENT_CA:-} 2>/dev/null | cksum; }

check() {
  openssl x509 -in "$TLS_CERT" -noout 2>/dev/null || { echo "tls: $TLS_CERT is not a certificate" >&2; return 1; }
  [ "$(openssl x509 -in "$TLS_CERT" -noout -pubkey 2>/dev/null | cksum)" = "$(openssl pkey -in "$TLS_KEY" -pubout 2>/dev/null | cksum)" ] \
    || { echo "tls: $TLS_KEY is not the key of $TLS_CERT" >&2; return 1; }
  for f in ${TLS_CA_LIST:-} ${TLS_CLIENT_CA:-}; do
    openssl x509 -in "$f" -noout 2>/dev/null || { echo "tls: $f holds no certificate" >&2; return 1; }
  done
  # Each file is one value of the table, and the SIP core reads no value
  # longer than DB_TEXT_BUFFER (run.sh, T-50): a longer one would make the
  # whole table unreadable, at a reload and at the next start alike. (The
  # value is the file plus at most a closing line break.)
  for f in "$TLS_CERT" "$TLS_KEY" ${TLS_CA_LIST:-} ${TLS_CLIENT_CA:-}; do
    size=$(wc -c < "$f" | tr -d ' ')
    [ "$size" -lt "${DB_TEXT_BUFFER:-4096}" ] \
      || { echo "tls: $f is $size bytes; the SIP core reads files of less than ${DB_TEXT_BUFFER:-4096}" >&2; return 1; }
  done
}

write() {
  check || return 1
  mkdir -p "$DIR"
  cert=$(cell "$TLS_CERT") key=$(cell "$TLS_KEY")
  # Callers: asked for a certificate only when there is something to check
  # it against, and never refused for having none (a trunk may demand one;
  # that is the engine's decision).
  in_verify=0 in_ca=
  if [ -n "${TLS_CLIENT_CA:-}" ]; then in_verify=1 in_ca=$(cell "$TLS_CLIENT_CA"); fi
  # Vendors: the certificate must be signed by TLS_CA_LIST or, without it,
  # by an authority in the system's directory (looked up on demand; loading
  # the whole system bundle as a list took twenty seconds of every start).
  out_ca= out_dir=${TLS_CA_DIR:-/etc/ssl/certs}
  if [ -n "${TLS_CA_LIST:-}" ]; then out_ca=$(cell "$TLS_CA_LIST") out_dir=/nonexistent; fi
  {
    echo 'id(int,auto) domain(string) match_ip_address(string,null) match_sip_domain(string,null) type(int) method(string,null) verify_cert(int,null) require_cert(int,null) certificate(blob,null) private_key(blob,null) crl_check_all(int,null) crl_dir(string,null) ca_list(blob,null) ca_dir(string,null) cipher_list(string,null) dh_params(blob,null) ec_curve(string,null) '
    # printf, not echo: this shell's echo would turn the escapes back into
    # line breaks.
    printf '%s\n' "1:in:*:*:2::$in_verify:0:$cert:$key:0::$in_ca:/nonexistent:::"
    printf '%s\n' "2:out:*:*:1::1:1:$cert:$key:0::$out_ca:$out_dir:::"
  } > "$DIR/tls_mgm.new"
  mv "$DIR/tls_mgm.new" "$DIR/tls_mgm"
  # The core checks the table's layout version before reading it.
  printf 'table_name(string) table_version(int) \ntls_mgm:3\n' > "$DIR/version"
}

case "${1:-}" in
write) write ;;
watch)
  last=$(fingerprint)
  while sleep "${TLS_WATCH_SEC:-30}"; do
    now=$(fingerprint)
    [ "$now" = "$last" ] && continue
    # A half-copied pair fails the check and is tried again next round.
    write || continue
    [ -p "$FIFO" ] || continue
    rm -f /tmp/tls_reload && mkfifo /tmp/tls_reload
    printf ':tls_reload:{"jsonrpc":"2.0","method":"tls_reload","id":1}\n' > "$FIFO"
    if timeout 10 cat /tmp/tls_reload | grep -q '"result"'; then
      last=$now
      echo "tls: certificate reloaded (expires $(openssl x509 -in "$TLS_CERT" -noout -enddate | cut -d= -f2))" >&2
    else
      echo "tls: reload failed; the previous certificate stays in use" >&2
    fi
  done ;;
*) echo "usage: $0 write|watch" >&2; exit 2 ;;
esac
