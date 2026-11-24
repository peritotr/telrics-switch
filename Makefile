# SPDX-License-Identifier: AGPL-3.0-only
COMPOSE := docker compose -f deploy/compose/docker-compose.yml
CPS ?= 50
CC ?= 1000
DURATION ?= 60

.PHONY: lab-build lab-up lab-down lab-logs test ci ci-go ci-web test-api test-openapi bench-snapshot test-engine test-routing test-journal test-reports test-archive test-mail seed-user lab-seed api-run seed panel-install panel-dev panel-build test-call load prod-smoke test-nft

PG := postgres://softswitch:lab-only@127.0.0.1:56432

# The release the binaries and the panel report (pkg/version, ADR-0051):
# the tag of the checkout, or its commit, "-dirty" with uncommitted changes.
# The compose files pass it to the image builds; deploy/prod/lib.sh does the
# same for installations.
ifndef SOFTSWITCH_VERSION
SOFTSWITCH_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
export SOFTSWITCH_VERSION
LDFLAGS := -X softswitch/core/pkg/version.Version=$(SOFTSWITCH_VERSION)

lab-build:
	./deploy/compose/certs.sh
	$(COMPOSE) --profile tools build

lab-up:
	./deploy/compose/certs.sh
	$(COMPOSE) --profile tools build sipp-uac
	$(COMPOSE) up -d --build

lab-down:
	$(COMPOSE) down -v

lab-logs:
	$(COMPOSE) logs --tail=100 opensips rtpengine route-engine rater sipp-uas

# Client, vendors and routes the SIP lab calls through; route-engine picks
# them up on its next reload.
lab-seed:
	$(COMPOSE) exec -T postgres psql -U softswitch -d softswitch -v ON_ERROR_STOP=1 -q -f - < test/seed/lab-routing.sql

test:
	go test ./...

# What the public repository's CI runs (.github/workflows/ci.yml), none of
# it needing the lab. ci-web reinstalls web/node_modules from the lock file.
ci: ci-go ci-web

ci-go:
	@out=$$(gofmt -l cmd pkg db); if [ -n "$$out" ]; then echo "gofmt would change:"; echo "$$out"; exit 1; fi
	go vet ./...
	go build ./...
	go test ./...

ci-web:
	cd web && npm ci && npx tsc --noEmit -p . && npm run check-i18n && npm run build

# API integration tests against the lab PostgreSQL (a separate database that
# the test wipes on every run).
test-api:
	$(COMPOSE) exec -T postgres createdb -U softswitch softswitch_test 2>/dev/null || true
	TEST_DATABASE_URL=$(PG)/softswitch_test go test -p 1 -count=1 -v ./pkg/api ./pkg/store

# The API's description (GET /v1/openapi.json), built against the test
# database (wiped, as by test-api) and linted by Redocly with redocly.yaml.
# Needs the lab PostgreSQL and npx.
REDOCLY := @redocly/cli@2.76.0
test-openapi:
	$(COMPOSE) exec -T postgres createdb -U softswitch softswitch_test 2>/dev/null || true
	@doc="$$(mktemp)"; \
	TEST_DATABASE_URL=$(PG)/softswitch_test OPENAPI_OUT="$$doc" go test -count=1 -v -run '^TestOpenAPIDocument$$' ./pkg/api && \
	npx --yes $(REDOCLY) lint --config redocly.yaml "$$doc"; status=$$?; rm -f "$$doc"; exit $$status

# T-30: how long a snapshot load takes and how much memory it needs with a
# tariff of 5 million rates, and what reloading it costs calls being decided
# meanwhile. The data set (about 2 GB) is built once in its own database,
# softswitch_t30, and kept; drop it with
# `$(COMPOSE) exec -T postgres dropdb -U softswitch softswitch_t30`.
bench-snapshot:
	$(COMPOSE) exec -T postgres createdb -U softswitch softswitch_t30 2>/dev/null || true
	SNAPSHOT_DATABASE_URL=$(PG)/softswitch_t30 go test -count=1 -v -timeout 60m -run '^TestSnapshotLoadSize$$' ./pkg/store
	SNAPSHOT_DATABASE_URL=$(PG)/softswitch_t30 TEST_REDIS_URL=redis://127.0.0.1:56379/15 go test -count=1 -v -timeout 20m -run '^TestReloadUnderLoad$$' ./pkg/engine

# route-engine tests against the lab Redis (database 15, wiped on every run).
test-engine:
	TEST_REDIS_URL=redis://127.0.0.1:56379/15 go test -count=1 -v ./pkg/engine

