#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Runs inside the lab's caller container, started by test/sipp/run.sh: plays
# a client's PBX behind address translation (NAT). The PBX names private
# addresses for itself (10.0.0.5, 10.0.0.6) in everything it sends, while
# its packets really come from lab addresses the switch knows nothing about
# (172.30.0.70, 172.30.0.71). There is no translating router in the lab: the
# mismatch between what is named and where packets come from is what a
# router would cause, and the named addresses lead nowhere. A vendor of its
# own (reoffer_caller) is at 172.30.0.68.
#
#   natlab.sh <function> [arguments]
set -u
SWITCH=172.30.0.10:5060
REAL1=172.30.0.70   # where "lab-nat" really is; it listens on port 5077
REAL2=172.30.0.71   # where "lab-nat-2" really is, port 5078
PASSWORD=lab-only-Nat-2026
DIR=/work/test/sipp
ip addr add $REAL1/24 dev eth0 2>/dev/null
ip addr add $REAL2/24 dev eth0 2>/dev/null

# Each run of this script is a new container with a new hardware address,
# and the switch may still hold the previous one for these lab addresses.
# Forgetting the switch's own hardware address here makes the next packet
# ask for it again, and that question tells the switch who has the address
# the packet comes from.
announce() { ip neigh flush all 2>/dev/null; }

# register <user> <password> <named address | "real"> <contact | "real" | "*"> <expires> <answer> <text in the answer>
# Registers over UDP from 172.30.0.70:5077 (or from $SRC). "real" names the address
# the request really comes from, as a PBX with a public address of its own
# would.
register() {
  self=$3 contact=$4
  [ "$self" = real ] && self='[local_ip]:[local_port]'
  [ "$contact" = real ] && contact="<sip:$1@[local_ip]:[local_port]>"
  sed -e "s/__SELF__/$self/" -e "s/__CONTACT__/$contact/" -e "s/__EXPIRES__/$5/" -e "s/__FINAL__/$6/" -e "s/__EXPECT__/$7/" \
    $DIR/uac-register.xml > /tmp/register.xml
  announce
  if [ -n "${TLS:-}" ]; then
    timeout 15 sipp -sf /tmp/register.xml 172.30.0.10:5061 -t l1 -tls_cert /work/certs/vendor.crt -tls_key /work/certs/vendor.key \
      -i ${SRC:-$REAL1} -p 5077 -s "$1" -au "$1" -ap "$2" -m 1 -nostdin >/dev/null 2>&1
    return
  fi
  timeout 15 sipp -sf /tmp/register.xml $SWITCH -i ${SRC:-$REAL1} -p 5077 -s "$1" -au "$1" -ap "$2" -m 1 -nostdin >/dev/null 2>&1
}

# register_tls <as register>: the same over TLS.
register_tls() { TLS=1 register "$@"; }

# probe <answer|silence>
# The PBX asks whether the switch is there (OPTIONS), from the address
# "lab-nat" registers from, and gets an answer or none.
probe() {
  sed "s/__EXPECT__/$1/" $DIR/uac-options.xml > /tmp/options.xml
  announce
  timeout 15 sipp -sf /tmp/options.xml $SWITCH -i $REAL1 -p 5077 -m 1 -nostdin >/dev/null 2>&1
}

# flood
# Six hundred registrations in three seconds from an address nobody knows
# (172.30.0.72), each with a wrong password; then one with the right
# password from the same address. The switch has stopped listening to the
# address by then: the result is that of the last registration, which gets
# no answer and runs into its time limit (124).
flood() {
  ip addr add 172.30.0.72/24 dev eth0 2>/dev/null
  sed -e 's/__SELF__/[local_ip]:[local_port]/' -e 's/__CONTACT__/<sip:lab-nat-2@[local_ip]:[local_port]>/' -e 's/__EXPIRES__/600/' \
    -e 's/__FINAL__/401/' -e 's/__EXPECT__/WWW-Authenticate/' $DIR/uac-register.xml > /tmp/flood.xml
  announce
  timeout 8 sipp -sf /tmp/flood.xml $SWITCH -i 172.30.0.72 -p 5079 -s lab-nat-2 -au lab-nat-2 -ap not-the-password \
    -m 600 -r 200 -l 600 -nostdin >/dev/null 2>&1
  SRC=172.30.0.72 register lab-nat-2 $PASSWORD real real 600 200 expires=
}

