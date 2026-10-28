"""Offline release/deployment contract checks. No Docker or database is started."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
IMAGE = 'ghcr.io/davisding/relay-service@sha256:' + 'ab' * 32
TAG = 'v0.1.0-rc.1'
INPUTS = ('deploy/nas/docker-compose.yaml', 'deploy/nas/init-db/001-roles.sh', 'deploy/nas/start-postgres.sh',
          'deploy/nas/.env.example', 'deploy/nas/generate-env.sh', 'scripts/package-release.sh')
SECRETS = ('POSTGRES_SUPERUSER_PASSWORD', 'RELAY_DB_PASSWORD', 'RELAY_MIGRATOR_PASSWORD',
           'RELAY_APP_PASSWORD', 'RELAY_BOOTSTRAP_TOKEN', 'RELAY_ENCRYPTION_KEY')


class PackageReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='relay-release-test-')
        self.addCleanup(self.temp.cleanup)
        self.folder = Path(self.temp.name)
        self.root = self.folder / 'checkout'
        for relative in INPUTS:
            dest = self.root / relative
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / relative, dest)
        # A deterministic, read-only Git metadata fixture avoids creating commits.
        self.bin = self.folder / 'bin'
        self.bin.mkdir()
        git = self.bin / 'git'
        git.write_text('#!/bin/sh\nprintf "%s\\n" "' + 'c' * 40 + '"\n')
        git.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ.get('PATH', ''))
        self.output = self.folder / 'release'

    def package(self, tag=TAG, image=IMAGE, output=None):
        return subprocess.run(['sh', str(self.root / 'scripts/package-release.sh'), tag, image,
                               str(output or self.output)], cwd=self.folder, env=self.env,
                              capture_output=True, text=True)

    def extract(self):
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        dest = self.folder / 'nas'
        dest.mkdir()
        with tarfile.open(self.output / f'relay-service-{TAG}-nas.tar.gz') as bundle:
            # The allowlisted archive contains no links or escaping member names.
            for member in bundle.getmembers():
                self.assertTrue(member.isfile())
                self.assertFalse(member.name.startswith('/'))
                self.assertNotIn('..', Path(member.name).parts)
                path = dest / member.name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(bundle.extractfile(member).read())
                path.chmod(member.mode)
        return dest

    def test_assets_checksums_and_source_free_bundle(self):
        # Files that must never be walked into the archive or release assets.
        sentinel = 'REAL-SECRET-MUST-NEVER-BE-PACKAGED-123456789'
        (self.root / 'deploy/nas/.env').write_text('RELAY_ENCRYPTION_KEY=' + sentinel)
        (self.root / 'secret-dump.sql').write_text(sentinel)
        nas = self.extract()
        expected = {f'relay-service-{TAG}-nas.tar.gz', 'docker-compose.yaml',
                    f'relay-service-{TAG}.env.example', 'release-metadata.json', 'SHA256SUMS'}
        self.assertEqual({p.name for p in self.output.iterdir()}, expected)
        sums = (self.output / 'SHA256SUMS').read_text().splitlines()
        self.assertEqual(len(sums), 4)
        for line in sums:
            digest, name = line.split('  ')
            self.assertEqual(hashlib.sha256((self.output / name).read_bytes()).hexdigest(), digest)
        self.assertEqual({str(p.relative_to(nas)) for p in nas.rglob('*') if p.is_file()},
                         {'.env.example', 'docker-compose.yaml', 'generate-env.sh',
                          'init-db/001-roles.sh', 'start-postgres.sh', 'README.txt'})
        compose = (nas / 'docker-compose.yaml').read_text()
        self.assertNotRegex(compose, r'(?m)^\s*build\s*:')
        self.assertEqual(compose.count('${RELAY_IMAGE:-' + IMAGE + '}'), 1)
        self.assertNotIn('relay-migrate:', compose)
        relay_section = compose.split('  relay:\n', 1)[1].split('  postgres:\n', 1)[0]
        self.assertIn('RELAY_DEPLOYMENT_FILE: /relay-secrets/current/deployment.env', relay_section)
        self.assertIn('relay-secrets:/relay-secrets:ro', relay_section)
        self.assertNotIn('${RELAY_APP_PASSWORD:?', relay_section)
        self.assertNotIn('command: ["migrate"]', relay_section)
        self.assertNotIn('service_completed_successfully', compose)
        self.assertNotIn('docker-compose.init.yaml', (nas / 'README.txt').read_text())
        self.assertIn('automatically applies only pending SQL files', (nas / 'README.txt').read_text())
        self.assertIn('image: postgres:latest', compose)
        self.assertIn('relay-postgres:/var/lib/postgresql', compose)
        self.assertNotIn('./init-db/001-roles.sh:', compose)
        self.assertIn('# BEGIN GENERATED POSTGRES STARTUP', compose)
        self.assertTrue((nas / 'init-db/001-roles.sh').is_file())
        self.assertEqual(stat.S_IMODE((nas / 'init-db/001-roles.sh').stat().st_mode), 0o644)
        self.assertEqual(stat.S_IMODE((nas / 'generate-env.sh').stat().st_mode), 0o755)
        self.assertIn('RELAY_IMAGE=' + IMAGE, (nas / '.env.example').read_text())
        self.assertEqual((nas / '.env.example').read_bytes(), (self.output / f'relay-service-{TAG}.env.example').read_bytes())
        for file in self.output.iterdir():
            self.assertNotIn(sentinel.encode(), file.read_bytes())
        for file in nas.rglob('*'):
            if file.is_file():
                self.assertNotIn(sentinel.encode(), file.read_bytes())
        metadata = json.loads((self.output / 'release-metadata.json').read_text())
        self.assertEqual(metadata['version'], TAG)
        self.assertEqual(metadata['image'], IMAGE)
        self.assertEqual(metadata['git_commit'], 'c' * 40)
        self.assertEqual(metadata['postgres_image'], 'postgres:latest')

    def test_invalid_tags_and_images_are_rejected_before_output(self):
        tags = ('1.2.3', 'v1.2', 'v01.2.3', 'v1.02.3', 'v1.2.3-01',
                'v1.2.3-', 'v1.2.3-rc..1', 'v1.2.3/../../x', 'v1.2.3\n',
                'v1.2.3+metadata', 'v１.2.3')
        for tag in tags:
            with self.subTest(tag=tag):
                result = self.package(tag=tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())
        images = ('ghcr.io/davisding/relay-service:latest',
                  'ghcr.io/other/relay-service@sha256:' + 'a' * 64,
                  'ghcr.io/davisding/relay-service@sha256:' + 'A' * 64,
                  'ghcr.io/davisding/relay-service@sha256:' + 'a' * 63,
                  IMAGE + '\n', IMAGE + ';echo BAD')
        for image in images:
            with self.subTest(image=image):
                result = self.package(image=image)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())

    def test_stable_and_prerelease_tags(self):
        for index, tag in enumerate(('v0.0.0', 'v1.2.3', 'v1.2.3-beta.1', 'v2.0.0-rc.0')):
            with self.subTest(tag=tag):
                result = self.package(tag=tag, output=self.folder / str(index))
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_output_refuses_existing_directory_file_and_dangling_symlink(self):
        paths = [self.folder / 'directory', self.folder / 'file', self.folder / 'symlink']
        paths[0].mkdir()
        (paths[0] / 'keep').write_text('unchanged')
        paths[1].write_text('unchanged')
        paths[2].symlink_to(self.folder / 'missing')
        for path in paths:
            with self.subTest(path=path.name):
                self.assertNotEqual(self.package(output=path).returncode, 0)
        self.assertEqual((paths[0] / 'keep').read_text(), 'unchanged')
        self.assertEqual(paths[1].read_text(), 'unchanged')
        self.assertTrue(paths[2].is_symlink())

    def test_real_secret_in_template_is_refused(self):
        template = self.root / 'deploy/nas/.env.example'
        template.write_text(template.read_text().replace('RELAY_ENCRYPTION_KEY=',
                                                         'RELAY_ENCRYPTION_KEY=' + 'de' * 32))
        result = self.package()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('never a secret', result.stderr)
        self.assertFalse(self.output.exists())

    def test_symlinked_input_is_refused(self):
        source = self.root / 'deploy/nas/.env.example'
        external = self.folder / 'external'
        source.rename(external)
        source.symlink_to(external)
        self.assertNotEqual(self.package().returncode, 0)
        self.assertFalse(self.output.exists())

    @unittest.skipUnless(shutil.which('openssl'), 'openssl required for secret generation')
    def test_bundle_generates_fresh_private_env_without_changing_published_image(self):
        nas = self.extract()
        env = dict(self.env, RELAY_IMAGE='relay-service:local')
        result = subprocess.run(['sh', str(nas / 'generate-env.sh'), '.env'], cwd=nas,
                                env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        generated = nas / '.env'
        self.assertEqual(stat.S_IMODE(generated.stat().st_mode), 0o600)
        values = dict(line.split('=', 1) for line in generated.read_text().splitlines()
                      if line and not line.startswith('#'))
        self.assertEqual(values['RELAY_IMAGE'], IMAGE)
        self.assertEqual(len({values[name] for name in SECRETS}), len(SECRETS))
        for name in SECRETS:
            self.assertRegex(values[name], r'^[0-9a-f]{64}$')
            self.assertNotIn(values[name], result.stdout + result.stderr)
        original = generated.read_bytes()
        repeat = subprocess.run(['sh', str(nas / 'generate-env.sh'), '.env'], cwd=nas,
                                env=env, capture_output=True, text=True)
        self.assertNotEqual(repeat.returncode, 0)
        self.assertEqual(generated.read_bytes(), original)

    @unittest.skipUnless(shutil.which('openssl'), 'openssl required for secret generation')
    def test_source_template_defaults_to_latest_and_supports_image_overrides(self):
        script = self.root / 'deploy/nas/generate-env.sh'
        for index, override in enumerate(('', 'relay-service:local', IMAGE)):
            with self.subTest(override=override):
                target = self.folder / f'source-{index}.env'
                result = subprocess.run(['sh', str(script), str(target)], cwd=self.folder,
                                        env=dict(self.env, RELAY_IMAGE=override), capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                expected = override or 'ghcr.io/davisding/relay-service:latest'
                self.assertIn('RELAY_IMAGE=' + expected + '\n', target.read_text())
                self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)

    @unittest.skipUnless(shutil.which('openssl'), 'openssl required for secret generation')
    def test_source_template_refuses_invalid_image_before_creating_env(self):
        script = self.root / 'deploy/nas/generate-env.sh'
        for override in ('ghcr.io/other/relay-service:latest', IMAGE + '\n',
                         'ghcr.io/davisding/relay-service@sha256:bad'):
            with self.subTest(override=override):
                target = self.folder / 'invalid.env'
                result = subprocess.run(['sh', str(script), str(target)], cwd=self.folder,
                                        env=dict(self.env, RELAY_IMAGE=override), capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(target.exists())

    def test_source_compose_defaults_relay_service_to_latest(self):
        compose = (ROOT / 'deploy/nas/docker-compose.yaml').read_text()
        self.assertEqual(compose.count('image: ${RELAY_IMAGE:-ghcr.io/davisding/relay-service:latest}'), 1)

    def test_bundle_is_reproducible(self):
        self.assertEqual(self.package().returncode, 0)
        second = self.folder / 'second'
        self.assertEqual(self.package(output=second).returncode, 0)
        for file in self.output.iterdir():
            self.assertEqual(file.read_bytes(), (second / file.name).read_bytes(), file.name)

    def test_local_build_overlay_uses_repository_root(self):
        overlay = (ROOT / 'deploy/nas/docker-compose.local-build.yaml').read_text()
        self.assertEqual(overlay.count('context: ../..'), 1)
        self.assertEqual(overlay.count('dockerfile: deploy/nas/Dockerfile'), 1)
        self.assertEqual(overlay.count('image: ${RELAY_IMAGE:-relay-service:local}'), 1)


if __name__ == '__main__':
    unittest.main()
