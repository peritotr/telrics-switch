#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Runs inside the lab's caller container, started by test/sipp/run.sh: the
# IPv6 scenarios (T-28). The container is on the lab's IPv6 network as well
# (fd30::21 on eth1, deploy/compose/docker-compose.yml); the other IPv6
# addresses a scenario calls from are added here. The switch listens on
# fd30::10 besides 172.30.0.10, the media relay has fd30::11 besides
# 172.30.0.11, vendor V6 answers at fd30::20 and vendor C at 172.30.0.20.
#
#   v6lab.sh <function> [arguments]
set -u
SWITCH4=172.30.0.10:5060
SWITCH6='[fd30::10]:5060'
RELAY=172.30.0.11:2223
PASSWORD=lab-only-Nat-2026
PBX=fd30::72        # where the trunk "lab-v6" registers from, port 5077
DIR=/work/test/sipp
# 100 packets of audio, 20 ms apart (written by run.sh). SIPp replays the
# payload over a socket of the call's own family.
AUDIO=/work/out/v6-rtp.pcap

add6() { ip -6 addr add "$1/64" dev eth1 nodad 2>/dev/null; }

# Each run of this script is a new container with a new hardware address,
# and the switch may still hold the previous one. Forgetting the switch's
# here makes the next packet ask for it again (neighbour solicitation),
# and the question tells the switch who has the address it comes from.
announce() { ip neigh flush all 2>/dev/null; ip -6 neigh flush all 2>/dev/null; }

# hello <address>: a side that is sent to before it sends anything (a
# vendor) tells the switch, the relay and the engine its hardware address
# first, with one datagram from that address to each.
hello() {
  for peer in fd30::10 fd30::11 fd30::40; do
    echo | socat -u - "UDP6:[$peer]:9,bind=[$1]" 2>/dev/null
  done
}

# Datagrams this container took in, over both families: delivered, or
# arrived at a port nobody reads.
udp_in() {
  v4=$(awk '/^Udp: [0-9]/ { print $2 + $3 }' /proc/net/snmp)
  v6=$(awk '$1 == "Udp6InDatagrams" || $1 == "Udp6NoPorts" { n += $2 } END { print n + 0 }' /proc/net/snmp6)
  echo $((v4 + v6))
}

# relay <call id>: each audio stream the media relay holds for the call, as
# "<family> <address> <packets received>", one a line (RTCP left out). Each
# question has a cookie of its own: the relay answers a cookie it has seen
# with the answer it gave then.
relay() {
  printf "q$$x%s d7:call-id%d:%s7:command5:querye" "$(date +%s%N)" ${#1} "$1" | socat -t2 - "UDP:$RELAY" |
    awk '{
      n = split($0, parts, "local porti")
      for (i = 2; i <= n; i++) {
        s = parts[i]
        p = index(s, "8:endpointd6:family4:IPv")
        if (p == 0 || index(s, "flagsl4:RTCP") > 0) continue
        fam = "IPv" substr(s, p + 24, 1)
        rest = substr(s, p + index(substr(s, p), "7:address") + 8)
        if (!match(rest, /^[0-9]+:/)) continue
        addr = substr(rest, RLENGTH + 1, substr(rest, 1, RLENGTH - 1) + 0)
        k = index(s, "statsd7:packetsi")
        pk = substr(s, k + 16)
        sub(/e.*/, "", pk)
        print fam, addr, pk
      }
    }'
}

# call <switch> <source> <number> <call id prefix> [audio]
# A call from the source, which hangs up after two seconds. With "audio" it
# plays 100 packets to the vendor, which sends each back; while the call is
# up the relay is asked what it holds (relay), and the datagrams that
# reached the caller are counted. Prints the relay's streams, then
# "back=<datagrams> caller=<SIPp's result>".
call() {
  scenario=$DIR/uac.xml
  if [ "${5:-}" = audio ]; then
    sed "s|<pause />|<nop><action><exec play_pcap_audio=\"$AUDIO\" /></action></nop><pause />|" $DIR/uac.xml > /tmp/caller.xml
    scenario=/tmp/caller.xml
  fi
  announce
  before=$(udp_in)
  timeout 30 sipp -sf $scenario "$1" -i "$2" -s "$3" -cid_str "$4-%u@%s" -d 3500 -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  pid=$!
  if [ "${5:-}" = audio ]; then
    sleep 3
    relay "$4-1@$2"
  fi
  wait $pid
  rc=$?
  echo "back=$(( $(udp_in) - before )) caller=$rc"
  return $rc
}

# from <source> <number> <call id prefix>: a call answered by vendor V6
# from another address of the lab's IPv6 network.
from() {
  add6 "$1"
  announce
  timeout 20 sipp -sf $DIR/uac.xml "$SWITCH6" -i "$1" -s "$2" -cid_str "$3-%u@%s" -d 1000 -m 1 -nostdin -timeout 15 >/dev/null 2>&1
}

