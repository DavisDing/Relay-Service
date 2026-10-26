#!/bin/sh
# Database-free image structure and CLI checks. Never starts Docker or PostgreSQL.
set -eu

usage() {
  echo "usage: $0 IMAGE [EXPECTED_VERSION] [EXPECTED_REVISION] | --build [IMAGE]" >&2
  exit 2
}

task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_build=false
case "${1:-}" in
  --build)
    task_build=true
    shift
    task_image=${1:-relay-service:smoke}
    [ "$#" -le 1 ] || usage
    ;;
  ''|-*) usage ;;
  *)
    task_image=$1
    [ "$#" -le 3 ] || usage
    ;;
esac
task_expected_version=${2:-}
task_expected_revision=${3:-}

if ! command -v docker >/dev/null 2>&1; then
  echo "NOT_EXECUTED: Docker CLI is unavailable; no daemon was installed or started." >&2
  exit 77
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "NOT_EXECUTED: python3 is required to inspect the exported image." >&2
  exit 77
fi
if ! docker info >/dev/null 2>&1; then
  echo "NOT_EXECUTED: Docker daemon is unavailable; no daemon was started." >&2
  exit 77
fi

if [ "$task_build" = true ]; then
  docker build --file "$task_root/deploy/nas/Dockerfile" --tag "$task_image" "$task_root"
fi

task_tmp=$(mktemp -d "${TMPDIR:-/tmp}/relay-container-smoke.XXXXXX")
task_container=
cleanup() {
  if [ -n "$task_container" ]; then
    docker rm -f "$task_container" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$task_tmp"
}
trap cleanup EXIT HUP INT TERM

docker image inspect "$task_image" > "$task_tmp/inspect.json"
python3 - "$task_tmp/inspect.json" "$task_expected_version" "$task_expected_revision" <<'PY'
import json
import sys

images = json.load(open(sys.argv[1], encoding="utf-8"))
assert len(images) == 1, "expected one resolved image"
config = images[0]["Config"]
user = config.get("User", "")
assert user and user.split(":", 1)[0] not in ("0", "root"), "image must default to a non-root user"
assert config.get("Entrypoint") == ["/relay"], "image entrypoint must be /relay"
assert images[0].get("Os") == "linux", "runtime image must target Linux"
labels = config.get("Labels") or {}
for key in ("org.opencontainers.image.version", "org.opencontainers.image.revision", "org.opencontainers.image.created"):
    assert labels.get(key), f"missing OCI label {key}"
if sys.argv[2]:
    assert labels["org.opencontainers.image.version"] == sys.argv[2], "OCI version differs from expected release"
if sys.argv[3]:
    assert labels["org.opencontainers.image.revision"] == sys.argv[3], "OCI revision differs from expected commit"
print("PASS: Linux image, non-root default user, entrypoint and OCI identity")
PY

# These commands have no database environment and no network access.
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true "$task_image" version > "$task_tmp/version.txt"
python3 - "$task_tmp/version.txt" "$task_tmp/inspect.json" "$task_expected_version" "$task_expected_revision" <<'PY'
import json
import sys

value = json.load(open(sys.argv[1], encoding="utf-8"))
labels = json.load(open(sys.argv[2], encoding="utf-8"))[0]["Config"]["Labels"]
for key in ("version", "revision", "created"):
    assert isinstance(value.get(key), str) and value[key], f"version JSON missing {key}"
    assert value[key] == labels[f"org.opencontainers.image.{key}"], f"binary/OCI {key} identity differs"
if sys.argv[3]:
    assert value["version"] == sys.argv[3], "binary version differs from expected release"
if sys.argv[4]:
    assert value["revision"] == sys.argv[4], "binary revision differs from expected commit"
print("PASS: isolated version JSON matches image labels and expected release identity")
PY

task_code=0
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true "$task_image" healthcheck \
  > "$task_tmp/health.txt" 2>&1 || task_code=$?
if [ "$task_code" -ne 1 ]; then
  echo "FAIL: healthcheck without a listening service must exit 1; got $task_code" >&2
  exit 1
fi
echo "PASS: healthcheck reports failure when no service is listening"

# Export a stopped container, then inspect the archive without extracting it.
task_container=$(docker create "$task_image" version)
docker export "$task_container" > "$task_tmp/rootfs.tar"
python3 - "$task_tmp/rootfs.tar" <<'PY'
import sys
import tarfile

with tarfile.open(sys.argv[1], "r:") as archive:
    members = {m.name.removeprefix("./"): m for m in archive.getmembers()}
    relay = members.get("relay")
    assert relay and relay.isfile() and relay.mode & 0o111, "missing executable /relay"
    data = archive.extractfile(relay).read()
    assert data[:4] == b"\x7fELF", "expected a Linux ELF executable"
    cert = members.get("etc/ssl/certs/ca-certificates.crt")
    assert cert and cert.isfile(), "missing HTTPS CA bundle"
    assert b"BEGIN CERTIFICATE" in archive.extractfile(cert).read(), "invalid CA bundle"
    assert "bin/sh" not in members, "scratch runtime unexpectedly includes a shell"
    print("PASS: executable Linux binary, CA bundle and shell-free runtime")
PY

echo "PASS: database-free container smoke checks completed"
echo "NOT_EXECUTED: application startup, PostgreSQL/migrations, HTTP UI, target services and NAS acceptance"
