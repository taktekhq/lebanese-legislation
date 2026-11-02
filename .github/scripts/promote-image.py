"""Promote a smoke-tested candidate, retaining existing immutable release tags."""
import json
import os
import subprocess
import urllib.error
import urllib.request
from pathlib import Path

service = os.environ["IMAGE_SERVICE"]
assert service in ("api", "mcp")
package = "lebanese-legislation-" + service
image = "ghcr.io/taktekhq/" + package
source = image + ":source-" + os.environ["GITHUB_SHA"]
version = Path("internal/release/VERSION").read_text().strip()
occupied = False
for page in range(1, 101):
    request = urllib.request.Request(
        f"https://api.github.com/orgs/taktekhq/packages/container/{package}/versions?per_page=100&page={page}",
        headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"], "Accept": "application/vnd.github+json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            versions = json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 404 and page == 1:
            break
        raise
    if any(version in item["metadata"]["container"]["tags"] for item in versions):
        occupied = True
        break
    if len(versions) < 100:
        break
else:
    raise SystemExit("Package version listing exceeded safety bound")
tags = [os.environ["DATE_TAG"], "latest"]
if not occupied:
    tags.append(version)
else:
    print(f"Retaining published immutable {image}:{version}")
for tag in tags:
    subprocess.run(["docker", "buildx", "imagetools", "create", "--tag", image + ":" + tag, source], check=True)