# nat_caller <call id prefix> <number> <hangup|wait> <rtp|quiet> [pause ms]
# A call from "lab-nat-2", behind address translation at 172.30.0.71:5078.
nat_caller() {
  drop='/<!-- rtp -->/d'
  [ "$4" = rtp ] && drop=''
  sed -e "s/__END__/$3/" -e "$drop" $DIR/uac-nat.xml > /tmp/caller.xml
  announce
  timeout 30 sipp -sf /tmp/caller.xml $SWITCH -i $REAL2 -p 5078 -s "$2" -au lab-nat-2 -ap $PASSWORD \
    -cid_str "$1-%u@%s" -d "${5:-1500}" -m 1 -nostdin -timeout 28 >/dev/null 2>&1
}

# plain_caller <call id prefix> <number> <hangup|wait>
# A call from the lab's ordinary client, known by its address.
plain_caller() {
  scenario=uac.xml
  [ "$3" = wait ] && scenario=uac-cutoff.xml
  announce
  timeout 30 sipp -sf $DIR/$scenario $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-%u@%s" -d 1500 -m 1 -nostdin -timeout 28 >/dev/null 2>&1
}

# to_trunk <call id prefix> <number> <plain|nat> <who ends it: caller|trunk|switch>
# The PBX "lab-nat" registers from behind address translation, then waits
# for a call where its REGISTER came from, and a caller places one.
# Succeeds when both sides saw the whole call, including the end of it.
to_trunk() {
  register lab-nat $PASSWORD 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 expires= || { echo "registration failed"; return 1; }
  pbx=wait caller=hangup
  [ "$4" = trunk ] && pbx=hangup
  [ "$4" != caller ] && caller=wait
  sed "s/__END__/$pbx/" $DIR/uas-trunk.xml > /tmp/pbx.xml
  timeout 40 sipp -sf /tmp/pbx.xml -i $REAL1 -p 5077 -m 1 -nostdin >/dev/null 2>&1 &
  pbx_pid=$!
  sleep 1
  if [ "$3" = nat ]; then nat_caller "$1" "$2" $caller quiet; else plain_caller "$1" "$2" $caller; fi
  caller_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  echo "caller=$caller_rc pbx=$pbx_rc"
  [ $caller_rc -eq 0 ] && [ $pbx_rc -eq 0 ]
}

# A caller that offers G.711 both ways (µ-law first) and key presses (as in
# connlab.sh).
BOTH='s/RTP\/AVP 8/RTP\/AVP 0 8 101/; s/a=rtpmap:8 PCMA\/8000/a=rtpmap:0 PCMU\/8000\n      a=rtpmap:8 PCMA\/8000\n      a=rtpmap:101 telephone-event\/8000/'

# limit_trunk <call id prefix> <number>
# "lab-nat" may take one call at a time and only PCMA (run.sh sets both).
# It registers from behind address translation and takes a first call
# (<prefix>-a), held for five seconds, from a caller that offers PCMU, PCMA
# and key presses. A second call meanwhile (<prefix>-b) must be refused
# with 503 and never reach the PBX: the PBX takes one call, and a second
# INVITE would make it fail. Prints how each side fared and the audio line
# of the offer the PBX got.
limit_trunk() {
  register lab-nat $PASSWORD 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 expires= || { echo "registration failed"; return 1; }
  sed "s/__END__/wait/" $DIR/uas-trunk.xml > /tmp/pbx.xml
  rm -f /tmp/pbx-messages.log
  timeout 40 sipp -sf /tmp/pbx.xml -i $REAL1 -p 5077 -m 1 -nostdin -trace_msg -message_file /tmp/pbx-messages.log >/dev/null 2>&1 &
  pbx_pid=$!
  sed "$BOTH" $DIR/uac.xml > /tmp/caller.xml
  sed 's/__CODE__/503/' $DIR/uac-refused.xml > /tmp/refused.xml
  sleep 1
  announce
  timeout 30 sipp -sf /tmp/caller.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-a-%u@%s" -d 5000 -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  first_pid=$!
  sleep 2
  timeout 20 sipp -sf /tmp/refused.xml $SWITCH -i 172.30.0.21 -p 5091 -mp 6100 -s "$2" -cid_str "$1-b-%u@%s" -m 1 -nostdin -timeout 15 >/dev/null 2>&1
  second_rc=$?
  wait $first_pid
  first_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  offer=$(grep -m1 'm=audio' /tmp/pbx-messages.log 2>/dev/null | sed 's/.*RTP\/AVP/RTP\/AVP/' | tr -d '\r')
  echo "first=$first_rc second=$second_rc pbx=$pbx_rc offer=$offer"
}

