#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
#
# Writes the known-address sets of softswitch.nft (known_v4, known_v6) from
# the panel: the addresses and networks of every enabled customer trunk that
# is recognised by address, and the addresses of every enabled vendor
# connection (host names are resolved here). Guide: docs/09-kurulum.md,
# "Ağ katmanı koruması".
#
#   known-sources.sh [--apply] [--allow-empty] [OUTPUT]
#
# OUTPUT (default /etc/softswitch/known-sources.nft) is replaced only when
# its content changes, so running this every minute from cron or a systemd
# timer is cheap and safe. --apply also loads the file into the running
# ruleset (one transaction: the sets are flushed and refilled at once).
# Nothing is written when anything fails: the API cannot be read, a vendor's
# host does not resolve, an address looks wrong, or the list is empty (an
# empty list in "known addresses only" mode would cut every trunk off;
# --allow-empty writes it anyway).
#
# Settings (environment):
#   SOFTSWITCH_API           the API, default http://127.0.0.1:58080
#   SOFTSWITCH_API_KEY_FILE  a file holding an API key (a readonly one is
#                            enough), default /etc/softswitch/nft-sync.key.
#                            The key goes to curl on its standard input; it is
#                            never printed, logged or put on a command line.
#   KNOWN_EXTRA              optional file of more addresses or networks, one
#                            per line (# starts a comment): trunks that sign
#                            in with a password from fixed addresses, other
#                            switches, monitoring
#   PAGE_SIZE                rows asked for per request, default 1000 (the
#                            API's maximum)
set -eu

api=${SOFTSWITCH_API:-http://127.0.0.1:58080}
key_file=${SOFTSWITCH_API_KEY_FILE:-/etc/softswitch/nft-sync.key}
extra=${KNOWN_EXTRA:-}
page=${PAGE_SIZE:-1000}
apply=0
allow_empty=0
out=/etc/softswitch/known-sources.nft

while [ $# -gt 0 ]; do
	case $1 in
	--apply) apply=1 ;;
	--allow-empty) allow_empty=1 ;;
	-h | --help)
		sed -n '4,33p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*)
		echo "known-sources: unknown option $1" >&2
		exit 2
		;;
	*) out=$1 ;;
	esac
	shift
done

fail() {
	echo "known-sources: $*; $out left as it was" >&2
	exit 1
}

for tool in curl jq getent; do
	command -v "$tool" >/dev/null 2>&1 || fail "$tool is needed"
done
[ -r "$key_file" ] || fail "cannot read the API key file $key_file"
key=$(tr -d ' \t\r\n' <"$key_file")
[ -n "$key" ] || fail "the API key file $key_file is empty"
# The key is written into curl's configuration below; anything that could
# break out of the quoted value is refused (keys are not made of it).
case $key in *[!A-Za-z0-9._~+/=-]*) fail "the API key file $key_file holds characters an API key does not have" ;; esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# All rows of a listing, page by page, as one JSON array.
fetch() {
	offset=0
	: >"$work/rows"
	while :; do
		printf 'header = "Authorization: Bearer %s"\n' "$key" |
			curl -sS --fail --max-time 20 -K - "$api$1?limit=$page&offset=$offset" >"$work/page" ||
			fail "cannot read $1 from $api"
		jq -e 'type == "array"' "$work/page" >/dev/null 2>&1 || fail "$1 did not return a list"
		cat "$work/page" >>"$work/rows"
		n=$(jq length "$work/page")
		[ "$n" -lt "$page" ] && break
		offset=$((offset + page))
	done
	jq -s add "$work/rows"
}

fetch /v1/client-trunks >"$work/trunks"
fetch /v1/vendor-connections >"$work/vendors"

jq -r '.[] | select(.enabled == true and .auth_type == "ip") | .addresses[]?' "$work/trunks" >"$work/addrs"

# Vendors: an address as it is, a name through the resolver (all of its
# addresses; a vendor whose name later points elsewhere is caught up with on
# the next run).
jq -r '.[] | select(.enabled == true) | "\(.id) \(.host)"' "$work/vendors" |
	while read -r id host; do
		case $host in
		'' | *[!A-Za-z0-9.:-]*) fail "vendor connection $id has a host that is not a name or an address" ;;
		esac
		# An address is taken as it is (the resolver would drop an IPv6
		# address on a server that has none of its own).
		case $host in
		*:*) # IPv6
			echo "$host"
			continue
			;;
		*[!0-9.]*) ;; # a name
		*)            # IPv4
			echo "$host"
			continue
			;;
		esac
		# Each family on its own: a single "ahosts" query can stop at
		# the first family it finds.
		{ getent ahostsv4 "$host" || :; getent ahostsv6 "$host" || :; } |
			awk '{print $1}' | grep -v '^::ffff:' | sort -u >"$work/resolved" || :
		[ -s "$work/resolved" ] || fail "vendor connection $id: $host does not resolve"
		cat "$work/resolved"
	done >>"$work/addrs"

if [ -n "$extra" ]; then
	[ -r "$extra" ] || fail "cannot read $extra"
	sed -e 's/#.*//' -e 's/[[:space:]]//g' -e '/^$/d' "$extra" >>"$work/addrs"
fi

: >"$work/v4"
: >"$work/v6"
sort -u "$work/addrs" | while read -r a; do
	case $a in
	*:*)
		case $a in *[!0-9A-Fa-f:./]*) fail "not an address: $a" ;; esac
		echo "$a" >>"$work/v6"
		;;
	*)
		case $a in *[!0-9./]* | '') fail "not an address: $a" ;; esac
		echo "$a" >>"$work/v4"
		;;
	esac
done

n4=$(wc -l <"$work/v4" | tr -d ' ')
n6=$(wc -l <"$work/v6" | tr -d ' ')
if [ "$n4" -eq 0 ] && [ "$n6" -eq 0 ] && [ "$allow_empty" -eq 0 ]; then
	fail "no trunk or vendor address found (--allow-empty writes an empty set)"
fi

element() { # set file
	[ -s "$2" ] || return 0
	printf 'add element inet softswitch %s { %s }\n' "$1" "$(paste -sd, "$2" | sed 's/,/, /g')"
}

{
	echo "# Written by known-sources.sh from the panel's trunks and vendor connections; do not edit."
	echo "flush set inet softswitch known_v4"
	echo "flush set inet softswitch known_v6"
	element known_v4 "$work/v4"
	element known_v6 "$work/v6"
} >"$work/new"

if [ -f "$out" ] && cmp -s "$work/new" "$out"; then
	state=unchanged
else
	dir=$(dirname "$out")
	tmp=$(mktemp "$dir/.known-sources.XXXXXX") || fail "cannot write in $dir"
	cat "$work/new" >"$tmp"
	chmod 0644 "$tmp"
	mv -f "$tmp" "$out"
	state=updated
fi

if [ "$apply" -eq 1 ]; then
	nft -f "$out" || fail "nft did not load $out (is softswitch.nft loaded?)"
fi
echo "known-sources: $out $state ($n4 IPv4, $n6 IPv6 entries)"
