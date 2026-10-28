"""Exercise the real NAS secret wrapper without Docker or a database.

The official PostgreSQL entrypoint is replaced by a recorder, and chown by an
argument recorder so these tests never change ownership outside their temp dir.
This verifies secret persistence/failure behavior, not PostgreSQL acceptance.
"""
import hashlib
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'deploy/nas/start-postgres.sh'
NAMES = ('POSTGRES_SUPERUSER_PASSWORD', 'RELAY_DB_PASSWORD', 'RELAY_MIGRATOR_PASSWORD',
         'RELAY_APP_PASSWORD', 'RELAY_ENCRYPTION_KEY', 'RELAY_INITIAL_PASSWORD')


class NASStartupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='relay-nas-startup-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = self.root / 'postgres'
        self.data.mkdir()
        self.secrets = self.root / 'secrets'
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.marker = self.root / 'entrypoint-called'
        entrypoint = self.bin / 'docker-entrypoint.sh'
        entrypoint.write_text('''#!/bin/sh
set -eu
test "$1" = postgres
test -n "$POSTGRES_PASSWORD"
test -n "$RELAY_DB_PASSWORD"
test -n "$RELAY_MIGRATOR_PASSWORD"
test -n "$RELAY_APP_PASSWORD"
printf called > "$RELAY_TEST_MARKER"
''')
        entrypoint.chmod(0o755)
        chown = self.bin / 'chown'
        chown.write_text('#!/bin/sh\ntest "$1" = 0:65532 || test "$1" = 65532:65532\n')
        chown.chmod(0o755)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('RELAY_') and k != 'POSTGRES_SUPERUSER_PASSWORD'}
        self.env.update(PATH=str(self.bin)+os.pathsep+os.environ.get('PATH', ''),
                        RELAY_SECRETS_DIR=str(self.secrets), RELAY_POSTGRES_DATA_ROOT=str(self.data),
                        RELAY_TEST_MARKER=str(self.marker))

    def run_start(self, **overrides):
        self.marker.unlink(missing_ok=True)
        return subprocess.run(['sh', str(SCRIPT), 'postgres'], env=dict(self.env, **overrides),
                              capture_output=True, text=True)

    def snapshot(self):
        return {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                for p in (self.secrets / 'current').iterdir() if p.is_file()}

    def test_compose_embedded_entrypoint_matches_sources_and_runs(self):
        compose = (ROOT / 'deploy/nas/docker-compose.yaml').read_text()
        generated = compose.split('        # BEGIN GENERATED POSTGRES STARTUP\n')[1].split('        # END GENERATED POSTGRES STARTUP\n')[0]
        script = '\n'.join((line[8:] if line else '').replace('$$', '$') for line in generated.splitlines()) + '\n'
        roles = (ROOT / 'deploy/nas/init-db/001-roles.sh').read_text()
        expected = "cat > /docker-entrypoint-initdb.d/001-roles.sh <<'RELAY_INIT_ROLES'\n" + roles + 'RELAY_INIT_ROLES\n' + SCRIPT.read_text()
        self.assertEqual(script, expected)
        init_dir = self.root / 'init'
        init_dir.mkdir()
        script = script.replace('/docker-entrypoint-initdb.d/001-roles.sh', str(init_dir / '001-roles.sh'))
        result = subprocess.run(['sh', '-ec', script, 'relay-postgres', 'postgres'], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((init_dir / '001-roles.sh').read_text(), roles)
        self.assertTrue(self.marker.exists())

    def test_fresh_install_generates_private_distinct_secrets_and_restart_reuses_them(self):
        result = self.run_start()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.marker.exists())
        current = self.secrets / 'current'
        self.assertEqual(stat.S_IMODE(current.stat().st_mode), 0o710)
        values = []
        for name in NAMES:
            path = current / name
            value = path.read_text()
            self.assertRegex(value, r'^[0-9a-f]{64}$')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertNotIn(value, result.stdout + result.stderr)
            values.append(value)
        self.assertEqual(len(set(values)), len(NAMES))
        original = self.snapshot()
        (self.data / 'PG_VERSION').write_text('18')
        result = self.run_start(RELAY_ENCRYPTION_KEY='ff'*32)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(original, self.snapshot())
        self.assertTrue(self.marker.exists())

    def test_missing_secrets_with_existing_database_fail_without_generating_replacements(self):
        (self.data / '18/docker').mkdir(parents=True)
        (self.data / '18/docker/PG_VERSION').write_text('18')
        result = self.run_start()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.marker.exists())
        self.assertFalse((self.secrets / 'current').exists())
        self.assertIn('refusing to regenerate', result.stderr)

    def test_incomplete_or_inconsistent_secrets_fail_without_repairing_them(self):
        self.assertEqual(self.run_start().returncode, 0)
        current = self.secrets / 'current'
        (current / 'RELAY_APP_PASSWORD').write_text('ef'*32)
        original = self.snapshot()
        result = self.run_start()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.marker.exists())
        self.assertEqual(original, self.snapshot())
        (current / 'RELAY_ENCRYPTION_KEY').unlink()
        original = self.snapshot()
        self.assertNotEqual(self.run_start().returncode, 0)
        self.assertEqual(original, self.snapshot())

    def test_manual_install_import_requires_all_original_secrets_and_preserves_key(self):
        (self.data / 'PG_VERSION').write_text('18')
        values = {name: format(i+10, '02x')*32 for i, name in enumerate(NAMES)}
        values.pop('RELAY_INITIAL_PASSWORD')  # Newly generated only; existing account stays intact.
        result = self.run_start(**values)
        self.assertEqual(result.returncode, 0, result.stderr)
        for name, value in values.items():
            self.assertEqual((self.secrets / 'current' / name).read_text(), value)
        for value in values.values():
            self.assertNotIn(value, result.stdout + result.stderr)

    def test_invalid_manual_key_is_rejected_without_publishing_partial_secrets(self):
        result = self.run_start(RELAY_ENCRYPTION_KEY='do-not-expose-this-secret-value-at-all')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.secrets / 'current').exists())
        self.assertFalse(self.marker.exists())
        self.assertNotIn('do-not-expose', result.stderr)


if __name__ == '__main__':
    unittest.main()
