#!/bin/sh
# Starts the local infrastructure, waits until it is healthy, creates the Kafka topics, and prints the
# TEST_DATABASE_URL that enables the database tests.
#
#   scripts/dev-up.sh                                   # postgres + redpanda
#   scripts/dev-up.sh postgres redpanda redpanda-console
#
# Then, for the tests that need a real Postgres:
#   export TEST_DATABASE_URL=...   (printed below)
#   go test -race ./pkg/...
set -eu

cd "$(dirname "$0")/.."
compose="docker compose -f deploy/docker-compose.yml"

if [ "$#" -eq 0 ]; then
  set -- postgres redpanda
fi

# --wait returns once the services' healthchecks pass
$compose up -d --wait "$@"

# the topics need the broker, so only when it was started
for service in "$@"; do
  if [ "$service" = redpanda ]; then
    KAFKA_BROKERS="${KAFKA_BROKERS:-localhost:19092}" go run ./cmd/topics
    break
  fi
done

password="${LOONG_DEV_DB_PASSWORD:-loong_dev}"
cat <<MSG

Infrastructure is up.
  Postgres   127.0.0.1:55432   databases: console backoffice driver optimizer
  Kafka      localhost:19092   (Console: http://127.0.0.1:18080)

Run the database tests with:
  export TEST_DATABASE_URL='postgres://console:${password}@127.0.0.1:55432/console?sslmode=disable'
MSG
