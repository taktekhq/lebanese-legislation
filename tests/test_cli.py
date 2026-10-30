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
