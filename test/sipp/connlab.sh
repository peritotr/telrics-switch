#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Runs inside the lab's caller container, started by test/sipp/run.sh: plays
# the vendors of the scenarios about a connection's settings, on lab
# addresses of their own, and the caller that reaches them through the
# switch. Each vendor takes one call and looks at what it is sent.
#
#   connlab.sh <function> [arguments]
#
# Addresses (nobody else in the lab uses them):
#   172.30.0.60  rings and never answers
#   172.30.0.61  refuses with a code
#   172.30.0.62  checks the request it gets (uas-check.xml)
#   172.30.0.63  asks callers to sign in (MD5, SHA-256, SHA-512-256; qop)
#   172.30.0.64  the vendor the engine asks whether it is there
#   172.30.0.65  answers a caller that never acknowledges the answer
#   172.30.0.66  answers a caller that acknowledges it late
#   172.30.0.67  takes and makes new offers within the call (re-INVITE)
#   172.30.0.69  takes a new offer on a call of at most five seconds
#   172.30.0.73  a call whose media stops (the switch must end it)
#   172.30.0.74  a call with media all along (it must not)
#   172.30.0.75  a call held with sendonly, then silent
#   172.30.0.76  a call held with inactive, then silent
#   172.30.0.77  fax (T.38) both ways and key presses as INFO
#   172.30.0.78  a fax call with fax datagrams only, past the media timeout
#   172.30.0.79  183 with 100rel (PRACK), then a transfer asked each way
#   172.30.0.85  183 with early media, no 100rel
#   172.30.0.86  183 with early media, then silent ringing past the media timeout
#   172.30.0.88  a call of 130 s whose money held is extended twice (T-03)
#   172.30.0.89  a call ended when its client's money runs out
#   172.30.0.93  a vendor the switch registers with (T-14)
#   172.30.0.100 speaks PCMU only; the switch translates (transcoding, T-09)
#   172.30.0.101 speaks PCMU only; translated when the caller's trunk allows it
#   172.30.0.102 makes the offer in its 200 to an INVITE without one (T-43)
#   172.30.0.103 new INVITEs without an offer within the call, both ways
#   172.30.0.104 an offer in the 200 the caller's ACK does not answer
#   172.30.0.94  an offer in the 200 while the media relay is stopped
#   172.30.0.105 rings three times (180, 183, 180), then refuses (T-46)
#   172.30.0.106 rings twice (180, 183), then answers
set -u
SWITCH=172.30.0.10:5060
DIR=/work/test/sipp
PIDS=""

# Each run is a new container with a new hardware address, and the switch
# and the engine may still hold the previous one for these lab addresses.
# A vendor does not speak first, so each address sends one datagram to a
# port nobody reads on both: asking for their hardware address tells them
# who has this one now.
listen_on() {
  ip addr add "$1/24" dev eth0 2>/dev/null
  ip neigh flush all 2>/dev/null
  for peer in 172.30.0.10 172.30.0.40; do
    echo | socat -u - "UDP:$peer:9,bind=$1" 2>/dev/null
  done
}

# vendor <address> <scenario file> [sipp arguments]: one call, in the background.
vendor() {
  addr=$1 file=$2; shift 2
  listen_on "$addr"
  timeout "${VENDOR_TIMEOUT:-45}" sipp -sf "$file" -i "$addr" -p 5060 -m 1 -nostdin "$@" >/dev/null 2>&1 &
  PIDS="$PIDS $!"
}

# checker <address> <sed expression>: a vendor that checks its request. The
# expression fills in the placeholders that matter; the rest accept anything.
checker() {
  sed -e "$2" -e 's/__LINE__/./; s/__FROM__/./; s/__PAI__/./; s/__PAI_YES__/l3/; s/__PAI_NO__/l3/; s/__SDPNOT__/no such text in an offer/; s/__SDP__/m=audio/; s/__ANSWER_PT__/8/g; s/__ANSWER_CODEC__/PCMA/' \
    $DIR/uas-check.xml > "/tmp/check-$1.xml"
  vendor "$1" "/tmp/check-$1.xml"
}

