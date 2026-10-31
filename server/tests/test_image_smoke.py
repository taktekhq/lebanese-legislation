"""Execute the exact release smoke against the installed public FastAPI API."""
import os
import shlex
import subprocess
import sys
from pathlib import Path

import pytest


@pytest.mark.parametrize('mode', ['api', 'mcp'])
def test_image_mode_smoke_executes_actual_snippet(mode):
    root=Path(__file__).parents[1]
    script=(root.parent/'.github/scripts/smoke-images.sh').read_text()
    line=next(line for line in script.splitlines() if 'expected=sys.argv[1]' in line)
    snippet=shlex.split(line.strip())[1]
    result=subprocess.run([sys.executable,'-c',snippet,mode],
        env={**os.environ,'APP_MODE':mode,'PYTHONPATH':str(root/'src')},
        capture_output=True,text=True,check=False)
    assert result.returncode==0,result.stderr
