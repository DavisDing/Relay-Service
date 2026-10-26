"""Verify an exported OCI image's Relay binary without extracting or running it."""

import argparse
import hashlib
import io
import json
import re
import sys
import tarfile


MACHINES = {"amd64": 62, "arm64": 183}


def check_oci_architecture(path, architecture):
    if architecture not in MACHINES:
        raise ValueError("expected architecture must be amd64 or arm64")

    with tarfile.open(path, "r:*") as archive:
        def read_member(name):
            member = archive.getmember(name)
            if not member.isfile():
                raise ValueError(f"OCI member is not a regular file: {name}")
            with archive.extractfile(member) as source:
                return source.read()

        def read_blob(descriptor):
            digest = descriptor.get("digest", "")
            if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
                raise ValueError("OCI blob must have a sha256 digest")
            data = read_member("blobs/sha256/" + digest.partition(":")[2])
            if hashlib.sha256(data).hexdigest() != digest.partition(":")[2]:
                raise ValueError("OCI blob digest does not match its content")
            if descriptor.get("size") != len(data):
                raise ValueError("OCI blob size does not match its descriptor")
            return data

        images = []

        def visit(descriptor, depth=0):
            if depth > 8:
                raise ValueError("OCI index nesting is too deep")
            platform = descriptor.get("platform") or {}
            # BuildKit exports provenance as additional unknown/unknown manifests.
            if platform and (platform.get("os") != "linux" or platform.get("architecture") != architecture):
                return
            document = json.loads(read_blob(descriptor))
            if "manifests" in document:
                for child in document["manifests"]:
                    visit(child, depth + 1)
                return
            config = json.loads(read_blob(document["config"]))
            if config.get("os") != "linux" or config.get("architecture") != architecture:
                if platform:
                    raise ValueError(f"image config does not match linux/{architecture}")
                return
            images.append(document)

        index = json.loads(read_member("index.json"))
        for descriptor in index["manifests"]:
            visit(descriptor)
        if len(images) != 1:
            raise ValueError(f"expected one linux/{architecture} image; found {len(images)}")

        relay_header = None
        for descriptor in images[0]["layers"]:
            replacement = None
            has_replacement = False
            hides_lower_relay = False
            with tarfile.open(fileobj=io.BytesIO(read_blob(descriptor)), mode="r:*") as layer:
                for member in layer:
                    name = member.name.lstrip("/")
                    while name.startswith("./"):
                        name = name[2:]
                    if name in (".wh.relay", ".wh..wh..opq"):
                        hides_lower_relay = True
                    elif name == "relay":
                        has_replacement = True
                        replacement = None
                        if member.isfile() and member.mode & 0o111:
                            with layer.extractfile(member) as source:
                                replacement = source.read(64)
            # Whiteouts hide lower layers, including when they follow a new file
            # in this layer's tar order. The same-layer replacement wins.
            if hides_lower_relay:
                relay_header = None
            if has_replacement:
                relay_header = replacement

        if relay_header is None:
            raise ValueError("final /relay must be a regular executable file")
        if len(relay_header) < 64 or relay_header[:6] != b"\x7fELF\x02\x01":
            raise ValueError("/relay must be a little-endian ELF64 binary")
        machine = int.from_bytes(relay_header[18:20], "little")
        if machine != MACHINES[architecture]:
            raise ValueError(f"/relay ELF machine {machine} does not match {architecture} ({MACHINES[architecture]})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("oci_tar")
    parser.add_argument("architecture", choices=sorted(MACHINES))
    arguments = parser.parse_args()
    try:
        check_oci_architecture(arguments.oci_tar, arguments.architecture)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    print(f"PASS: OCI linux/{arguments.architecture} config and /relay ELF architecture agree; image was not run")
    return 0


if __name__ == "__main__":
    sys.exit(main())
