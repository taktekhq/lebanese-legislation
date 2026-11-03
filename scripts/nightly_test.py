#!/usr/bin/env python3
"""Exercise nightly gates and git publication locally, without scraping or a DB."""
import datetime
import fcntl
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parent
GIT = shutil.which("git")


def run(*args, cwd=None, env=None):
    return subprocess.run(args, cwd=cwd, env=env, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT)


def executable(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    path.chmod(0o755)


class Nightly(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.env = dict(os.environ)
        for name in list(self.env):
            if name.startswith("LEBLEG_") or name in ("GITHUB_ACTIONS", "NIGHT"):
                self.env.pop(name)
        self.env.update(LEBLEG_DNS_SKIP="1", GIT_AUTHOR_NAME="Nightly test",
                        GIT_AUTHOR_EMAIL="test@example.invalid",
                        GIT_COMMITTER_NAME="Nightly test",
                        GIT_COMMITTER_EMAIL="test@example.invalid")
        self.git("init", "-q", "-b", "main")
        (self.repo / "scripts").mkdir()
        for name in ("nightly.sh", "check-build-report.sh"):
            shutil.copy2(SOURCE / name, self.repo / "scripts" / name)
        executable(self.repo / "scripts/check-data-age.sh", "#!/bin/bash\nexit 0\n")
        executable(self.repo / "scraper/bin/ul-scrape", '#!/bin/bash\nprintf "scraper %s\\n" "$*" >> "$CALLS"\n')
        data = self.repo / "data"
        (data / "tables/noindex_page").mkdir(parents=True)
        for name, content in {"popular_texts.txt": "123\n", "shards.json": "{}\n",
                              "stats.json": "old\n", "README.md": "curated\n",
                              "article_calculated_current.jsonl": "old\n"}.items():
            (data / name).write_text(content)
        (data / "tables/noindex_page/old.tsv").write_text("old\n")
        self.git("add", ".")
        self.git("commit", "-qm", "baseline")
        remote = self.root / "remote.git"
        self.assertEqual(run(GIT, "init", "--bare", "-q", str(remote)).returncode, 0)
        self.git("remote", "add", "origin", str(remote))
        self.git("push", "-qu", "origin", "main")
        self.mocks = self.root / "mocks"
        self.env.update(PATH=f"{self.mocks}:{self.env['PATH']}",
                        CALLS=str(self.root / "calls"))
        executable(self.mocks / "git", f'''#!/bin/bash
if [ "${{1:-}}" = submodule ]; then exit 0; fi
exec {GIT} "$@"
''')
        executable(self.mocks / "gh", '#!/bin/bash\nprintf "gh %s\\n" "$*" >> "$CALLS"\n')
        # Fake compilation installs small command stubs. Git/worktrees/rsync/lock
        # remain real, so commits, deletions, cleanup and pushes are verified.
        executable(self.mocks / "go", '''#!/bin/bash
set -eu
cp "$COMMAND_STUB" "$3"
chmod +x "$3"
''')
        stub = self.root / "command-stub"
        executable(stub, '''#!/bin/bash
set -eu
name=${0##*/}
printf '%s %s\\n' "$name" "$*" >> "$CALLS"
case "$name" in
  build-db)
    if [[ " $* " == *" -only indexing "* ]]; then exit "${INDEX_RC:-0}"; fi
    echo "publishable: ${PUBLISHABLE:-true}"
    exit "${BUILD_RC:-0}" ;;
  check-dates) exit "${DATES_RC:-0}" ;;
  export-data|calculated-current)
    out=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -out ]; then out=$2; break; fi
      shift
    done
    if [ "$name" = export-data ]; then
      [ "${EXPORT_RC:-0}" -eq 0 ] || exit "$EXPORT_RC"
      mkdir -p "$out/tables/noindex_page"
      rm -f "$out/tables/noindex_page/old.tsv"
      printf 'english\\t193806\\n' > "$out/tables/noindex_page/0.tsv"
      echo new > "$out/stats.json"
    else
      echo new > "$out"
    fi ;;
esac
''')
        self.env["COMMAND_STUB"] = str(stub)
        self.branch = "data/" + datetime.datetime.now(datetime.timezone.utc).strftime("%F")

    def git(self, *args):
        result = run(GIT, *args, cwd=self.repo, env=self.env)
        self.assertEqual(result.returncode, 0, result.stdout)
        return result.stdout

    def nightly(self, *args, **env):
        return run("bash", str(self.repo / "scripts/nightly.sh"), *args,
                   cwd=self.repo, env={**self.env, **env})

    def calls(self):
        path = self.root / "calls"
        return path.read_text() if path.exists() else ""

    def test_busy_lock_returns_75(self):
        lock = self.root / "nightly.lock"
        with lock.open("w") as handle:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.nightly("--dry-run", LEBLEG_LOCK_FILE=str(lock), LEBLEG_LOCK_WAIT="0")
        self.assertEqual(result.returncode, 75)
        self.assertEqual(self.calls(), "")

    def test_github_actions_even_empty_is_refused(self):
        result = self.nightly("--dry-run", GITHUB_ACTIONS="")
        self.assertEqual(result.returncode, 1)
        self.assertIn("never in GitHub Actions", result.stdout)
        self.assertEqual(self.calls(), "")

    def test_build_and_indexing_failures_hold_export(self):
        for setting in ({"BUILD_RC": "2"}, {"INDEX_RC": "1"},
                        {"DATES_RC": "1"}, {"PUBLISHABLE": "false"}):
            with self.subTest(setting=setting):
                (self.root / "calls").write_text("")
                self.assertNotEqual(self.nightly("--dry-run", **setting).returncode, 0)
                self.assertNotIn("export-data ", self.calls())
                self.assertNotIn("gh ", self.calls())

    def test_missing_popular_list_holds_run(self):
        (self.repo / "data/popular_texts.txt").unlink()
        self.assertEqual(self.nightly("--dry-run").returncode, 1)
        self.assertEqual(self.calls(), "")

    def test_provenance_exit_is_preserved(self):
        self.assertEqual(self.nightly("--dry-run", EXPORT_RC="4").returncode, 4)
        self.assertNotIn("calculated-current ", self.calls())
        self.assertNotIn("gh ", self.calls())

    def test_dry_run_commits_data_only_and_cleans_worktree(self):
        (self.repo / "unrelated-code").write_text("local\n")
        result = self.nightly("--dry-run")
        self.assertEqual(result.returncode, 0, result.stdout)
        changed = self.git("diff", "--name-only", "main", self.branch).splitlines()
        self.assertTrue(changed)
        self.assertTrue(all(p.startswith("data/") for p in changed))
        self.assertIn("-only indexing", self.calls())
        self.assertIn("-popular", self.calls())
        self.assertNotIn("gh ", self.calls())
        self.assertEqual(self.git("ls-remote", "--heads", "origin", self.branch), "")
        self.assertEqual(self.git("worktree", "list", "--porcelain").count("worktree "), 1)
        self.assertEqual(self.git("show", f"{self.branch}:data/README.md"), "curated\n")
        self.assertNotIn("old.tsv", self.git("ls-tree", "-r", self.branch, "data/tables"))

    def test_push_and_pr_with_external_export(self):
        result = self.nightly(LEBLEG_DATA=str(self.root / "export"), LEBLEG_HEAD_OWNER="contributor")
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn(self.branch, self.git("ls-remote", "--heads", "origin", self.branch))
        self.assertIn("gh pr create --repo taktekhq/lebanese-legislation", self.calls())
        self.assertIn("--head contributor:" + self.branch, self.calls())
        self.assertEqual(self.git("show", f"{self.branch}:data/popular_texts.txt"), "123\n")
        self.assertEqual(self.git("worktree", "list", "--porcelain").count("worktree "), 1)

    def test_no_change_does_not_push_or_open_pr(self):
        self.assertEqual(self.nightly("--dry-run").returncode, 0)
        self.git("reset", "--hard", self.branch)
        self.git("push", "origin", "main")
        self.git("branch", "-D", self.branch)
        (self.root / "calls").write_text("")
        result = self.nightly()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("gh ", self.calls())
        self.assertEqual(self.git("ls-remote", "--heads", "origin", self.branch), "")


if __name__ == "__main__":
    unittest.main()
