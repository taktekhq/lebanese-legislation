import pytest

from lebleg_starter.bootstrap import ensure_env


def test_creates_private_distinct_credentials_and_preserves_existing(tmp_path):
    path = tmp_path / ".env"
    assert ensure_env(path)
    values = dict(line.split("=", 1) for line in path.read_text().splitlines())
    assert len(values["POSTGRES_PASSWORD"]) >= 32
    assert len(values["READER_PASSWORD"]) >= 32
    assert values["POSTGRES_PASSWORD"] != values["READER_PASSWORD"]
    assert values["GEMINI_API_KEY"] == ""
    assert path.stat().st_mode & 0o777 == 0o600
    original = path.read_bytes()
    assert not ensure_env(path)
    assert path.read_bytes() == original


def test_refuses_symlink_without_modifying_target(tmp_path):
    target = tmp_path / "secret"
    target.write_text("untouched")
    link = tmp_path / ".env"
    link.symlink_to(target)
    with pytest.raises(ValueError):
        ensure_env(link)
    assert target.read_text() == "untouched"