# A call that "lab-nat" places itself, signed in, from 172.30.0.70:5080
# (its registration and its PBX keep 5077): own_call <call id> <number>
# <200: answered, ended after five seconds | 503: refused after the
# challenge>.
own_call() {
  if [ "$3" = 200 ]; then
    sed 's/__FINAL__/200/' $DIR/uac-digest.xml > /tmp/own.xml
  else
    sed -e 's/__FINAL__/401/' -e 's/response="401" optional="true" next="refused"/response="503" optional="true" next="refused"/' \
      $DIR/uac-digest.xml > /tmp/own.xml
  fi
  timeout 30 sipp -sf /tmp/own.xml $SWITCH -i $REAL1 -p 5080 -s "$2" -au lab-nat -ap $PASSWORD -cid_str "$1-%u@%s" \
    -d 5000 -m 1 -nostdin -timeout 25 >/dev/null 2>&1
}

# limit_both <call id prefix> <number delivered to lab-nat> <number of a vendor>
# "lab-nat" may have one call at a time, in either direction (run.sh sets
# it; ADR-0042). First it places a call (<prefix>-a, five seconds) and a
# call to it meanwhile (<prefix>-b) must be refused with 503; then a call
# is delivered to it (<prefix>-c, five seconds) and its own call meanwhile
# (<prefix>-d) must be refused with 503. Prints how each fared, in that
# order, and how the PBX fared with the one call it should get.
limit_both() {
  register lab-nat $PASSWORD 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 expires= || { echo "registration failed"; return 1; }
  sed 's/__CODE__/503/' $DIR/uac-refused.xml > /tmp/refused.xml
  sed "s/__END__/wait/" $DIR/uas-trunk.xml > /tmp/pbx.xml
  announce
  own_call "$1-a" "$3" 200 &
  a_pid=$!
  sleep 2
  timeout 20 sipp -sf /tmp/refused.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-b-%u@%s" -m 1 -nostdin -timeout 15 >/dev/null 2>&1
  b_rc=$?
  wait $a_pid
  a_rc=$?
  timeout 40 sipp -sf /tmp/pbx.xml -i $REAL1 -p 5077 -m 1 -nostdin >/dev/null 2>&1 &
  pbx_pid=$!
  sleep 1
  timeout 30 sipp -sf $DIR/uac.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-c-%u@%s" -d 5000 -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  c_pid=$!
  sleep 2
  own_call "$1-d" "$3" 503
  d_rc=$?
  wait $c_pid
  c_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  echo "a=$a_rc b=$b_rc c=$c_rc d=$d_rc pbx=$pbx_rc"
}

# limit_client <call id prefix> <number delivered to lab-nat> <number of a vendor>
# The client of "lab-nat" and "lab-nat-2" may have one call at a time (run.sh
# sets it; ADR-0042): a call "lab-nat-2" places (<prefix>-a, five seconds)
# keeps a call to "lab-nat" (<prefix>-b) out with 503. Prints how both fared.
limit_client() {
  register lab-nat $PASSWORD 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 expires= || { echo "registration failed"; return 1; }
  sed 's/__CODE__/503/' $DIR/uac-refused.xml > /tmp/refused.xml
  nat_caller "$1-a" "$3" hangup quiet 5000 &
  a_pid=$!
  sleep 2
  timeout 20 sipp -sf /tmp/refused.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-b-%u@%s" -m 1 -nostdin -timeout 15 >/dev/null 2>&1
  b_rc=$?
  wait $a_pid
  echo "a=$? b=$b_rc"
}

