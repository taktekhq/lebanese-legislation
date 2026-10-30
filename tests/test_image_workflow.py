"""Release retries preserve an existing image while publishing a missing peer."""

import io
import json
import textwrap
from pathlib import Path

import pytest


def test_image_release_guard_retries_same_source_but_refuses_replacement(monkeypatch, tmp_path):
    workflow = (Path(__file__).parents[1] / ".github/workflows/images.yml").read_text()
    block = workflow.split("python3 - <<'PYCODE'", 1)[1].split("          PYCODE", 1)[0]
    block = textwrap.dedent(block)
    output = tmp_path / "output"
    monkeypatch.setenv("VERSION", "0.1.5")
    monkeypatch.setenv("GITHUB_SHA", "a" * 40)
    monkeypatch.setenv("GH_TOKEN", "test-only")
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    occupied = []

    def request(url, **kwargs):
        rows = occupied if "/lebanese-legislation-db/" in url.full_url else []
        return io.StringIO(json.dumps(rows))

    monkeypatch.setattr("urllib.request.urlopen", request)
    exec(compile(block, "<release guard>", "exec"), {})  # noqa: S102 - trusted checked-in workflow, mocked network
    assert "build_db=true" in output.read_text() and "build_api=true" in output.read_text()
    occupied.append({"metadata": {"container": {"tags": ["0.1.5", "source-" + "a" * 40]}}})
    output.unlink()
    exec(compile(block, "<release guard>", "exec"), {})  # noqa: S102 - trusted checked-in workflow, mocked network
    assert "build_db=false" in output.read_text() and "build_api=true" in output.read_text()
    occupied[0]["metadata"]["container"]["tags"] = ["0.1.5", "source-" + "b" * 40]
    with pytest.raises(SystemExit, match="another source"):
        exec(compile(block, "<release guard>", "exec"), {})  # noqa: S102 - trusted checked-in workflow, mocked network
