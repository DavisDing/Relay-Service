#!/bin/sh
# Check the companion packaging without starting a database or generating secrets.
set -eu
[ "$#" -ge 1 ] && [ "$#" -le 3 ] || { echo "usage: $0 IMAGE [VERSION] [REVISION]" >&2; exit 2; }
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo 'NOT_EXECUTED: Docker is unavailable.' >&2
  exit 77
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_tmp=$(mktemp -d)
task_container=
cleanup() {
  if [ -n "$task_container" ]; then docker rm -f "$task_container" >/dev/null 2>&1 || true; fi
  rm -rf "$task_tmp"
}
trap cleanup EXIT HUP INT TERM
docker image inspect "$1" > "$task_tmp/inspect.json"
task_container=$(docker create "$1")
docker export "$task_container" > "$task_tmp/rootfs.tar"
python3 - "$task_tmp" "$task_root" "${2:-}" "${3:-}" <<'PY'
import json
from pathlib import Path
import sys
import tarfile
folder, root = map(Path, sys.argv[1:3])
config = json.loads((folder / 'inspect.json').read_text())[0]['Config']
assert config['Entrypoint'] == ['/usr/local/bin/relay-postgres-entrypoint.sh']
assert config['Cmd'] == ['postgres']
assert config['Healthcheck']['Test'][0] == 'CMD-SHELL'
for key, expected in zip(('version', 'revision'), sys.argv[3:]):
    if expected:
        assert config['Labels']['org.opencontainers.image.' + key] == expected
with tarfile.open(folder / 'rootfs.tar') as archive:
    members = {m.name.lstrip('./'): m for m in archive.getmembers()}
    for target, source, executable in (
        ('usr/local/bin/relay-postgres-entrypoint.sh', 'deploy/nas/start-postgres.sh', True),
        ('docker-entrypoint-initdb.d/001-roles.sh', 'deploy/nas/init-db/001-roles.sh', False)):
        member = members[target]
        assert member.isfile()
        assert bool(member.mode & 0o111) == executable
        assert archive.extractfile(member).read() == (root / source).read_bytes()
print('PASS: companion entrypoint, health check, identity and packaged initialization sources')
PY
docker run --rm --network none --read-only --user postgres --entrypoint postgres "$1" --version
echo 'NOT_EXECUTED: database initialization, persistence, permissions and NAS acceptance.'
