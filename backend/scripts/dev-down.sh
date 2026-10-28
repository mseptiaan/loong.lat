#!/bin/sh
# Stops the local infrastructure. Data survives; pass -v to delete the volumes as well (fresh databases and
# topics next time).
#
#   scripts/dev-down.sh
#   scripts/dev-down.sh -v
set -eu

cd "$(dirname "$0")/.."
docker compose -f deploy/docker-compose.yml down "$@"
