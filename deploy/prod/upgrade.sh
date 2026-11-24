#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Moves the installation to the version checked out in this repository:
# takes a backup, builds the images and restarts what changed. The API
# applies new schema steps as it starts. Calls in progress are dropped when
# the SIP core's image changed.
#
#   git fetch && git checkout <version>
#   ./deploy/prod/upgrade.sh
. "$(dirname "$0")/lib.sh"
need_env
# The route-engine's interface is closed with a secret (ENGINE_TOKEN). An
# installation from before that gets one here, made as install.sh makes it
# and never shown; the engine, the SIP core and the API pick it up as they
# are restarted below.
if [ -z "$(get_env ENGINE_TOKEN)" ]; then
  command -v openssl >/dev/null || die "openssl is not installed"
  set_env ENGINE_TOKEN "$(openssl rand -hex 24)"
  echo "added ENGINE_TOKEN to $ENV_FILE: the route-engine now answers only its own services"
fi
# Two-step sign-in secrets and vendors' sign-in passwords are sealed with
# SECRET_KEY. An installation from before that gets one here; secrets stored
# before a key was set stay readable.
if [ -z "$(get_env SECRET_KEY)" ]; then
  command -v openssl >/dev/null || die "openssl is not installed"
  set_env SECRET_KEY "$(openssl rand -hex 32)"
  echo "added SECRET_KEY to $ENV_FILE: two-step sign-in secrets are encrypted in the database from now on"
fi
echo "backup: $(./backup.sh)"
echo "building images"
dc build -q
# The API first: it brings the schema up to date before the others start.
# The rater with it: it must know every field of a call record before the
# engine starts sending it.
dc up -d api rater
wait_for "the API" 180 api_up
dc up -d --remove-orphans
wait_for "the API" 180 api_up
wait_for "the SIP core" 180 sip_up
echo "upgraded to $(git -C "$HERE" describe --tags --always 2>/dev/null || echo 'this checkout')"
dc ps --format 'table {{.Service}}\t{{.Status}}'
