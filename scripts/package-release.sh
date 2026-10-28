#!/bin/sh
# Produce a source-free NAS deployment bundle from an allowlist of repository files.
set -eu
if [ "$#" -ne 4 ]; then
  echo "Usage: sh scripts/package-release.sh TAG IMAGE_REF POSTGRES_IMAGE_REF OUTPUT_DIR" >&2
  exit 2
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command -v python3 >/dev/null 2>&1 || { echo "python3 is required to package releases" >&2; exit 1; }
exec python3 - "$script_dir/.." "$1" "$2" "$3" "$4" <<'PY'
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

root = Path(sys.argv[1]).resolve()
tag, image, postgres_image = sys.argv[2:5]
output = Path(sys.argv[5]).absolute()
number = r'(?:0|[1-9][0-9]*)'
identifier = r'(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
tag_pattern = rf'v{number}\.{number}\.{number}(?:-{identifier}(?:\.{identifier})*)?'
if not re.fullmatch(tag_pattern, tag, flags=re.ASCII):
    sys.exit('TAG must be vX.Y.Z, optionally followed by a SemVer prerelease')
if not re.fullmatch(r'ghcr\.io/davisding/relay-service@sha256:[0-9a-f]{64}', image, flags=re.ASCII):
    sys.exit('IMAGE_REF must be the Relay GHCR repository with an immutable sha256 digest')
if not re.fullmatch(r'ghcr\.io/davisding/relay-service-postgres@sha256:[0-9a-f]{64}', postgres_image, flags=re.ASCII):
    sys.exit('POSTGRES_IMAGE_REF must be the Relay PostgreSQL GHCR repository with an immutable sha256 digest')
if os.path.lexists(output):
    sys.exit('Refusing to overwrite an existing output path')

# Do not walk the checkout: actual .env, dumps, keys and source can never enter the bundle.
allowlist = {
    'docker-compose.yaml': ('deploy/nas/docker-compose.yaml', 0o644),
    'docker-compose.settings.example.yaml': ('deploy/nas/docker-compose.settings.example.yaml', 0o644),
    'docker-compose.network.example.yaml': ('deploy/nas/docker-compose.network.example.yaml', 0o644),
    'generate-env.sh': ('deploy/nas/generate-env.sh', 0o755),
    '.env.example': ('deploy/nas/.env.example', 0o644),
}
payload = {}
for name, (relative, mode) in allowlist.items():
    source = root / relative
    if source.is_symlink() or not source.is_file():
        sys.exit(f'Required release input must be a regular file: {relative}')
    payload[name] = (source.read_bytes(), mode)

compose = payload['docker-compose.yaml'][0].decode('utf-8')
if re.search(r'^\s*build\s*:', compose, re.MULTILINE):
    sys.exit('Default release Compose must not require source builds')
# Versioned assets pin both matching images, including the optional settings file.
for source_image, pinned in (
        ('ghcr.io/davisding/relay-service:latest', image),
        ('ghcr.io/davisding/relay-service-postgres:latest', postgres_image)):
    if compose.count(source_image) != 1:
        sys.exit('Source Compose must contain exactly one default reference per image')
    compose = compose.replace(source_image, pinned)
    settings, mode = payload['docker-compose.settings.example.yaml']
    payload['docker-compose.settings.example.yaml'] = (settings.replace(source_image.encode(), pinned.encode()), mode)
payload['docker-compose.yaml'] = (compose.encode(), 0o644)

template = payload['.env.example'][0].decode('utf-8')
if len(re.findall(r'^RELAY_IMAGE=.*$', template, re.MULTILINE)) != 1:
    sys.exit('Environment template must contain exactly one RELAY_IMAGE')
template = re.sub(r'^RELAY_IMAGE=.*$', f'RELAY_IMAGE={image}', template, flags=re.MULTILINE)
if len(re.findall(r'^RELAY_POSTGRES_IMAGE=.*$', template, re.MULTILINE)) != 1:
    sys.exit('Environment template must contain exactly one RELAY_POSTGRES_IMAGE')
