# SPDX-License-Identifier: AGPL-3.0-only
# rtpengine from Debian's package. Runs in user space unless RTP_KERNEL_TABLE
# says otherwise; kernel forwarding needs the xt_RTPENGINE module on the host.
# Build context is the repository root.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends rtpengine-daemon \
    && rm -rf /var/lib/apt/lists/*
COPY deploy/images/render-config.sh /usr/local/bin/render-config
COPY deploy/images/rtpengine/run.sh /usr/local/bin/rtpengine-run
COPY media/rtpengine/rtpengine.conf /etc/rtpengine/rtpengine.conf
ENTRYPOINT ["/usr/local/bin/rtpengine-run"]