# media <call id prefix> <number>
# A call from behind address translation that sends audio; the vendor it
# reaches sends back what it gets. Prints what the media relay knows about
# the call while it is up (its answer to "query", as it comes), and how
# many datagrams arrived at this container during the call: the signalling
# is a handful, the rest is audio that found its way back.
udp_in() { awk '/^Udp: [0-9]/ { print $2 + $3 }' /proc/net/snmp; } # delivered, or arrived at a port nobody reads
media() {
  before=$(udp_in)
  nat_caller "$1" "$2" hangup rtp 5000 &
  pid=$!
  sleep 4
  id="$1-1@$REAL2"
  printf 'q1 d7:call-id%d:%s7:command5:querye' ${#id} "$id" | socat -t2 - UDP:172.30.0.11:2223
  echo
  wait $pid
  rc=$?
  echo "datagrams=$(( $(udp_in) - before )) caller=$rc"
  return $rc
}

# reoffer_caller <call id prefix> <number>
# A vendor at 172.30.0.68 (8723) puts the call of "lab-nat-2", behind
# address translation, on hold half a second after the answer: the request
# must reach the caller where its INVITE came from. The caller hangs up.
reoffer_caller() {
  ip addr add 172.30.0.68/24 dev eth0 2>/dev/null
  announce
  # The switch and the engine may still hold another hardware address for
  # the vendor's: one datagram from it tells them (as in connlab.sh).
  for peer in 172.30.0.10 172.30.0.40; do
    echo | socat -u - "UDP:$peer:9,bind=172.30.0.68" 2>/dev/null
  done
  sed 's/__PLAN__/nat/' $DIR/uas-reinvite.xml > /tmp/vendor.xml
  timeout 40 sipp -sf /tmp/vendor.xml -i 172.30.0.68 -p 5060 -m 1 -nostdin >/dev/null 2>&1 &
  vendor_pid=$!
  sleep 1
  nat_caller "$1" "$2" reoffer quiet
  caller_rc=$?
  wait $vendor_pid
  vendor_rc=$?
  echo "caller=$caller_rc vendor=$vendor_rc"
}

# tls_trunk <call id prefix> <number>
# The PBX "lab-nat-tls" registers over TLS from 172.30.0.70, naming a
# private address, and gets a call on the same connection, in the same SIPp
# process (uas-trunk-tls.xml): the caller's INVITE carries the PBX's
# Call-ID so that SIPp files it under the PBX's scenario. The PBX checks it
# is offered encrypted audio with a key; the caller, which offers plain
# audio, checks it gets plain audio back (uac.xml). The caller hangs up.
# Prints how each side fared and the audio line of the offer the PBX got.
tls_trunk() {
  rm -f /tmp/pbx-messages.log
  announce
  timeout 40 sipp -sf $DIR/uas-trunk-tls.xml 172.30.0.10:5061 -t l1 -tls_cert /work/certs/vendor.crt -tls_key /work/certs/vendor.key \
    -i $REAL1 -p 5077 -s lab-nat-tls -au lab-nat-tls -ap $PASSWORD -cid_str "$1-1@lab" -m 1 -nostdin -timeout 35 \
    -trace_msg -message_file /tmp/pbx-messages.log >/dev/null 2>&1 &
  pbx_pid=$!
  sleep 2
  timeout 30 sipp -sf $DIR/uac.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-1@lab" -d 2000 -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  caller_pid=$!
  # While the call is up: SIPp also listens on its port, so a connection
  # the switch opened anew would be taken too. The PBX's connections to
  # the switch must be the one it opened itself (to port 5061), alone.
  sleep 1
  conns=$(ss -tn state established "( sport = :5077 )" 2>/dev/null | awk 'NR > 1 { print $NF }' | paste -sd ' ' -)
  wait $caller_pid
  caller_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  offer=$(grep -m1 'm=audio' /tmp/pbx-messages.log 2>/dev/null | sed 's/.*RTP\//RTP\//' | tr -d '\r')
  keyed=$(grep -c '^a=crypto:' /tmp/pbx-messages.log 2>/dev/null)
  echo "caller=$caller_rc pbx=$pbx_rc offer=$offer keyed=$([ "${keyed:-0}" -ge 1 ] && echo yes || echo no) conns=$conns"
}

# gone_tls <call id prefix> <number> <udp|none>
# A trunk registers over TLS and its connection is closed before the call
# (the registering process ends). With "udp", "lab-nat-mix" has registered
# over UDP first, from behind address translation, and a PBX listens there
# (uas-trunk.xml, which takes plain audio only): the attempt over the
# closed connection fails and the call goes to the UDP registration. With
# "none", "lab-nat-tls" has no other registration: the caller must be
# refused with 503 (uac-refused.xml). Prints how each side fared.
gone_tls() {
  if [ "$3" = udp ]; then
    user=lab-nat-mix
    register $user $PASSWORD 10.0.0.5:5060 "<sip:$user@10.0.0.5:5060>" 3600 200 expires= || { echo "registration over UDP failed"; return 1; }
  else
    user=lab-nat-tls
  fi
  register_tls $user $PASSWORD 10.0.0.5:5061 "<sip:$user@10.0.0.6:5061;transport=tls>" 600 200 expires= || { echo "registration over TLS failed"; return 1; }
  if [ "$3" = none ]; then
    # Something listens where the connection came from: a switch that
    # opened a new connection there would get through. It must not try.
    rm -f /tmp/accepted
    timeout 15 socat TCP-LISTEN:5077,bind=$REAL1,reuseaddr,fork SYSTEM:'echo x >> /tmp/accepted' 2>/dev/null &
    listener=$!
    sed 's/__CODE__/503/' $DIR/uac-refused.xml > /tmp/refused.xml
    sleep 0.5
    announce
    timeout 20 sipp -sf /tmp/refused.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-%u@%s" -m 1 -nostdin -timeout 15 >/dev/null 2>&1
    caller_rc=$?
    kill $listener 2>/dev/null
    echo "caller=$caller_rc new_connections=$(cat /tmp/accepted 2>/dev/null | wc -l | tr -d ' ')"
    return
  fi
  sed -e 's/__END__/wait/' -e "s/lab-nat@/$user@/g" $DIR/uas-trunk.xml > /tmp/pbx.xml
  timeout 40 sipp -sf /tmp/pbx.xml -i $REAL1 -p 5077 -m 1 -nostdin >/dev/null 2>&1 &
  pbx_pid=$!
  sleep 1
  plain_caller "$1" "$2" hangup
  caller_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  echo "caller=$caller_rc pbx=$pbx_rc"
}

# reoffer_trunk <call id prefix> <number>
# The PBX "lab-nat" registers from behind address translation and gets a
# call (8988…); the caller puts it on hold: the request must reach the PBX
# where its REGISTER came from. The caller hangs up.
reoffer_trunk() {
  register lab-nat $PASSWORD 10.0.0.5:5060 '<sip:lab-nat@10.0.0.5:5060>' 3600 200 expires= || { echo "registration failed"; return 1; }
  sed "s/__END__/reoffer/" $DIR/uas-trunk.xml > /tmp/pbx.xml
  timeout 40 sipp -sf /tmp/pbx.xml -i $REAL1 -p 5077 -m 1 -nostdin >/dev/null 2>&1 &
  pbx_pid=$!
  sed 's/__PLAN__/one/; s/__PAUSE__/300/; s/__AFTER__/hangup/' $DIR/uac-reinvite.xml > /tmp/caller.xml
  sleep 1
  announce
  timeout 30 sipp -sf /tmp/caller.xml $SWITCH -i 172.30.0.21 -s "$2" -cid_str "$1-%u@%s" -d 1000 -m 1 -nostdin -timeout 25 >/dev/null 2>&1
  caller_rc=$?
  wait $pbx_pid
  pbx_rc=$?
  echo "caller=$caller_rc pbx=$pbx_rc"
}

"$@"
