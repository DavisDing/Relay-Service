#!/usr/bin/env python3
"""Embed reviewed startup scripts into a self-contained NAS Compose file.

Run after editing deploy/nas/start-postgres.sh or init-db/001-roles.sh.
--check verifies the committed Compose without writing files or using Docker.
"""
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
NAS = ROOT / 'deploy/nas'
path = NAS / 'docker-compose.yaml'
text = path.read_text()
start = '        # BEGIN GENERATED POSTGRES STARTUP\n'
end = '        # END GENERATED POSTGRES STARTUP\n'
if text.count(start) != 1 or text.count(end) != 1:
    sys.exit('Compose must contain one generated PostgreSQL startup block')
roles = (NAS / 'init-db/001-roles.sh').read_text()
startup = (NAS / 'start-postgres.sh').read_text()
script = "cat > /docker-entrypoint-initdb.d/001-roles.sh <<'RELAY_INIT_ROLES'\n" + roles + 'RELAY_INIT_ROLES\n' + startup
# Docker Compose interpolation must leave every shell dollar untouched.
block = start + ''.join(('        ' + line.replace('$', '$$') if line else '') + '\n' for line in script.splitlines()) + end
prefix, rest = text.split(start)
_, suffix = rest.split(end)
rendered = prefix + block + suffix
if '--check' in sys.argv:
    if rendered != text:
        sys.exit('Compose startup block is stale; run python3 scripts/render-nas-compose.py')
else:
    path.write_text(rendered)
