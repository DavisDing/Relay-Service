#!/bin/sh
set -eu
: "${RELAY_DB_PASSWORD:?set RELAY_DB_PASSWORD}"
: "${RELAY_MIGRATOR_PASSWORD:?set RELAY_MIGRATOR_PASSWORD}"
: "${RELAY_APP_PASSWORD:?set RELAY_APP_PASSWORD}"
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 \
  --set db_password="$RELAY_DB_PASSWORD" \
  --set migrator_password="$RELAY_MIGRATOR_PASSWORD" \
  --set app_password="$RELAY_APP_PASSWORD" <<'SQL'
CREATE ROLE relay_owner LOGIN PASSWORD :'db_password';
CREATE ROLE relay_migrator LOGIN PASSWORD :'migrator_password';
CREATE ROLE relay_app LOGIN PASSWORD :'app_password';
CREATE DATABASE relay OWNER relay_owner;
REVOKE CONNECT ON DATABASE relay FROM PUBLIC;
GRANT CONNECT ON DATABASE relay TO relay_owner, relay_migrator, relay_app;
SQL
psql --username "$POSTGRES_USER" --dbname relay --set ON_ERROR_STOP=1 <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO relay_migrator, relay_app;
GRANT CREATE ON SCHEMA public TO relay_migrator;
ALTER DEFAULT PRIVILEGES FOR ROLE relay_migrator IN SCHEMA public GRANT SELECT ON TABLES TO relay_app;
SQL
