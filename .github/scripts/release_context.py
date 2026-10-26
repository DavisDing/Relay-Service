"""Resolve an existing SemVer tag and enforce default-branch ancestry."""

import os
import re
import subprocess
import sys


TAG_PATTERN = re.compile(
    r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?\Z"
)


def resolve_release(tag, default_branch, repository):
    match = TAG_PATTERN.fullmatch(tag)
    if match is None:
        raise ValueError("release tag must be vX.Y.Z with an optional SemVer prerelease")
    prerelease = match.group(4)
    if prerelease and any(part.isdigit() and len(part) > 1 and part[0] == "0" for part in prerelease.split(".")):
        raise ValueError("numeric prerelease identifiers must not contain leading zeroes")
    if repository.lower() != "davisding/relay-service":
        raise ValueError("release publishing is restricted to DavisDing/Relay-Service")
    if not default_branch:
        raise ValueError("default branch is required")

    def git(*arguments):
        return subprocess.check_output(["git", *arguments], text=True, stderr=subprocess.DEVNULL).strip()

    try:
        commit = git("rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}")
        branch_commit = git("rev-parse", "--verify", f"refs/remotes/origin/{default_branch}^{{commit}}")
        subprocess.run(["git", "merge-base", "--is-ancestor", commit, branch_commit], check=True, stderr=subprocess.DEVNULL)
        created = git("show", "-s", "--format=%cI", commit)
    except subprocess.CalledProcessError as error:
        raise ValueError("release tag must exist and point to a commit in the default branch history") from error
    return {
        "tag": tag,
        "commit": commit,
        "image": f"ghcr.io/{repository.lower()}",
        "prerelease": str(prerelease is not None).lower(),
        "created": created,
    }


def main():
    try:
        release = resolve_release(os.environ["RELEASE_TAG"], os.environ["DEFAULT_BRANCH"], os.environ["GITHUB_REPOSITORY"])
    except (KeyError, ValueError) as error:
        print(f"::error::{error}", file=sys.stderr)
        return 1
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in release.items():
            output.write(f"{key}={value}\n")
    print(f"Validated {release['tag']} at {release['commit']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