template = re.sub(r'^RELAY_POSTGRES_IMAGE=.*$', f'RELAY_POSTGRES_IMAGE={postgres_image}', template, flags=re.MULTILINE)
secret_names = ('POSTGRES_SUPERUSER_PASSWORD', 'RELAY_DB_PASSWORD', 'RELAY_MIGRATOR_PASSWORD', 'RELAY_APP_PASSWORD', 'RELAY_BOOTSTRAP_TOKEN', 'RELAY_ENCRYPTION_KEY')
for name in secret_names:
    values = re.findall(rf'^{name}=(.*)$', template, re.MULTILINE)
    if len(values) != 1 or (values[0] and not values[0].startswith('replace-with-')):
        sys.exit(f'Release template must contain a placeholder, never a secret: {name}')
payload['.env.example'] = (template.encode(), 0o644)
readme = f'''Relay Service {tag} — NAS deployment bundle

Relay image: {image}
PostgreSQL companion image: {postgres_image}

Import docker-compose.yaml into NAS Docker Projects or run:
  docker compose -f docker-compose.yaml up -d
No .env, external network, host IP, local build or manual secret generation is needed.
Initialization scripts, runtime defaults and health checks are included in the images.
Compose creates the project network. PostgreSQL automatically generates persistent
secrets and initializes roles on an empty volume. Relay checks the schema and
automatically applies only pending SQL files before serving HTTP.

Open http://<NAS address>:8787. Get the temporary admin password from Relay logs;
first login requires a permanent username/password before using business APIs.
The application invalidates all temporary sessions after this change.

Only add environment settings under the service when you want to override defaults.
Optional settings and existing-network examples are separate files; they are not
loaded automatically. To use .env overrides, explicitly merge the settings example:
  docker compose -f docker-compose.yaml -f docker-compose.settings.example.yaml up -d
For a different host port, edit the main ports mapping directly.
Never regenerate secrets for an existing database. Preserve and protect both
relay-postgres and relay-secrets volumes; never use down -v to upgrade or debug.
The companion image extends official postgres:latest with Relay initialization.
A major PostgreSQL upgrade still requires a database migration procedure.
The development schema targets empty installations; legacy-schema backfill and
NAS/database acceptance are separate from release packaging.
'''
payload['README.txt'] = (readme.encode(), 0o644)
try:
    git_commit = subprocess.run(['git', '-C', str(root), 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True).stdout.strip()
except (OSError, subprocess.CalledProcessError):
    sys.exit('A Git checkout with HEAD is required to record the release source revision')
if not re.fullmatch(r'[0-9a-f]{40,64}', git_commit):
    sys.exit('Could not resolve release source revision')

output.parent.mkdir(parents=True, exist_ok=True)
stage = Path(tempfile.mkdtemp(prefix='.relay-release-', dir=output.parent))
reserved = False
try:
    archive_name = f'relay-service-{tag}-nas.tar.gz'
    with (stage / archive_name).open('wb') as raw:
        with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as zipped:
            with tarfile.open(fileobj=zipped, mode='w', format=tarfile.USTAR_FORMAT) as archive:
                for name in sorted(payload):
                    data, mode = payload[name]
                    member = tarfile.TarInfo(name)
                    member.size = len(data)
                    member.mode = mode
                    member.mtime = member.uid = member.gid = 0
                    member.uname = member.gname = ''
                    archive.addfile(member, io.BytesIO(data))
    (stage / 'docker-compose.yaml').write_text(compose)
    (stage / f'relay-service-{tag}.env.example').write_text(template)
    metadata = {'format_version': 2, 'version': tag, 'image': image, 'git_commit': git_commit, 'bundle': archive_name, 'postgres_image': postgres_image, 'postgres_base_image': 'postgres:latest'}
    (stage / 'release-metadata.json').write_text(json.dumps(metadata, indent=2, sort_keys=True) + '\n')
    sums = []
    for file in sorted(stage.iterdir()):
        sums.append(f'{hashlib.sha256(file.read_bytes()).hexdigest()}  {file.name}\n')
    (stage / 'SHA256SUMS').write_text(''.join(sums))
    # Reserve a new destination atomically, then link each completed file without replacement.
    output.mkdir(mode=0o755)
    reserved = True
    for file in sorted(stage.iterdir()):
        os.link(file, output / file.name)
        os.chmod(output / file.name, 0o644)
except BaseException:
    if reserved:
        shutil.rmtree(output)
    raise
finally:
    shutil.rmtree(stage)
print(f'Release assets created: {output}')
PY
