# SPDX-License-Identifier: AGPL-3.0-only
# Builds one Go service of this repository (build arg CMD names it).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY pkg pkg
COPY db db
ARG CMD
# The release the binary reports (pkg/version); the deploy scripts pass what
# `git describe` says of the checkout. Last, so that a new version only
# repeats the final build step.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X softswitch/core/pkg/version.Version=${VERSION}" -o /out/service ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/service /service
ENTRYPOINT ["/service"]
