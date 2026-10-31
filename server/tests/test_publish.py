import hashlib
import json

import pytest

from lebleg.publish import plan_release


def test_release_plan_verifies_dump_and_pins_pointer(tmp_path):
    from test_runtime import manifest

    m = manifest()
    payload = b"public snapshot"
    m["dump"] = {"path": "corpus.dump", "size_bytes": len(payload), "sha256": hashlib.sha256(payload).hexdigest()}
    (tmp_path / "corpus.dump").write_bytes(payload)
    (tmp_path / "manifest.json").write_text(json.dumps(m))
    files = plan_release(tmp_path)
    pointer = json.loads(files["releases/latest.json"])
    raw = files[pointer["manifest_path"]]
    assert pointer["manifest_sha256"] == hashlib.sha256(raw).hexdigest()
    assert files[f"releases/{m['release_id']}/corpus.dump"] == tmp_path / "corpus.dump"
    (tmp_path / "corpus.dump").write_bytes(b"bad")
    with pytest.raises(ValueError):
        plan_release(tmp_path)