# stranger <source> <call id prefix>: a call from an address no trunk has,
# which the switch must ask to sign in (401).
stranger() {
  add6 "$1"
  announce
  sed 's/__CODE__/401/' $DIR/uac-refused.xml > /tmp/refused.xml
  timeout 10 sipp -sf /tmp/refused.xml "$SWITCH6" -i "$1" -s 8601555111 -cid_str "$2-%u@%s" -m 1 -nostdin -timeout 8 >/dev/null 2>&1
}

# probe <source> <answer|silence>: OPTIONS from the source.
probe() {
  add6 "$1"
  announce
  sed "s/__EXPECT__/$2/" $DIR/uac-options.xml > /tmp/options.xml
  timeout 10 sipp -sf /tmp/options.xml "$SWITCH6" -i "$1" -m 1 -nostdin >/dev/null 2>&1
}

# register <expires> <answer> <text in the answer>
# "lab-v6" registers over IPv6 from [fd30::72]:5077, naming where it is.
register() {
  add6 $PBX
  sed -e 's/__SELF__/[local_ip]:[local_port]/' -e 's/__CONTACT__/<sip:lab-v6@[local_ip]:[local_port]>/' \
    -e "s/__EXPIRES__/$1/" -e "s/__FINAL__/$2/" -e "s/__EXPECT__/$3/" $DIR/uac-register.xml > /tmp/register.xml
  announce
  timeout 15 sipp -sf /tmp/register.xml "$SWITCH6" -i $PBX -p 5077 -s lab-v6 -au lab-v6 -ap $PASSWORD -m 1 -nostdin >/dev/null 2>&1
}

# to_trunk <call id prefix> <number>
# "lab-v6" registers over IPv6, then answers a call at its registration
# (8603…, delivered there by the lab's engine) from the lab's IPv4 caller,
# with audio both ways: the PBX sends back what it gets. Prints the relay's
# streams, what the PBX was sent ("uri=<request URI> media=<c= line>"), and
# how both sides fared.
to_trunk() {
  register 600 200 'expires=600' || { echo "registration failed"; return 1; }
  hello $PBX
  rm -f /tmp/pbx-messages.log
  timeout 40 sipp -sf $DIR/uas.xml -i $PBX -p 5077 -m 1 -rtp_echo -nostdin -trace_msg -message_file /tmp/pbx-messages.log >/dev/null 2>&1 &
  pbx_pid=$!
  sleep 1
  call $SWITCH4 172.30.0.21 "$2" "$1" audio
  caller_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  uri=$(grep -m1 '^INVITE ' /tmp/pbx-messages.log 2>/dev/null | awk '{ print $2 }')
  media=$(grep -m1 '^c=' /tmp/pbx-messages.log 2>/dev/null | tr -d '\r')
  echo "uri=$uri media=$media pbx=$pbx_rc"
  [ $caller_rc -eq 0 ] && [ $pbx_rc -eq 0 ]
}

# late <call id prefix> <number>
# A call without an offer (RFC 3261 13.2.1) from the lab's IPv4 caller to
# vendor V6's connection at fd30::64 (8604…), which makes the offer in its
# 200 and checks that the caller's answer reaches it through the relay's
# IPv6 address. Prints the address the caller was offered and how both
# sides fared.
late() {
  add6 fd30::64
  announce
  hello fd30::64
  sed -e 's/__PLAN__/take/' -e 's/__ACK__/answer/' -e 's/c=IN IP4 172\\\.30\\\.0\\\.11/c=IN IP6 fd30::11/' \
    $DIR/uas-late.xml > /tmp/vendor.xml
  timeout 30 sipp -sf /tmp/vendor.xml -i fd30::64 -p 5060 -m 1 -rtp_echo -nostdin -timeout 25 >/dev/null 2>&1 &
  vendor_pid=$!
  sleep 1
  sed -e 's/__PLAN__/plain/' -e "s|__RTP__|$AUDIO|" -e 's/__WAIT__/1000/' $DIR/uac-late.xml > /tmp/caller.xml
  rm -f /tmp/caller-messages.log
  timeout 30 sipp -sf /tmp/caller.xml $SWITCH4 -i 172.30.0.21 -s "$2" -cid_str "$1-%u@%s" -m 1 -nostdin -timeout 20 \
    -trace_msg -message_file /tmp/caller-messages.log >/dev/null 2>&1
  caller_rc=$?
  wait $vendor_pid
  vendor_rc=$?
  offer=$(grep -m1 '^c=' /tmp/caller-messages.log 2>/dev/null | tr -d '\r')
  echo "offer=$offer caller=$caller_rc vendor=$vendor_rc"
  [ $caller_rc -eq 0 ] && [ $vendor_rc -eq 0 ]
}

"$@"
