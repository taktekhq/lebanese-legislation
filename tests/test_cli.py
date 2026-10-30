from types import SimpleNamespace

from lebleg_starter.cli import main


def test_subcommand_help_is_delegated(monkeypatch):
    seen = []
    monkeypatch.setattr(
        "lebleg_starter.cli.importlib.import_module",
        lambda *args: SimpleNamespace(main=lambda argv: seen.extend(argv) or 0),
    )
    assert main(["restore", "--help"]) == 0
    assert seen == ["--help"]


def test_release_version_matches_package_metadata():
    import tomllib
    from pathlib import Path

    from lebleg_starter import __version__

    project = tomllib.loads((Path(__file__).parents[1] / "pyproject.toml").read_text())
    assert __version__ == project["project"]["version"]
