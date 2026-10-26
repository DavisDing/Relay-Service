"""Check the remote tag again immediately before publication using the API."""

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


def remote_tag_commit(api_url, repository, tag, token, get_json=None):
    def request_json(url):
        request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"})
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)

    get_json = get_json or request_json
    encoded_tag = urllib.parse.quote(tag, safe="")
    reference = get_json(f"{api_url}/repos/{repository}/git/ref/tags/{encoded_tag}")
    current = reference["object"]
    # An annotated tag may point to another annotated tag. Bound traversal to
    # reject malformed/cyclic objects rather than spinning in the runner.
    for _ in range(16):
        if current["type"] == "commit":
            return current["sha"]
        if current["type"] != "tag":
            raise ValueError("release tag does not point to a commit")
        current = get_json(f"{api_url}/repos/{repository}/git/tags/{current['sha']}")["object"]
    raise ValueError("release tag annotation chain is too deep")


def main():
    try:
        commit = remote_tag_commit(os.environ["GITHUB_API_URL"], os.environ["GITHUB_REPOSITORY"], os.environ["RELEASE_TAG"], os.environ["GH_TOKEN"])
        if commit != os.environ["RELEASE_COMMIT"]:
            raise ValueError("release tag moved after verification; refusing publication")
    except (KeyError, ValueError, urllib.error.URLError) as error:
        print(f"::error::{error}", file=sys.stderr)
        return 1
    print(f"Remote release tag still identifies {commit}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
