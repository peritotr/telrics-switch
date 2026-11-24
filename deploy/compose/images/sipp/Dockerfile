# SPDX-License-Identifier: AGPL-3.0-only
# SIPp built from source: the distribution package comes without TLS.
FROM debian:bookworm-slim AS build
ARG SIPP_VERSION=v3.7.3
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake git ca-certificates libssl-dev libpcap-dev libncurses-dev \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 -b ${SIPP_VERSION} https://github.com/SIPp/sipp /src
WORKDIR /src
# The project builds with warnings as errors, which this compiler trips over.
RUN sed -i 's/-Werror//g' CMakeLists.txt \
 && cmake . -DUSE_SSL=1 -DUSE_PCAP=1 -DUSE_SCTP=0 -DUSE_GSL=0 -DCMAKE_BUILD_TYPE=Release \
 && make -j"$(nproc)" sipp

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libssl3 libpcap0.8 libncurses6 libncursesw6 iproute2 socat openssl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/sipp /usr/local/bin/sipp
WORKDIR /work
ENTRYPOINT ["sipp"]
