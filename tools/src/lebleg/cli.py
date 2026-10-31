"""Command dispatch keeps publisher extras out of normal runtime imports."""

import argparse
import importlib
import sys

from . import __version__


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="lebleg", description="Restore, build and publish the Lebanese legal database")
    parser.add_argument("--version", action="version", version=__version__)
    parser.add_argument("command", choices=["restore", "build", "publish", "seed", "pipeline"])
    values = list(sys.argv[1:] if argv is None else argv)
    args = parser.parse_args(values[:1])
    rest = values[1:]
    try:
        module = importlib.import_module("." + args.command, __package__)
        return module.main(rest) or 0
    except ModuleNotFoundError:
        print(
            "This command needs additional dependencies. Install the publisher extra for build/publish.",
            file=sys.stderr,
        )
        return 1
    except Exception:  # noqa: BLE001 - CLI errors must never expose credentials from DSNs
        print("Operation failed. Check the configuration and release compatibility.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
