# SPDX-License-Identifier: AGPL-3.0-only
# The management API with the panel built in. Build context is the
# repository root; the ignore file next to this one lets web/ through.
FROM node:22-bookworm-slim AS panel
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
# The release the panel shows beside the server's (see deploy/images/go).
ARG VERSION=dev
RUN VITE_VERSION=${VERSION} npm run build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY pkg pkg
COPY db db
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X softswitch/core/pkg/version.Version=${VERSION}" -o /out/service ./cmd/api

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/service /service
COPY --from=panel /web/dist /panel
ENV WEB_DIR=/panel
ENTRYPOINT ["/service"]