# Routing scenarios with real calls: failover, refusals, limits, cut-off.
test-routing:
	./test/sipp/run.sh

# No call record lost when the engine dies with NATS down; a record for calls
# whose end never came (ADR-0029). Stops NATS, kills the engine and the SIP
# core, about six minutes: run it alone.
test-journal:
	./test/sipp/journal.sh

# Creates the lab's panel user from test/seed/dev.env on a running API.
seed-user:
	@set -a; . test/seed/dev.env; set +a; \
	curl -fsS -o /dev/null -w "panel user: HTTP %{http_code}\n" -X POST "$$API_URL/v1/users" \
	  -H "Authorization: Bearer $$BOOTSTRAP_API_KEY" -H "Content-Type: application/json" \
	  -d "{\"email\":\"$$PANEL_USER_EMAIL\",\"name\":\"Lab Admin\",\"role\":\"admin\",\"password\":\"$$PANEL_USER_PASSWORD\"}"

# E-mail end to end through the lab's mail catcher. Needs the lab,
# `make api-run` and `make seed-user`.
test-mail:
	set -a; . test/seed/dev.env; set +a; python3 test/mail/reset.py

# Needs the lab and `make api-run`.
test-reports:
	./test/load/compare-reports.sh

# Archiving of old call records, and the API serving the archived ones
# (single record, list, CSV) as before, against the test database (make
# test-api creates it) and a ClickHouse database of its own, emptied each time.
test-archive:
	$(COMPOSE) exec -T clickhouse clickhouse-client --user softswitch --password lab-only -q "create database if not exists softswitch_test"
	TEST_DATABASE_URL=$(PG)/softswitch_test TEST_CLICKHOUSE_URL=http://softswitch:lab-only@127.0.0.1:58123/softswitch_test go test -count=1 -v -run TestArchive ./pkg/rater

# The engine's token is the lab's (deploy/compose/lab.env).
api-run:
	DATABASE_URL=$(PG)/softswitch ENGINE_URL=http://127.0.0.1:58090 ENGINE_TOKEN=$$(sed -n 's/^ENGINE_TOKEN=//p' deploy/compose/lab.env) CLICKHOUSE_URL=http://softswitch:lab-only@127.0.0.1:58123/softswitch SMTP_URL=smtp://127.0.0.1:58026 MAIL_FROM="Telrics Switch <no-reply@lab.example.test>" PANEL_URL=http://127.0.0.1:58081 BOOTSTRAP_API_KEY=$${BOOTSTRAP_API_KEY:?set BOOTSTRAP_API_KEY} go run -ldflags "$(LDFLAGS)" ./cmd/api

# Demo data for the panel. Start the API with the lab key first:
#   set -a; . test/seed/dev.env; set +a; make api-run
seed:
	set -a; . test/seed/dev.env; set +a; python3 test/seed/seed.py

panel-install:
	npm --prefix web install

# Panel on 127.0.0.1:58081, forwarding API calls to 127.0.0.1:58080.
panel-dev:
	npm --prefix web run dev

# Builds web/dist; the API serves it when WEB_DIR=web/dist is set.
panel-build:
	VITE_VERSION=$(SOFTSWITCH_VERSION) npm --prefix web run build

# One end-to-end call through OpenSIPS, route-engine and rtpengine.
test-call: lab-seed
	./test/load/run.sh 1 1 1

# Steady state of CC concurrent calls at CPS for DURATION seconds of arrivals.
load: lab-seed
	./test/load/run.sh $(CPS) $(CC) $(DURATION)

# Tries the single-server installation (deploy/prod) end to end on a private
# Docker network: install, call, crash, backup and restore, upgrade.
prod-smoke:
	./deploy/prod/smoke.sh

# The example network-layer rules (deploy/prod/nftables): nft -c in both
# modes and known-sources.sh against a stub API, in a throwaway container
# from a local Debian image (the lab's rtpengine image by default; it must
# exist: make lab-build). Needs Docker and network access to Debian's
# mirrors (nftables is installed in the container). The container's own
# network namespace is used; the host's rules are not touched.
NFT_IMAGE ?= softswitch-lab-rtpengine:latest
test-nft:
	docker run --rm --name softswitch-nft-check --cap-add NET_ADMIN -v "$(CURDIR):/src:ro" --entrypoint sh $(NFT_IMAGE) /src/test/nft/check.sh
