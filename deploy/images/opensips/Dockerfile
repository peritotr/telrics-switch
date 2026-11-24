# SPDX-License-Identifier: AGPL-3.0-only
# OpenSIPS built from source: apt.opensips.org ships no arm64 packages.
FROM debian:bookworm-slim AS build
ARG OPENSIPS_BRANCH=3.6
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential bison flex git ca-certificates pkg-config \
      libncurses-dev libcurl4-openssl-dev libjson-c-dev libssl-dev libmicrohttpd-dev libxml2-dev \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 -b ${OPENSIPS_BRANCH} https://github.com/OpenSIPS/opensips /src
WORKDIR /src
RUN make -j"$(nproc)" include_modules="rest_client json tls_openssl tls_mgm proto_tls httpd prometheus mi_xmlrpc_ng" prefix=/usr/local all \
 && make include_modules="rest_client json tls_openssl tls_mgm proto_tls httpd prometheus mi_xmlrpc_ng" prefix=/usr/local install \
 && ln -s "$(dirname "$(find /usr/local -name tm.so)")" /usr/local/opensips-modules

FROM debian:bookworm-slim
# curl and jq: registrants.sh asks the engine which vendors to register with.
RUN apt-get update && apt-get install -y --no-install-recommends \
      libncurses6 libcurl4 libjson-c5 libssl3 libmicrohttpd12 libxml2 ca-certificates openssl curl jq \
    && rm -rf /var/lib/apt/lists/* && mkdir -p /run/opensips
COPY --from=build /usr/local /usr/local
# Build context is the repository root.
COPY deploy/images/render-config.sh /usr/local/bin/render-config
COPY deploy/images/opensips/run.sh /usr/local/bin/opensips-run
COPY deploy/images/opensips/tls-table.sh /usr/local/bin/opensips-tls-table
COPY deploy/images/opensips/registrants.sh /usr/local/bin/opensips-registrants
COPY sip/opensips/opensips.cfg /etc/opensips/opensips.cfg
ENTRYPOINT ["/usr/local/bin/opensips-run"]
