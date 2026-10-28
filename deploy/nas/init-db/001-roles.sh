#!/bin/sh
set -eu
POSTGRES_USER=${POSTGRES_USER:-postgres}
POSTGRES_DB=${POSTGRES_DB:-postgres}
: "${RELAY_APP_PASSWORD:?set RELAY_APP_PASSWORD}"
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 \
  --set app_password="$RELAY_APP_PASSWORD" <<'SQL'
CREATE ROLE relay_app LOGIN PASSWORD :'app_password';
CREATE DATABASE relay OWNER relay_app;
REVOKE CONNECT ON DATABASE relay FROM PUBLIC;
GRANT CONNECT ON DATABASE relay TO relay_app;
SQL
psql --username "$POSTGRES_USER" --dbname relay --set ON_ERROR_STOP=1 <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO relay_app;
SQL
