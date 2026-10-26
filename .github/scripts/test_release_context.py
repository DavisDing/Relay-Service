import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location("release_context", Path(__file__).with_name("release_context.py"))
release_context = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release_context)


class ReleaseContextTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="relay-release-context-")
        self.addCleanup(self.directory.cleanup)
        self.previous_directory = os.getcwd()
        os.chdir(self.directory.name)
        self.addCleanup(os.chdir, self.previous_directory)
        self.git("init", "--initial-branch=main")
        self.git("config", "user.name", "Release test")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("commit", "--allow-empty", "-m", "default branch")
        self.commit = self.git("rev-parse", "HEAD")
        self.git("update-ref", "refs/remotes/origin/main", self.commit)

    def git(self, *arguments):
        return subprocess.check_output(["git", *arguments], text=True, stderr=subprocess.DEVNULL).strip()

    def resolve(self, tag):
        return release_context.resolve_release(tag, "main", "DavisDing/Relay-Service")

    def test_stable_tag_resolves_commit_and_lowercase_image(self):
        self.git("tag", "v1.2.3")
        result = self.resolve("v1.2.3")
        self.assertEqual(result["commit"], self.commit)
        self.assertEqual(result["image"], "ghcr.io/davisding/relay-service")
        self.assertEqual(result["prerelease"], "false")

    def test_annotated_prerelease_resolves_peeled_commit(self):
        self.git("tag", "-a", "v1.2.3-rc.1", "-m", "release candidate")
        result = self.resolve("v1.2.3-rc.1")
        self.assertEqual(result["commit"], self.commit)
        self.assertEqual(result["prerelease"], "true")

    def test_missing_tag_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "must exist"):
            self.resolve("v1.2.3")

    def test_fork_repository_is_rejected_before_publication(self):
        self.git("tag", "v1.2.3")
        with self.assertRaisesRegex(ValueError, "restricted"):
            release_context.resolve_release("v1.2.3", "main", "someone/Relay-Service")

    def test_tag_outside_default_branch_history_is_rejected(self):
        self.git("checkout", "--orphan", "unrelated")
        self.git("commit", "--allow-empty", "-m", "unrelated history")
        self.git("tag", "v1.2.3")
        with self.assertRaisesRegex(ValueError, "default branch history"):
            self.resolve("v1.2.3")

    def test_malformed_or_injectable_tags_are_rejected_before_git(self):
        for tag in ["latest", "1.2.3", "v01.2.3", "v1.2.3-01", "v1.2.3-rc.01", "v1.2.3\ncommit=bad", "v1.2.3;echo bad", "--help", "v1.2.3+build"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                self.resolve(tag)


if __name__ == "__main__":
    unittest.main()