refuser() { # refuser <address> <code>
  sed "s/__CODE__/$2/" $DIR/uas-reject.xml > "/tmp/reject-$1.xml"
  vendor "$1" "/tmp/reject-$1.xml"
}

# call <call id prefix> <number> [sed expression on the caller's scenario] [sipp arguments]
# Places the call once the vendors listen, then prints how each side fared:
# "caller=0 vendors=0 0" is a call every side saw through to its end.
call() {
  id=$1 number=$2 edit=${3:-}; shift; shift; [ $# -gt 0 ] && shift
  sed "$edit" $DIR/${CALLER:-uac.xml} > /tmp/caller.xml
  sleep 1
  timeout 45 sipp -sf /tmp/caller.xml $SWITCH -i 172.30.0.21 -s "$number" -cid_str "$id-%u@%s" -d "${TALK:-800}" -m 1 -nostdin "$@" >/dev/null 2>&1
  out="caller=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# A caller that offers G.711 both ways (µ-law first) and key presses.
BOTH='s/RTP\/AVP 8/RTP\/AVP 0 8 101/; s/a=rtpmap:8 PCMA\/8000/a=rtpmap:0 PCMU\/8000\n      a=rtpmap:8 PCMA\/8000\n      a=rtpmap:101 telephone-event\/8000/'
# A caller with a number, a name and an asserted identity; with PRIVACY set
# it also asks for the identity to be withheld (Privacy: id).
caller_is() { printf '%s' "s/sipp <sip:sipp@\\([^>]*\\)>;tag/sipp <sip:$1@\\1>;tag/; s/Max-Forwards: 70/Max-Forwards: 70\\n      P-Asserted-Identity: <sip:$1@lab.test>\\n      Remote-Party-ID: <sip:$1@lab.test>;party=calling${PRIVACY:+\\n      Privacy: id}/"; }

# ring <id> <number>: the first vendor rings for ever; the switch must move on.
ring() {
  vendor 172.30.0.60 $DIR/uas-ring.xml
  call "$1" "$2"
}

# refused <id> <number> <code> [caller scenario]: the first vendor refuses with the code.
refused() {
  refuser 172.30.0.61 "$3"
  CALLER=${4:-uac.xml} call "$1" "$2" "${5:-}"
}

# number <id> <number> <what the request line must start with>
number() {
  checker 172.30.0.62 "s/__LINE__/^INVITE sip:$3@/"
  call "$1" "$2"
}

# shown <id> <number> <caller> <From must match> <any|must|none> [P-Asserted-Identity must match]
# The caller sends its number in From and in an asserted identity.
shown() {
  yes=l3 no=l3
  [ "$5" = must ] && no=refuse
  [ "$5" = none ] && yes=refuse
  checker 172.30.0.62 "s/__FROM__/$4/; s/__PAI__/${6:-.}/; s/__PAI_YES__/$yes/; s/__PAI_NO__/$no/"
  call "$1" "$2" "$(caller_is "$3")"
}

# private <id> <number> <caller> <From must match> <any|must|none> [P-Asserted-Identity must match]
# As shown, with a caller that asks for privacy (Privacy: id).
private() {
  PRIVACY=1
  shown "$@"
}

# shown_after <id> <number> <caller> <From must match at the second vendor>
# The first vendor refuses (486); the second must see the caller as its own
# connection shows it, not as the first one's did.
shown_after() {
  refuser 172.30.0.61 486
  checker 172.30.0.62 "s/__FROM__/$4/"
  call "$1" "$2" "$(caller_is "$3")"
}

# codecs <id> <number> <offer must match> <offer must not match> <answer payload type> <answer codec>
# The caller offers PCMU, PCMA and key presses, in that order.
codecs() {
  checker 172.30.0.62 "s/__SDP__/$3/; s/__SDPNOT__/$4/; s/__ANSWER_PT__/$5/g; s/__ANSWER_CODEC__/$6/"
  call "$1" "$2" "$BOTH"
}

# signin <id> <number> <401|407> <user> <password the vendor expects> [algorithms] [qop]
# The vendor asks with one challenge header per algorithm, in the order it
# prefers them ("SHA-256 MD5"; MD5 alone by default), each offering the qop
# given ("auth", "auth-int", "auth,auth-int"; none by default). The answer
# must use the first algorithm, the nonce it was given and, with qop, the
# first count (nc=00000001) and a cnonce. With 401 SIPp checks the password
# itself; with 407 only that the answer names the user and its realm (SIPp
# can do no more there). In both cases the answer is then worked out again
# here from the user name and password; "digest=" says how that went: ok,
# bad, none (no answer came) or auth-int (the body is not at hand here, so
# only SIPp's check counts).
signin() {
  ask=Proxy-Authenticate answer=Proxy-Authorization
  verify="<ereg regexp=\"username=.$4., realm=.vendor.lab.test.\" search_in=\"hdr\" header=\"Proxy-Authorization:\" assign_to=\"signed\" \/>"
  if [ "$3" = 401 ]; then
    ask=WWW-Authenticate answer=Authorization
    verify="<verifyauth assign_to=\"signed\" username=\"$4\" password=\"$5\" \/>"
  fi
  algs=${6:-MD5} qop=${7:-}
  alg=${algs%% *}
  challenge=""
  for a in $algs; do
    challenge="$challenge${challenge:+\\n      }$ask: Digest realm=\"vendor.lab.test\", nonce=\"5f2a[call_number]c1d9e8\"${qop:+, qop=\"$qop\"}, algorithm=$a"
  done
  case ",$qop," in
    *,auth,*) asked="qop=auth, nc=00000001, cnonce=.[^,]+., " ;;
    *auth-int*) asked="qop=auth-int, nc=00000001, cnonce=.[^,]+., " ;;
    *) asked="" ;;
  esac
  shape="nonce=.5f2a[0-9]+c1d9e8., uri=.[^,]+., ${asked}response=.[0-9a-f]+., algorithm=$alg"
  sed -e "s/__CODE__/$3/; s/__CHALLENGE__/$challenge/; s/__VERIFY__/$verify/; s/__SHAPE__/$shape/; s/__ANSWER__/$answer/" $DIR/uas-auth.xml > /tmp/auth.xml
  rm -f /tmp/auth-answer.log
  vendor 172.30.0.63 /tmp/auth.xml -trace_logs -log_file /tmp/auth-answer.log
  # Not inside "$(…)": the vendor must be this shell's child to be waited for.
  call "$1" "$2" "" -timeout 40 > /tmp/signin.out
  echo "$(cat /tmp/signin.out) digest=$(digest "$4" "$5" "$alg" "$asked")"
}

