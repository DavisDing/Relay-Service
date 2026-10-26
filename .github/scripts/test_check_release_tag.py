import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location("check_release_tag", Path(__file__).with_name("check_release_tag.py"))
check_release_tag = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check_release_tag)


class RemoteTagTests(unittest.TestCase):
    def resolve(self, responses):
        return check_release_tag.remote_tag_commit("https://api.example.invalid", "DavisDing/Relay-Service", "v1.0.0", "unused", responses.__getitem__)

    def test_lightweight_tag(self):
        self.assertEqual(self.resolve({"https://api.example.invalid/repos/DavisDing/Relay-Service/git/ref/tags/v1.0.0": {"object": {"type": "commit", "sha": "c"}}}), "c")

    def test_nested_annotated_tag_is_peeled(self):
        prefix = "https://api.example.invalid/repos/DavisDing/Relay-Service/git/"
        responses = {
            prefix + "ref/tags/v1.0.0": {"object": {"type": "tag", "sha": "a"}},
            prefix + "tags/a": {"object": {"type": "tag", "sha": "b"}},
            prefix + "tags/b": {"object": {"type": "commit", "sha": "c"}},
        }
        self.assertEqual(self.resolve(responses), "c")

    def test_non_commit_and_cycle_are_rejected(self):
        prefix = "https://api.example.invalid/repos/DavisDing/Relay-Service/git/"
        with self.assertRaisesRegex(ValueError, "does not point"):
            self.resolve({prefix + "ref/tags/v1.0.0": {"object": {"type": "tree", "sha": "a"}}})
        with self.assertRaisesRegex(ValueError, "too deep"):
            self.resolve({prefix + "ref/tags/v1.0.0": {"object": {"type": "tag", "sha": "a"}}, prefix + "tags/a": {"object": {"type": "tag", "sha": "a"}}})


if __name__ == "__main__":
    unittest.main()
