#!/bin/sh
# Runs once, when the Postgres data volume is first created. Gives every service its own database and a role
# that can use only that database: no tables or credentials are shared between services.
# The password is for local development only (see LOONG_DEV_DB_PASSWORD in docker-compose.yml).
set -eu

for service in console backoffice driver optimizer; do
  psql -v ON_ERROR_STOP=1 -v password="$LOONG_DEV_DB_PASSWORD" --username "$POSTGRES_USER" --dbname postgres <<SQL
CREATE ROLE $service LOGIN PASSWORD :'password';
CREATE DATABASE $service OWNER $service;
-- new databases are connectable by PUBLIC; only the owning role may connect
REVOKE ALL ON DATABASE $service FROM PUBLIC;
SQL
done