# digest <user> <password> <algorithm> <qop asked>: works the answer the
# vendor logged out again (RFC 7616 §3.4.1, with H the algorithm's hash).
digest() {
  got=$(grep '^answer: ' /tmp/auth-answer.log 2>/dev/null | tail -n 1 | tr -d '\r')
  [ "${got#answer: }" != "" ] || { echo none; return; }
  case $4 in *auth-int*) echo auth-int; return ;; esac
  case $3 in
    MD5) sum=md5sum ;;
    SHA-256) sum=sha256sum ;;
    SHA-512-256) sum="openssl dgst -sha512-256 -r" ;;
    *) echo bad; return ;;
  esac
  ha1=$(hashed "$1:$(field realm):$2")
  ha2=$(hashed "INVITE:$(field uri)")
  if [ -n "$4" ]; then
    want=$(hashed "$ha1:$(field nonce):$(field nc):$(field cnonce):$(field qop):$ha2")
  else
    want=$(hashed "$ha1:$(field nonce):$ha2")
  fi
  if [ "$(field algorithm)" = "$3" ] && [ "$(field response)" = "$want" ]; then echo ok; else echo bad; fi
}
hashed() { printf '%s' "$1" | $sum | cut -d' ' -f1; }
field() { printf '%s' "$got" | sed -n "s/.*[ ,]$1=\"\{0,1\}\([^\",]*\).*/\1/p"; }

