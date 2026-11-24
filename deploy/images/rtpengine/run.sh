#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Writes the configuration from the template and the environment, then
# starts rtpengine in the foreground.
set -eu
: "${RTP_PORT_MIN:=30000}" "${RTP_PORT_MAX:=39999}" "${RTP_THREADS:=4}"
# -1 keeps packets in user space. Kernel forwarding (table 0 and up) needs
# the xt_RTPENGINE module on the host and a privileged container.
: "${RTP_KERNEL_TABLE:=-1}"
# Seconds without media before a call is ended; longer when it is on hold.
: "${RTP_TIMEOUT:=60}" "${RTP_HOLD_TIMEOUT:=3600}"
# Where the SIP core takes the order to end a dead call (its management
# interface over XML-RPC, on its metrics listener). It has no default: the
# relay cannot know the SIP core's address, and without it dead calls would
# stay up.
if [ -z "${RTP_TEARDOWN_URL:-}" ]; then
  echo "rtpengine-run: RTP_TEARDOWN_URL is not set (e.g. http://127.0.0.1:8888/RPC2)" >&2
  exit 1
fi
for value in "$RTP_TIMEOUT" "$RTP_HOLD_TIMEOUT"; do
  case $value in
    ''|*[!0-9]*|0) echo "rtpengine-run: RTP_TIMEOUT and RTP_HOLD_TIMEOUT are whole seconds, at least 1" >&2; exit 1 ;;
  esac
done
# The metrics listener follows the control socket's address unless told otherwise.
: "${RTP_METRICS:=${RTP_CONTROL%:*}:2225}"
export RTP_METRICS
export RTP_PORT_MIN RTP_PORT_MAX RTP_THREADS RTP_KERNEL_TABLE RTP_TIMEOUT RTP_HOLD_TIMEOUT RTP_TEARDOWN_URL
render-config /etc/rtpengine/rtpengine.conf /run/rtpengine.conf
exec rtpengine --foreground --log-stderr --config-file=/run/rtpengine.conf
