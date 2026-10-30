"""Local credentials for an isolated Docker starter, never public release data."""

from __future__ import annotations

import argparse
import os
import secrets
import stat
from pathlib import Path


def ensure_env(path: Path) -> bool:
    if path.is_symlink():
        raise ValueError("Refusing a symbolic link for the environment file")
    if path.exists():
        if not path.is_file():
            raise ValueError("Environment path must be a regular file")
        path.chmod(0o600)
        return False
    values = {
        "POSTGRES_PASSWORD": secrets.token_urlsafe(32),
        "READER_PASSWORD": secrets.token_urlsafe(32),
        "GEMINI_API_KEY": "",
    }
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        fd = os.open(path, flags, stat.S_IRUSR | stat.S_IWUSR)
    except FileExistsError:
        if path.is_symlink() or not path.is_file():
            raise ValueError("Environment path must be a regular file") from None
        return False
    with os.fdopen(fd, "w", encoding="utf-8") as out:
        out.write("".join(f"{key}={value}\n" for key, value in values.items()))
    return True


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Create private local starter credentials without replacing existing values"
    )
    parser.add_argument("--env-file", type=Path, default=Path(".env"))
    args = parser.parse_args(argv)
    created = ensure_env(args.env_file)
    print("Created private environment file" if created else "Kept existing environment values")
    return 0
