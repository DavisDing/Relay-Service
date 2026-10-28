#!/bin/sh
# Generate fresh secrets without sourcing an environment template or printing them.
set -eu
if [ "$#" -gt 2 ]; then
  echo "Usage: sh generate-env.sh [TARGET_ENV [TEMPLATE_ENV]]" >&2
  exit 2
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
target=${1:-.env}
template=${2:-"$script_dir/.env.example"}
if [ -e "$target" ] || [ -L "$target" ]; then
  echo "Refusing to overwrite existing environment file: $target" >&2
  exit 1
fi
if [ ! -f "$template" ]; then
  echo "Environment template is missing: $template" >&2
  exit 1
fi
image_count=$(awk '/^RELAY_IMAGE=/{count++} END {print count+0}' "$template")
if [ "$image_count" != 1 ]; then
  echo "Template must contain exactly one RELAY_IMAGE" >&2
  exit 1
fi
for secret_name in POSTGRES_SUPERUSER_PASSWORD RELAY_APP_PASSWORD RELAY_BOOTSTRAP_TOKEN RELAY_ENCRYPTION_KEY; do
  secret_count=$(awk -v key="$secret_name" 'index($0,key "=")==1 {count++} END {print count+0}' "$template")
  if [ "$secret_count" != 1 ]; then
    echo "Template must contain exactly one $secret_name" >&2
    exit 1
  fi
done
image=$(sed -n 's/^RELAY_IMAGE=//p' "$template")
# The source template permits an override; a versioned bundle keeps its digest.
case "$image" in
  ghcr.io/davisding/relay-service:latest) image=${RELAY_IMAGE:-$image} ;;
  *replace-with-release-digest) image=${RELAY_IMAGE:-} ;;
esac
case "$image" in
  ghcr.io/davisding/relay-service@sha256:*)
    digest=${image#ghcr.io/davisding/relay-service@sha256:}
    case "$digest" in *[!0-9a-f]*|'') echo "Release image digest is invalid" >&2; exit 1 ;; esac
    if [ "${#digest}" != 64 ]; then echo "Release image digest must have 64 hex characters" >&2; exit 1; fi
    ;;
  ghcr.io/davisding/relay-service:latest|relay-service:local) ;;
  *) echo "Use Relay's latest image or a published digest; source builds may set RELAY_IMAGE=relay-service:local" >&2; exit 1 ;;
esac
command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required to generate random deployment secrets" >&2
  exit 1
}
umask 077
temporary=$(mktemp "${target}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    RELAY_IMAGE=*) printf 'RELAY_IMAGE=%s\n' "$image" ;;
    POSTGRES_SUPERUSER_PASSWORD=*|RELAY_APP_PASSWORD=*|RELAY_BOOTSTRAP_TOKEN=*|RELAY_ENCRYPTION_KEY=*)
      name=${line%%=*}
      value=$(openssl rand -hex 32)
      printf '%s=%s\n' "$name" "$value"
      ;;
    *) printf '%s\n' "$line" ;;
  esac
done < "$template" > "$temporary"
# A hard link also rejects an intervening destination, including dangling symlinks.
ln "$temporary" "$target" || exit 1
echo "Environment file created: $target (mode 600)"
