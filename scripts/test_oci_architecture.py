"""Synthetic OCI regression fixtures; no Docker, network or database is used."""

import gzip
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("check-oci-architecture.py")
SPEC = importlib.util.spec_from_file_location("check_oci_architecture", SCRIPT)
checker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checker)
INDEX = "application/vnd.oci.image.index.v1+json"
MANIFEST = "application/vnd.oci.image.manifest.v1+json"


def tar_bytes(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:") as archive:
        for name, data, mode in files:
            member = tarfile.TarInfo(name)
            member.size = len(data)
            member.mode = mode
            archive.addfile(member, io.BytesIO(data))
    return output.getvalue()


def elf_header(machine):
    data = bytearray(64)
    data[:7] = b"\x7fELF\x02\x01\x01"
    data[18:20] = machine.to_bytes(2, "little")
    return bytes(data)


class OCIArchitectureTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="relay-oci-test-")
        self.addCleanup(self.temporary.cleanup)
        self.path = Path(self.temporary.name) / "image.tar"

    def fixture(self, machines=(183,), platform="arm64", config_architecture=None, nested=False):
        blobs = {}

        def blob(data, media_type):
            digest = hashlib.sha256(data).hexdigest()
            blobs["blobs/sha256/" + digest] = data
            return {"digest": "sha256:" + digest, "size": len(data), "mediaType": media_type}

        def json_blob(value, media_type):
            return blob(json.dumps(value).encode(), media_type)

        layers = [blob(gzip.compress(tar_bytes([("relay", elf_header(machine), 0o755)]), mtime=0),
                       "application/vnd.oci.image.layer.v1.tar+gzip") for machine in machines]
        config = json_blob({"os": "linux", "architecture": config_architecture or platform},
                           "application/vnd.oci.image.config.v1+json")
        image = json_blob({"schemaVersion": 2, "mediaType": MANIFEST, "config": config, "layers": layers}, MANIFEST)
        image["platform"] = {"os": "linux", "architecture": platform}
        descriptors = [image]
        if nested:
            # Provenance is not an executable platform image and has no /relay.
            unknown_config = json_blob({"os": "unknown", "architecture": "unknown"},
                                       "application/vnd.oci.image.config.v1+json")
            attestation = json_blob({"schemaVersion": 2, "config": unknown_config, "layers": []}, MANIFEST)
            attestation["platform"] = {"os": "unknown", "architecture": "unknown"}
            descriptors = [json_blob({"schemaVersion": 2, "mediaType": INDEX,
                                      "manifests": [image, attestation]}, INDEX)]
        index = json.dumps({"schemaVersion": 2, "manifests": descriptors}).encode()
        self.path.write_bytes(tar_bytes([("index.json", index, 0o644)] +
                                       [(name, data, 0o644) for name, data in blobs.items()]))

    def test_correct_arm64_passes_cli(self):
        self.fixture()
        result = subprocess.run([sys.executable, str(SCRIPT), str(self.path), "arm64"],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("PASS: OCI linux/arm64", result.stdout)

    def test_arm64_manifest_with_amd64_binary_is_rejected(self):
        self.fixture(machines=(62,))
        with self.assertRaisesRegex(ValueError, "ELF machine 62 does not match arm64"):
            checker.check_oci_architecture(self.path, "arm64")

    def test_missing_target_architecture_is_rejected(self):
        self.fixture(machines=(62,), platform="amd64")
        with self.assertRaisesRegex(ValueError, "expected one linux/arm64 image; found 0"):
            checker.check_oci_architecture(self.path, "arm64")

    def test_later_layer_replacing_binary_is_checked(self):
        self.fixture(machines=(183, 62))
        with self.assertRaisesRegex(ValueError, "ELF machine 62 does not match arm64"):
            checker.check_oci_architecture(self.path, "arm64")

    def test_nested_index_and_unknown_attestation_are_supported(self):
        self.fixture(nested=True)
        checker.check_oci_architecture(self.path, "arm64")

    def test_config_architecture_must_match_manifest(self):
        self.fixture(config_architecture="amd64")
        with self.assertRaisesRegex(ValueError, "image config does not match linux/arm64"):
            checker.check_oci_architecture(self.path, "arm64")

    def test_correct_amd64_is_supported(self):
        self.fixture(machines=(62,), platform="amd64")
        checker.check_oci_architecture(self.path, "amd64")


if __name__ == "__main__":
    unittest.main()
