#!/bin/sh
# Runs inside the official PostgreSQL container, before its normal entrypoint.
# Store one complete generation atomically; never regenerate secrets for data.
set -eu
umask 077
secrets_dir=${RELAY_SECRETS_DIR:-/relay-secrets}
data_root=${RELAY_POSTGRES_DATA_ROOT:-/var/lib/postgresql}
current="$secrets_dir/current"
secret_names='POSTGRES_SUPERUSER_PASSWORD RELAY_DB_PASSWORD RELAY_MIGRATOR_PASSWORD RELAY_APP_PASSWORD RELAY_ENCRYPTION_KEY RELAY_INITIAL_PASSWORD'
if [ ! -d "$data_root" ] || [ -L "$current" ]; then
  echo 'Invalid database or deployment secrets directory.' >&2
  exit 1
fi
mkdir -p "$secrets_dir"
chmod 755 "$secrets_dir"

if [ ! -d "$current" ]; then
  # Existing manual installations can import all their original secrets. An
  # incomplete set must fail instead of silently losing database/key access.
  if [ -n "$(find "$data_root" -name PG_VERSION -type f -print -quit)" ]; then
    if [ -z "${POSTGRES_SUPERUSER_PASSWORD:-}" ] || [ -z "${RELAY_DB_PASSWORD:-}" ] ||
       [ -z "${RELAY_MIGRATOR_PASSWORD:-}" ] || [ -z "${RELAY_APP_PASSWORD:-}" ] || [ -z "${RELAY_ENCRYPTION_KEY:-}" ]; then
      echo 'Existing database has no deployment secrets. Restore the secrets volume or provide all original secrets; refusing to regenerate.' >&2
      exit 1
    fi
  fi
  stage=$(mktemp -d "$secrets_dir/.pending.XXXXXX")
  trap 'rm -rf "$stage"' EXIT HUP INT TERM
  for name in $secret_names; do
    # Only read the explicit allowlist; never eval environment or stored data.
    case "$name" in
      POSTGRES_SUPERUSER_PASSWORD) value=${POSTGRES_SUPERUSER_PASSWORD:-} ;;
      RELAY_DB_PASSWORD) value=${RELAY_DB_PASSWORD:-} ;;
      RELAY_MIGRATOR_PASSWORD) value=${RELAY_MIGRATOR_PASSWORD:-} ;;
      RELAY_APP_PASSWORD) value=${RELAY_APP_PASSWORD:-} ;;
      RELAY_ENCRYPTION_KEY) value=${RELAY_ENCRYPTION_KEY:-} ;;
      RELAY_INITIAL_PASSWORD) value=${RELAY_INITIAL_PASSWORD:-} ;;
    esac
    if [ -z "$value" ]; then value=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n'); fi
    case "$value" in
      *[!a-zA-Z0-9_-]*|'') echo "Invalid $name; use URL-safe random characters." >&2; exit 1 ;;
    esac
    if [ "${#value}" -lt 32 ]; then echo "$name must have at least 32 random characters." >&2; exit 1; fi
    if [ "$name" = RELAY_ENCRYPTION_KEY ]; then
      case "$value" in *[!a-fA-F0-9]*) echo 'Encryption key must be hexadecimal.' >&2; exit 1 ;; esac
      if [ "${#value}" != 64 ]; then echo 'Encryption key must have 64 hexadecimal characters.' >&2; exit 1; fi
    fi
    printf '%s' "$value" > "$stage/$name"
  done
  app=$(cat "$stage/RELAY_APP_PASSWORD")
  migrator=$(cat "$stage/RELAY_MIGRATOR_PASSWORD")
  {
    printf 'RELAY_DATABASE_URL=postgres://relay_app:%s@postgres:5432/relay?sslmode=disable\n' "$app"
    printf 'RELAY_MIGRATION_DATABASE_URL=postgres://relay_migrator:%s@postgres:5432/relay?sslmode=disable\n' "$migrator"
    printf 'RELAY_ENCRYPTION_KEY=%s\n' "$(cat "$stage/RELAY_ENCRYPTION_KEY")"
    printf 'RELAY_INITIAL_PASSWORD=%s\n' "$(cat "$stage/RELAY_INITIAL_PASSWORD")"
  } > "$stage/deployment.env"
  # Relay may traverse the directory and read only its configuration file.
  # Keep superuser/owner password files root-only, even on its read-only mount.
  chown 0:65532 "$stage"
  chmod 710 "$stage"
  chown 65532:65532 "$stage/deployment.env"
  # No other container writes this volume. Rename publishes the full generation.
  mv "$stage" "$current"
  trap - EXIT HUP INT TERM
fi

for name in $secret_names; do
  if [ ! -s "$current/$name" ] || [ -L "$current/$name" ]; then
    echo 'Deployment secrets are incomplete; restore the secrets volume.' >&2
    exit 1
  fi
  value=$(cat "$current/$name")
  case "$value" in *[!a-zA-Z0-9_-]*|'') echo 'Invalid stored deployment secret; restore the secrets volume.' >&2; exit 1 ;; esac
  if [ "${#value}" -lt 32 ]; then echo 'Invalid stored deployment secret length.' >&2; exit 1; fi
done
if [ ! -s "$current/deployment.env" ] || [ -L "$current/deployment.env" ]; then
  echo 'Deployment configuration is missing; restore the secrets volume.' >&2
  exit 1
fi
POSTGRES_PASSWORD=$(cat "$current/POSTGRES_SUPERUSER_PASSWORD")
RELAY_DB_PASSWORD=$(cat "$current/RELAY_DB_PASSWORD")
RELAY_MIGRATOR_PASSWORD=$(cat "$current/RELAY_MIGRATOR_PASSWORD")
RELAY_APP_PASSWORD=$(cat "$current/RELAY_APP_PASSWORD")
if ! {
  printf 'RELAY_DATABASE_URL=postgres://relay_app:%s@postgres:5432/relay?sslmode=disable\n' "$RELAY_APP_PASSWORD"
  printf 'RELAY_MIGRATION_DATABASE_URL=postgres://relay_migrator:%s@postgres:5432/relay?sslmode=disable\n' "$RELAY_MIGRATOR_PASSWORD"
  printf 'RELAY_ENCRYPTION_KEY=%s\n' "$(cat "$current/RELAY_ENCRYPTION_KEY")"
  printf 'RELAY_INITIAL_PASSWORD=%s\n' "$(cat "$current/RELAY_INITIAL_PASSWORD")"
} | cmp -s - "$current/deployment.env"; then
  echo 'Stored deployment configuration does not match its secrets; restore the secrets volume.' >&2
  exit 1
fi
export POSTGRES_PASSWORD RELAY_DB_PASSWORD RELAY_MIGRATOR_PASSWORD RELAY_APP_PASSWORD
unset POSTGRES_PASSWORD_FILE
exec docker-entrypoint.sh "$@"