# alive <id> <number> <seconds to wait before calling>
# The vendor at 172.30.0.64 comes to life: it answers the engine's
# questions (OPTIONS) and takes calls. The engine must have noticed by the
# time the call is placed. The vendor has no end of its own; only the
# caller's result is printed.
alive() {
  listen_on 172.30.0.64
  sipp -sf $DIR/uas-alive.xml -i 172.30.0.64 -p 5060 -nostdin >/dev/null 2>&1 &
  alive_pid=$!
  sleep "$3"
  call "$1" "$2"
  kill $alive_pid 2>/dev/null
}

# registrar <attempts> <user> <password> <domain> <granted s> <taken> [<call id> <number> <after s>]
# The vendor at .93 that the switch registers with (uas-registrar.xml). It
# ends after <attempts> registration attempts, and the call when there is
# one: a registration taken <taken> times (its first REGISTER and then its
# refreshes) is one attempt, and so is one turned away. With a call id, the
# lab client calls <number> <after> seconds after the vendor starts.
# Prints "registrar=<exit>" and, with a call, " caller=<exit>".
registrar() {
  calls=$1
  [ $# -ge 9 ] && calls=$(($1 + 1))
  sed "s/__USER__/$2/g; s/__PASS__/$3/g; s/__DOMAIN__/$4/g; s/__EXPIRES__/$5/; s/__COUNT__/$6/" $DIR/uas-registrar.xml > /tmp/registrar.xml
  listen_on 172.30.0.93
  timeout "${VENDOR_TIMEOUT:-75}" sipp -sf /tmp/registrar.xml -i 172.30.0.93 -p 5060 -m "$calls" -nostdin -timeout 70 >/dev/null 2>&1 &
  reg=$!
  out=""
  if [ $# -ge 9 ]; then
    sleep "$9"
    timeout 30 sipp -sf $DIR/uac.xml $SWITCH -i 172.30.0.21 -s "$8" -cid_str "$7-%u@%s" -d 800 -m 1 -nostdin -timeout 25 >/dev/null 2>&1
    out=" caller=$?"
  fi
  wait $reg
  echo "registrar=$?$out"
}

# give_up <id> <number>: the vendor at .60 rings; the caller gives up a
# second after the ringing reaches it. The vendor must get the CANCEL.
give_up() {
  vendor 172.30.0.60 $DIR/uas-ring.xml
  CALLER=uac-cancel.xml call "$1" "$2" "s/__PROVISIONAL__/180/; s/__OPTIONAL_100__/true/; s/__WAIT__/1000/"
}

# ack_wait <call id prefix, never acknowledged> <call id prefix, acknowledged late>
# Two answered calls at once, each to a vendor of its own (8720 at .65, 8721
# at .66). The first caller never acknowledges the answer: the switch must
# end its call 32 seconds after the answer with a BYE to the caller and to
# the vendor. The second acknowledges three seconds after the answer and
# talks 33 seconds more: it must not be ended and hangs up itself. Each side's
# result is printed; "never=0 late=0 vendors=0 0" is the expected outcome.
ack_wait() {
  vendor 172.30.0.65 $DIR/uas-ackwait.xml
  vendor 172.30.0.66 $DIR/uas-ackwait.xml
  sed 's/__ACK_MS__/3000/; s/__TALK_MS__/33000/' $DIR/uac-lateack.xml > /tmp/lateack.xml
  sleep 1
  timeout 50 sipp -sf $DIR/uac-noack.xml $SWITCH -i 172.30.0.21 -p 5071 -s 87205551112233 -cid_str "$1-%u@%s" -m 1 -nostdin -timeout 45 >/dev/null 2>&1 &
  never=$!
  timeout 50 sipp -sf /tmp/lateack.xml $SWITCH -i 172.30.0.21 -p 5072 -s 87215551112233 -cid_str "$2-%u@%s" -m 1 -nostdin -timeout 45 >/dev/null 2>&1 &
  late=$!
  wait $never; out="never=$?"
  wait $late; out="$out late=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# media_timeout <dead> <talk> <sendonly> <inactive> <silent>: five calls at
# once, each given by its call id prefix. The first four go to a vendor of
# their own that echoes the audio it gets (8725–8728 at .73–.76;
# uac-media.xml and uas-media.xml). Every one has three seconds of audio,
# then:
#   dead      nothing more; the switch must end it on both sides once the
#             media relay has heard nothing for its timeout (60 s);
#   talk      audio for 70 seconds; the caller hangs up after 65;
#   sendonly, inactive
#             held at 3.5 s, with nothing sent by either side; the caller
#             hangs up 65 seconds later: a held call has the longer timeout.
# The fifth (8733 at .86, whose connection may ring 90 s) rings with a 183
# with early media and no media either way, the vendor meaning to answer
# after 70 s: the media relay's timeout gives it up while it rings
# (ADR-0038), the vendor is sent CANCEL and answers 487, and so does the
# caller (uac-early.xml, plan "silent"). The capture files are written by
# run.sh. "dead=0 talk=0 sendonly=0 inactive=0 silent=0 vendors=0 0 0 0 0"
# is the expected outcome.
media_timeout() {
  VENDOR_TIMEOUT=110
  for addr in 172.30.0.73 172.30.0.74 172.30.0.75 172.30.0.76; do
    vendor $addr $DIR/uas-media.xml -rtp_echo
  done
  # Being cancelled is what this vendor is here for: its CANCEL path ends
  # well (the "late" plan alone takes it as a failure).
  sed 's/__PLAN__/late/g; s/__WAIT__/70000/; s/<recv request="ACK" next="cancelled" \/>/<recv request="ACK" next="done" \/>/' $DIR/uas-early.xml > /tmp/media-silent-v.xml
  vendor 172.30.0.86 /tmp/media-silent-v.xml
  silent=$5
  sleep 1
  sed 's/__PLAN__/silent/g; s/__RING_MS__/100000/g' $DIR/uac-early.xml > /tmp/media-silent.xml
  timeout 110 sipp -sf /tmp/media-silent.xml $SWITCH -i 172.30.0.21 -p 5087 -mp 10188 -s 87335551112233 -cid_str "$silent-%u@%s" \
    -m 1 -nostdin -timeout 105 >/dev/null 2>&1 &
  pid_silent=$!
  out=""
  port=5073
  for spec in "dead $1 87255551112233 dead short 80000 sendrecv" \
              "talk $2 87265551112233 talk long 65000 sendrecv" \
              "sendonly $3 87275551112233 hold short 65000 sendonly" \
              "inactive $4 87285551112233 hold short 65000 inactive"; do
    set -- $spec
    sed "s/__PLAN__/$4/; s|__RTP__|/work/out/media-$5.pcap|; s/__WAIT__/$6/g; s/__DIRECTION__/$7/" $DIR/uac-media.xml > "/tmp/media-$1.xml"
    timeout 100 sipp -sf "/tmp/media-$1.xml" $SWITCH -i 172.30.0.21 -p $port -mp $((port * 2)) -s "$3" -cid_str "$2-%u@%s" \
      -m 1 -nostdin -timeout 95 >/dev/null 2>&1 &
    eval "pid_$1=\$!"
    port=$((port + 1))
  done
  for name in dead talk sendonly inactive silent; do
    eval "wait \$pid_$name"
    out="$out$name=$? "
  done
  out="${out}vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# reinviter <address> <plan>: a vendor for new offers within the call
# (uas-reinvite.xml); reinvite_caller <plan> [pause ms] [hangup|wait]: the
# sed expression for the caller's side (uac-reinvite.xml).
reinviter() {
  sed "s/__PLAN__/$2/" $DIR/uas-reinvite.xml > "/tmp/reinvite-$1.xml"
  vendor "$1" "/tmp/reinvite-$1.xml"
}
reinvite_caller() { printf '%s' "s/__PLAN__/$1/; s/__PAUSE__/${2:-0}/; s/__AFTER__/${3:-hangup}/"; }

# reoffer <id> <number>: new offers both ways within one call (8722, at
# .67; the connection lets only PCMA through): hold and resume from the
# caller, an offer the vendor refuses, the vendor making the call inactive
# with the connection's codecs only, and an offer the caller refuses. The
# call goes on through all of it and the caller hangs up.
reoffer() {
  reinviter 172.30.0.67 full
  CALLER=uac-reinvite.xml call "$1" "$2" "$(reinvite_caller full)"
}

# reoffer_short <id> <number>: a call of at most five seconds (8724, at
# .69); the caller holds it after two. The switch must still end it five
# seconds after the answer, not five after the new offer.
reoffer_short() {
  reinviter 172.30.0.69 take
  CALLER=uac-reinvite.xml call "$1" "$2" "$(reinvite_caller one 2000 wait)"
}

# fax <full id> <keep id>: two calls at once (ADR-0031; uac-fax.xml and
# uas-fax.xml). The first (8729 at .77; the connection lets only PCMA
# through) turns into a fax (T.38) from the caller, carries a key press as
# INFO each way, goes back to audio from the vendor, into a fax again from
# the vendor and back to audio from the caller; then the caller hangs up.
# The second (8730 at .78) turns into a fax half a second after the answer
# and carries nothing but fax datagrams (the capture file run.sh writes) for
# 70 seconds: longer than the media relay's timeout (ADR-0027), which must
# not end it. "full=0 keep=0 vendors=0 0" is the expected outcome.
fax() {
  VENDOR_TIMEOUT=110
  sed 's/__PLAN__/full/; s/__WAIT__/15000/' $DIR/uas-fax.xml > /tmp/fax-full-v.xml
  sed 's/__PLAN__/take/; s/__WAIT__/90000/' $DIR/uas-fax.xml > /tmp/fax-keep-v.xml
  vendor 172.30.0.77 /tmp/fax-full-v.xml
  vendor 172.30.0.78 /tmp/fax-keep-v.xml
  sed 's/__PLAN__/full/; s/__START__/300/; s|__FAX__|/work/out/fax-udptl.pcap|' $DIR/uac-fax.xml > /tmp/fax-full.xml
  sed 's/__PLAN__/keep/; s/__START__/500/; s|__FAX__|/work/out/fax-udptl.pcap|' $DIR/uac-fax.xml > /tmp/fax-keep.xml
  sleep 1
  timeout 40 sipp -sf /tmp/fax-full.xml $SWITCH -i 172.30.0.21 -p 5083 -mp 10166 -s 87295551112233 -cid_str "$1-%u@%s" \
    -d 800 -m 1 -nostdin -timeout 35 >/dev/null 2>&1 &
  full=$!
  timeout 100 sipp -sf /tmp/fax-keep.xml $SWITCH -i 172.30.0.21 -p 5084 -mp 10170 -s 87305551112233 -cid_str "$2-%u@%s" \
    -d 70000 -m 1 -nostdin -timeout 95 >/dev/null 2>&1 &
  keep=$!
  wait $full; out="full=$?"
  wait $keep; out="$out keep=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# early <reliable id> <early id>: two calls at once (ADR-0028; uac-early.xml
# and uas-early.xml). The first (8731 at .79) gets a 183 that requires
# PRACK (100rel); the PRACK and its answer must pass, then each side asks
# for a transfer (REFER) and is refused with 403, and the call goes on until
# the caller hangs up. The second (8732 at .85) gets a 183 with early media
# and no 100rel. Both 183s must carry the media relay's address. "reliable=0
# early=0 vendors=0 0" is the expected outcome.
early() {
  sed 's/__PLAN__/reliable/g; s/__WAIT__/0/' $DIR/uas-early.xml > /tmp/early-r-v.xml
  sed 's/__PLAN__/early/g; s/__WAIT__/300/' $DIR/uas-early.xml > /tmp/early-e-v.xml
  vendor 172.30.0.79 /tmp/early-r-v.xml
  vendor 172.30.0.85 /tmp/early-e-v.xml
  sed 's/__PLAN__/reliable/g; s/__RING_MS__/10000/g' $DIR/uac-early.xml > /tmp/early-r.xml
  sed 's/__PLAN__/early/g; s/__RING_MS__/10000/g' $DIR/uac-early.xml > /tmp/early-e.xml
  sleep 1
  timeout 30 sipp -sf /tmp/early-r.xml $SWITCH -i 172.30.0.21 -p 5085 -mp 10180 -s 87315551112233 -cid_str "$1-%u@%s" \
    -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  reliable=$!
  timeout 30 sipp -sf /tmp/early-e.xml $SWITCH -i 172.30.0.21 -p 5086 -mp 10184 -s 87325551112233 -cid_str "$2-%u@%s" \
    -d 800 -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  early=$!
  wait $reliable; out="reliable=$?"
  wait $early; out="$out early=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# progress <id> <number>: each attempt reports progress more than once
# (uas-progress.xml, T-46). The first vendor (8745 at .105) sends 180, 183
# and 180, then refuses with 503; the next (.106) sends 180 and 183, then
# answers. The engine must hear "ringing" once per attempt.
progress() {
  sed 's/__END__/refuse/; s/__CODE__/503/' $DIR/uas-progress.xml > /tmp/progress-1.xml
  sed 's/__END__/answer/; s/__CODE__/503/' $DIR/uas-progress.xml > /tmp/progress-2.xml
  vendor 172.30.0.105 /tmp/progress-1.xml
  vendor 172.30.0.106 /tmp/progress-2.xml
  call "$1" "$2"
}

# early_long <id> <ring ms>: a 183 with early media, then no media either way
# while the vendor (8733 at .86; its connection may ring 90 s) waits the
# given time before it answers; the caller talks 800 ms and hangs up. Prints
# how each side fared.
early_long() {
  VENDOR_TIMEOUT=110
  sed "s/__PLAN__/late/g; s/__WAIT__/$2/" $DIR/uas-early.xml > /tmp/early-l-v.xml
  vendor 172.30.0.86 /tmp/early-l-v.xml
  sed 's/__PLAN__/early/g; s/__RING_MS__/100000/g' $DIR/uac-early.xml > /tmp/early-l.xml
  sleep 1
  timeout 110 sipp -sf /tmp/early-l.xml $SWITCH -i 172.30.0.21 -p 5087 -mp 10188 -s 87335551112233 -cid_str "$1-%u@%s" \
    -d 800 -m 1 -nostdin -timeout 105 >/dev/null 2>&1
  out="caller=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# transcode <id> <number> <full|plain> [vendor address]: a caller that
# speaks PCMA only (uac-transcode.xml) calls; the vendor at the given
# address speaks PCMU only and echoes the audio (uas-transcode.xml). With
# "full" the vendor also makes a new offer within the call, which must
# reach the caller in PCMA. Without an address no vendor of this script's
# is started: the call is expected elsewhere. The capture file is written
# by run.sh. "caller=0 vendors=0" is the expected outcome.
transcode() {
  if [ -n "${4:-}" ]; then
    plan=take
    [ "$3" = full ] && plan=full
    sed "s/__PLAN__/$plan/" $DIR/uas-transcode.xml > "/tmp/transcode-$4.xml"
    vendor "$4" "/tmp/transcode-$4.xml" -rtp_echo
  fi
  sed "s/__PLAN__/$3/; s|__RTP__|/work/out/media-short.pcap|; s/__WAIT__/3500/" $DIR/uac-transcode.xml > /tmp/transcode.xml
  sleep 1
  timeout 40 sipp -sf /tmp/transcode.xml $SWITCH -i 172.30.0.21 -p 5090 -mp 10190 -s "$2" -cid_str "$1-%u@%s" \
    -m 1 -nostdin -timeout 35 >/dev/null 2>&1
  out="caller=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

# late <id> <number> <plan> <vendor address> [vendor plan] [ack]: offers made
# in an answer (T-43, ADR-0043). The vendor (uas-late.xml, plan "take" and
# ACK "answer" unless given) echoes the audio it gets; the caller is
# uac-relate.xml for the plan "relate", else uac-late.xml with the plan
# (plain, noanswer, refused). Calls with audio ("plain", "relate") play
# three seconds of it and hang up after four; meanwhile the media relay is
# asked what it has seen of the call (its "query" command). Prints how
# each side fared, then the relay's reply, if any, on a line of its own.
# "caller=0 vendors=0" is the expected outcome.
late() {
  sed "s/__PLAN__/${5:-take}/; s/__ACK__/${6:-answer}/" $DIR/uas-late.xml > "/tmp/late-$4.xml"
  vendor "$4" "/tmp/late-$4.xml" -rtp_echo
  scenario=uac-late.xml
  [ "$3" = relate ] && scenario=uac-relate.xml
  sed "s/__PLAN__/$3/; s|__RTP__|/work/out/media-short.pcap|; s/__WAIT__/4000/" $DIR/$scenario > /tmp/late.xml
  sleep 1
  timeout 30 sipp -sf /tmp/late.xml $SWITCH -i 172.30.0.21 -p 5091 -mp 10192 -s "$2" -cid_str "$1-%u@%s" \
    -m 1 -nostdin -timeout 25 >/dev/null 2>&1 &
  caller=$!
  relay=""
  if [ "$3" = plain ] || [ "$3" = relate ]; then
    # Two seconds or so into the audio, a second before the caller hangs
    # up; the new offers of "relate" come first.
    if [ "$3" = relate ]; then sleep 4.5; else sleep 3.5; fi
    id="$1-1@172.30.0.21"
    # The relay gives its reply to a cookie again, for a while, to the same
    # cookie: each question has one of its own.
    cookie="q$(date +%s)$1"
    relay=$(printf '%s d7:call-id%d:%s7:command5:querye' "$cookie" ${#id} "$id" | socat -t2 - UDP:172.30.0.11:2223)
  fi
  wait $caller
  out="caller=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
  [ -n "$relay" ] && echo "$relay"
  return 0
}

# reservation <long> <short>: two calls at once, each given by its call id
# prefix, each to a vendor of its own that echoes the audio (uac-media.xml,
# uas-media.xml), with audio all along so the media relay never ends them:
#   long   the lab client calls 8735 (.88) and hangs up after 130 s;
#   short  the client whose money runs out at the second step (tech prefix
#          554) calls 8736 (.89) and waits for the switch to end the call.
# The capture file is written by run.sh. "long=0 short=0 vendors=0 0" is
# the expected outcome.
reservation() {
  VENDOR_TIMEOUT=160
  for addr in 172.30.0.88 172.30.0.89; do
    vendor $addr $DIR/uas-media.xml -rtp_echo
  done
  sleep 1
  sed "s/__PLAN__/talk/; s|__RTP__|/work/out/media-reserve.pcap|; s/__WAIT__/130000/g" $DIR/uac-media.xml > /tmp/reserve-long.xml
  timeout 155 sipp -sf /tmp/reserve-long.xml $SWITCH -i 172.30.0.21 -p 5088 -mp 10176 -s 87355551112233 -cid_str "$1-%u@%s" \
    -m 1 -nostdin -timeout 150 >/dev/null 2>&1 &
  long=$!
  sed "s/__PLAN__/dead/; s|__RTP__|/work/out/media-reserve.pcap|; s/__WAIT__/120000/g" $DIR/uac-media.xml > /tmp/reserve-short.xml
  timeout 140 sipp -sf /tmp/reserve-short.xml $SWITCH -i 172.30.0.21 -p 5089 -mp 10178 -s 55487365551112233 -cid_str "$2-%u@%s" \
    -m 1 -nostdin -timeout 135 >/dev/null 2>&1 &
  short=$!
  wait $long; out="long=$?"
  wait $short; out="$out short=$? vendors="
  for pid in $PIDS; do
    wait "$pid"
    out="$out$? "
  done
  echo "${out% }"
}

"$@"
