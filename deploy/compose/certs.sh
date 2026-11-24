#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Makes the lab's TLS material once: a lab certificate authority, a
# certificate for the SIP core, one for the TLS vendor and one a caller
# presents. Lab only: the
# keys are unprotected and the files are not kept in the repository.
set -euo pipefail
DIR=$(cd "$(dirname "$0")" && pwd)/certs
[ -f "$DIR/ca.crt" ] && [ -f "$DIR/opensips.crt" ] && [ -f "$DIR/vendor.crt" ] && [ -f "$DIR/caller.crt" ] && [ -d "$DIR/roots" ] && exit 0
mkdir -p "$DIR"
cd "$DIR"
issue() { # issue <name> <address>; existing ones are kept
  [ -f "$1.crt" ] && return
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$1.lab.test" -keyout "$1.key" -out "$1.csr" 2>/dev/null
  printf 'subjectAltName=DNS:%s.lab.test,IP:%s\n' "$1" "$2" > "$1.ext"
  openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 3650 -extfile "$1.ext" -out "$1.crt" 2>/dev/null
  rm -f "$1.csr" "$1.ext"
}
[ -f ca.crt ] || openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=softswitch lab CA" -keyout ca.key -out ca.crt 2>/dev/null
# The authority again, the way a system keeps its trusted ones: a directory
# of files named by the hash of their subject.
mkdir -p roots && cp ca.crt "roots/$(openssl x509 -in ca.crt -noout -subject_hash).0"
issue opensips 172.30.0.10
issue vendor 172.30.0.24
issue caller 172.30.0.21
chmod 644 ./*.key # read inside the lab containers, which run as other users
echo "lab certificates written to $DIR"
