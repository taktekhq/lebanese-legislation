# Manual data

Data added by hand, not read from the Centre's site. The build uses it next
to `archive/`, and it ends up in the database and on the website like the
rest. Each kind has its own folder:

| Folder | What it holds | Read by |
| --- | --- | --- |
| `scans/` | Texts the Centre has not published yet, read from Official Gazette scans: one folder per text (a lebgazette `sader-bundle`). `scans.manifest.tsv` beside it records each file's SHA-256 the first time it is seen (`record-scan-manifest`). | `build-db -scans`, the wording guard |
| `rulings/` | Readings of the full court judgments: `index.tsv` and one `<id>.read.json` per judgment, without the PDFs. | `build-db -ruling-files`, `check-rulings` |

Commands default to these folders and skip one that is missing, so a fresh
clone builds without them.

For now the contents are not in git (see `.gitignore`): they are filled on
the machine that runs the build. When they are published, the `.gitignore`
lines go and the files are committed here.

To add a new kind of manual data, give it its own folder here, say in this
table what reads it, and keep its format documented in that folder.
