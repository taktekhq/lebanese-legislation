"""Public resource API over the exported snapshot and the shared read-only pool."""

import calendar
import re
from contextlib import contextmanager
from datetime import date
from urllib.parse import quote, urlsplit

import psycopg
from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from psycopg_pool import PoolTimeout

NOT_ADVICE = "Not legal advice. Official texts from the Lebanese University's Centre for Research and Studies in Legal Informatics."
TITLE_EN_NOTE = "English titles are machine-generated discovery aids, not authoritative translations. Original source text remains the reference."
CACHE_CONTROL = "public, max-age=60, stale-while-revalidate=300"
SCAN_ID_BASE = 2_000_000_000
LAW_COLS = """l.*, k.name_en AS kind_en,
    (SELECT count(*) FROM legislation_article a WHERE a.law_id=l.id)::int AS article_count,
    (SELECT name_en FROM code c WHERE c.law_id=l.id) AS code,
    (SELECT title_en FROM legislation_title_en t WHERE t.law_id=l.id) AS translated_title,
    (SELECT min(issue_id) FROM gazette_entry e WHERE e.law_id=l.id) AS gazette_issue_id"""
LAW_FROM = " FROM legislation l JOIN legislation_kind k ON k.name_ar=l.kind"


class GeneralNotFound(LookupError):
    pass


class GeneralError(RuntimeError):
    pass


def _clean(value):
    """Match Go omitempty while keeping false, zero and required empty strings."""
    return {k: v.isoformat() if isinstance(v, date) else v for k, v in value.items() if v is not None}


def _slug(number):
    return "-".join(number.split())


def _safe_url(value):
    if value:
        try:
            parsed = urlsplit(value)
        except ValueError:
            return None
        if parsed.scheme in ("http", "https") and parsed.netloc and not parsed.username and not parsed.password:
            return value
    return None


def _clamp(value, default, low, high):
    try:
        return max(low, min(high, int(value)))
    except (ValueError, TypeError):
        return default


def date_bound(value, end=False):
    """Go list API inclusive date bounds: year, month, or day."""
    try:
        if not re.fullmatch(r"\d{4}(?:-\d{2}(?:-\d{2})?)?", value):
            raise ValueError
        parts = [int(p) for p in value.split("-")]
        year, month, day = (parts + [1, 1])[:3]
        if end and len(parts) == 1:
            month, day = 12, 31
        elif end and len(parts) == 2:
            day = calendar.monthrange(year, month)[1]
        return date(year, month, day)
    except (ValueError, IndexError):
        raise ValueError(f'"{value}" is not a date: use YYYY, YYYY-MM or YYYY-MM-DD') from None


def list_params(since="", until="", kind="", order="", limit=None, offset=None, **_):
    since, until, kind = since.strip(), until.strip(), kind.strip()
    if order not in ("", "newest", "oldest"):
        raise ValueError("order is newest or oldest")
    return {
        "since": since or None,
        "until": until or None,
        "kind": kind or None,
        "start": date_bound(since) if since else None,
        "end": date_bound(until, True) if until else None,
        "order": order or "newest",
        "limit": _clamp(limit, 50, 1, 100),
        "offset": _clamp(offset, 0, 0, 1 << 30),
    }


class GeneralService:
    def __init__(self, search_service, public_url="https://lebaneselegislation.com"):
        if not _safe_url(public_url):
            raise ValueError("public_url must be an HTTP or HTTPS URL without credentials")
        self.search_service = search_service
        self.pool = search_service.pool
        self.public_url = public_url.rstrip("/")

    def _url(self, kind, ident, number=None):
        path = f"/{kind}/{ident}/"
        if number is not None:
            path += "article/" + quote(_slug(number), safe="") + "/"
        return self.public_url + path

    def _source(self, conn, kind, ident):
        row = conn.execute("SELECT url FROM source_page WHERE doc_type=%s AND centre_id=%s", (kind, ident)).fetchone()
        if row and _safe_url(row["url"]):
            return row["url"]
        if kind == "law":
            scan = conn.execute("SELECT source_url FROM legislation_scan WHERE law_id=%s", (ident,)).fetchone()
            return _safe_url(scan["source_url"]) if scan else None
        return None

    def _summary(self, row, source_url=None):
        return _clean(
            {
                **{
                    k: row.get(k)
                    for k in (
                        "id",
                        "kind",
                        "kind_en",
                        "number",
                        "issued_on",
                        "title",
                        "status",
                        "article_count",
                        "code",
                    )
                },
                "title_en": row.get("translated_title") or row.get("title_en") or None,
                "url": self._url("law", row["id"]),
                "source_url": source_url,
            }
        )

    @contextmanager
    def connection(self):
        try:
            with self.search_service.connection() as conn:
                yield conn
        except (psycopg.Error, PoolTimeout):
            raise GeneralError("service unavailable") from None

    def summary(self, ident):
        with self.connection() as conn:
            return self._summary(self._law_row(conn, ident), self._source(conn, "law", ident))

    def _law_row(self, conn, ident):
        row = conn.execute("SELECT " + LAW_COLS + LAW_FROM + " WHERE l.id=%s", (ident,)).fetchone()
        if row is None:
            raise GeneralNotFound("law not found")
        return row

    def _detail_status(self, conn, row):
        repealed = conn.execute(
            """SELECT EXISTS(SELECT 1 FROM amendment WHERE law_id=%s
            AND article_number IS NULL AND action='repeal' AND part IS NULL AND status='applied') AS yes""",
            (row["id"],),
        ).fetchone()["yes"]
        if repealed:
            row["status"] = "repealed"

    def root(self):
        return {
            "name": "Lebanese Legislation API",
            "version": "v1",
            "docs": self.public_url + "/developers/",
            "mcp": self.public_url + "/mcp",
            "not_advice": NOT_ADVICE,
            "notes": [
                TITLE_EN_NOTE,
                "Resources are served from the installed public snapshot. Original source text and recorded version uncertainty are preserved.",
            ],
            "endpoints": [
                "GET /api/v1/search/?q=&type=laws|rulings|gazette|articles&year=&limit=&offset=",
                "GET /api/v1/law/{id}/",
                "GET /api/v1/law/{id}/article/{n}/",
                "GET /api/v1/ruling/{id}/",
                "GET /api/v1/gazette/{id}/",
                "GET /api/v1/laws/?since=&until=&kind=&order=newest|oldest&limit=&offset=",
                "GET /api/v1/gazette/?since=&until=&order=newest|oldest&limit=&offset=",
            ],
        }

    def law(self, ident):
        with self.connection() as conn:
            row = self._law_row(conn, ident)
            self._detail_status(conn, row)
            out = self._summary(row, self._source(conn, "law", ident))
            out.update(
                _clean(
                    {
                        k: row.get(k)
                        for k in (
                            "gazette_number",
                            "gazette_date",
                            "gazette_issue_id",
                            "preamble",
                            "explanatory_note",
                            "closing",
                        )
                    }
                )
            )
            sections = conn.execute(
                "SELECT id, depth, heading, note FROM legislation_section WHERE law_id=%s ORDER BY position", (ident,)
            ).fetchall()
            articles = conn.execute(
                "SELECT position, number, section_id FROM legislation_article WHERE law_id=%s ORDER BY position",
                (ident,),
            ).fetchall()
            # Version-only additions are real source records too, with no invented position.
            extras = conn.execute(
                "SELECT DISTINCT article_number FROM article_version WHERE law_id=%s ORDER BY article_number", (ident,)
            ).fetchall()
            have = {a["number"] for a in articles}
            for extra in extras:
                if extra["article_number"] not in have:
                    item = {"position": 0, "number": extra["article_number"], "section_id": None}
                    lead = lambda value: int(re.match(r"[0-9]+", value)[0]) if re.match(r"[0-9]+", value) else 0
                    at = next(
                        (i for i, a in enumerate(articles) if lead(a["number"]) > lead(item["number"])), len(articles)
                    )
                    articles.insert(at, item)
            if sections:
                out["table_of_contents"] = [_clean(s) for s in sections]
            if articles:
                out["articles"] = [{**_clean(a), "url": self._url("law", ident, a["number"])} for a in articles]
            for key, incoming in (("amended_by", True), ("amended", False)):
                changes = self._law_changes(conn, ident, incoming)
                if changes:
                    out[key] = changes
            out["not_legal_advice"] = NOT_ADVICE
            return out

    def _law_changes(self, conn, ident, incoming):
        near, where = ("e.from_id", "e.to_law_id") if incoming else ("e.to_law_id", "e.from_id")
        rows = conn.execute(
            "SELECT e.relation, e.to_article, "
            + LAW_COLS
            + LAW_FROM
            + f" JOIN graph_edge e ON l.id={near} WHERE {where}=%s AND e.from_type='legislation' AND l.id<>%s AND e.status IN ('linked','no_article') AND e.relation IN ('replace','amend','repeal','insert','suspend','extend') ORDER BY coalesce(l.gazette_date,l.issued_on) NULLS FIRST,l.id,e.id",
            (ident, ident),
        ).fetchall()
        other, match = ("by_law_id", "law_id") if incoming else ("law_id", "by_law_id")
        amendments = conn.execute(
            "SELECT x.action, x.part, x.article_number, x.effective_on, "
            + LAW_COLS
            + LAW_FROM
            + f" JOIN amendment x ON l.id=x.{other} WHERE x.{match}=%s AND x.status IN ('applied','partial') ORDER BY x.effective_on NULLS FIRST,l.id,x.seq",
            (ident,),
        ).fetchall()
        for a in amendments:
            a["relation"] = (
                "insert"
                if a["part"] == "+"
                else "amend"
                if a["part"] is not None and a["action"] == "replace"
                else a["action"]
            )
            a["to_article"] = a["article_number"]
        verbs = {
            "replace": "Replaced or reworded",
            "amend": "Edited in part",
            "repeal": "Repealed",
            "insert": "Added to",
            "suspend": "Suspended",
            "extend": "Extended",
        }
        groups = {}
        self_date = None
        if not incoming:
            own = self._law_row(conn, ident)
            self_date = own.get("gazette_date") or own.get("issued_on")
        for row in [*rows, *amendments]:
            key = row["relation"], row["id"]
            if key not in groups:
                groups[key] = _clean(
                    {
                        "relation": key[0],
                        "verb": verbs.get(key[0], "Changed"),
                        "law": self._summary(row),
                        "effective_on": (row.get("effective_on") or row.get("gazette_date") or row.get("issued_on"))
                        if incoming
                        else self_date,
                    }
                )
            article = row["to_article"]
            if article and article not in groups[key].setdefault("articles", []):
                groups[key]["articles"].append(article)
        for group in groups.values():
            if group.get("articles"):
                group["articles"].sort(
                    key=lambda value: [
                        int(p) if p.isascii() and p.isdigit() else p for p in re.split(r"([0-9]+)", value)
                    ]
                )
        return list(groups.values())

    def article(self, ident, number):
        with self.connection() as conn:
            law = self._law_row(conn, ident)
            self._detail_status(conn, law)
            rows = conn.execute(
                "SELECT * FROM legislation_article WHERE law_id=%s ORDER BY position", (ident,)
            ).fetchall()
            article = next((a for a in rows if _slug(a["number"]) == _slug(number)), None)
            versions = conn.execute(
                "SELECT article_number,major,minor,valid_from,valid_to,state,text_ar,text_source,note FROM article_version WHERE law_id=%s ORDER BY major,minor",
                (ident,),
            ).fetchall()
            versions = [v for v in versions if _slug(v["article_number"]) == _slug(number)]
            if article is None:
                if not versions:
                    raise GeneralNotFound("article not found")
                article = {
                    "number": versions[0]["article_number"],
                    "text_ar": "",
                    "text_fr": None,
                    "note": None,
                    "in_force_from": None,
                }
            number = article["number"]
            source = self._source(conn, "law", ident)
            out = _clean(
                {
                    "law": self._summary(law, source),
                    "number": number,
                    "note": article["note"],
                    "text_ar": article["text_ar"],
                    "text_fr": article["text_fr"],
                    "in_force_from": article["in_force_from"],
                    "url": self._url("law", ident, number),
                    "source_url": source,
                    "not_legal_advice": NOT_ADVICE,
                }
            )
            if versions:
                out["versions"] = [
                    _clean({k: v for k, v in version.items() if k != "article_number"}) for version in versions
                ]
                # Go selects the latest recorded version with actual source text, preserving the Centre text.
                shown = next((v["text_ar"] for v in reversed(versions) if v["text_ar"] is not None), article["text_ar"])
                repealed_note = bool(re.match(r"^\s*مادة ملغا", article["note"] or ""))
                if shown != article["text_ar"] and not repealed_note:
                    out["centre_text_ar"] = article["text_ar"]
                    out["text_ar"] = shown
                    if article["text_fr"] is not None:
                        out["centre_text_fr"] = article["text_fr"]
                    out.pop("text_fr", None)
                if versions[-1]["state"] == "repealed" or repealed_note:
                    out["repealed"] = True
            error = conn.execute(
                "SELECT note_en AS en,note_ar AS ar FROM centre_error WHERE law_id=%s AND number=%s", (ident, number)
            ).fetchone()
            if error:
                out["centre_error"] = _clean(error)
            if re.match(r"^\s*مادة ملغا", article["note"] or ""):
                out["repealed"] = True
            self._article_links(conn, ident, number, out)
            return out

    def _article_links(self, conn, ident, number, out):
        rows = conn.execute(
            """WITH e AS (
            SELECT from_id AS by_id, from_article AS by_article, to_article AS article,
                   relation, part, source FROM graph_edge
            WHERE to_law_id=%s AND status IN ('linked','no_article') AND from_id<>%s
              AND relation IN ('replace','amend','repeal','insert','suspend','extend')
            UNION ALL
            SELECT to_law_id, NULL, from_article, relation, NULL, source FROM graph_edge
            WHERE from_type='legislation' AND from_id=%s AND status='linked'
              AND to_law_id<>%s AND relation IN ('note_amended_by','note_repealed_by','note_added_by')
            ) SELECT e.by_article,e.relation,e.part,e.source,coalesce(a.text_ar,'') AS change_text,
            """
            + LAW_COLS
            + LAW_FROM
            + """ JOIN e ON e.by_id=l.id
            LEFT JOIN legislation_article a ON a.law_id=e.by_id AND a.number=e.by_article
            WHERE e.article=%s ORDER BY coalesce(l.gazette_date,l.issued_on) NULLS LAST,l.id""",
            (ident, ident, ident, ident, number),
        ).fetchall()
        changes = {}
        for row in rows:
            key = row["id"], row["by_article"]
            if key not in changes:
                changes[key] = _clean(
                    {
                        "relation": row["relation"],
                        "part": row["part"],
                        "by": self._summary(row),
                        "by_article": row["by_article"] or None,
                        "effective_on": row["gazette_date"] or row["issued_on"],
                        "text_ar": row["change_text"] or None,
                        "found_by": [],
                        "url": self._url("law", row["id"], row["by_article"] or ""),
                    }
                )
            sources = changes[key]["found_by"]
            if row["source"] not in sources:
                sources.append(row["source"])
        if changes:
            out["changes"] = list(changes.values())
        affected = conn.execute(
            "SELECT p.repealing_article,"
            + LAW_COLS
            + LAW_FROM
            + " JOIN possibly_affected p ON p.repealing_law_id=l.id WHERE p.law_id=%s AND p.article_number=%s ORDER BY l.issued_on,p.repealing_article",
            (ident, number),
        ).fetchall()
        if affected:
            out["possibly_affected_by"] = [
                {
                    "by": self._summary(row),
                    "by_article": row["repealing_article"] or "",
                    "url": self._url("law", row["id"], row["repealing_article"] or ""),
                }
                for row in affected
            ]
        rows = conn.execute(
            """SELECT DISTINCT r.id,coalesce(r.court,'') AS court,r.number,r.year,r.decided_on
            FROM graph_edge e JOIN ruling r ON r.id=e.from_id
            WHERE e.from_type='ruling' AND e.to_law_id=%s AND e.to_article=%s AND e.status='linked'
            ORDER BY r.decided_on DESC NULLS LAST,r.id DESC""",
            (ident, number),
        ).fetchall()
        if rows:
            out["rulings_count"] = len(rows)
            out["rulings"] = [_clean({**row, "url": self._url("ruling", row["id"])}) for row in rows[:20]]

    def ruling(self, ident):
        with self.connection() as conn:
            row = conn.execute(
                "SELECT r.*,c.name_en AS court_en FROM ruling r LEFT JOIN court c ON c.name_ar=r.court WHERE r.id=%s",
                (ident,),
            ).fetchone()
            if row is None:
                raise GeneralNotFound("ruling not found")
            out = _clean(
                {
                    **row,
                    "url": self._url("ruling", ident),
                    "source_url": self._source(conn, "ruling", ident),
                    "not_legal_advice": NOT_ADVICE,
                }
            )
            if not out.get("members"):
                out.pop("members", None)
            file = conn.execute("SELECT pages FROM ruling_file WHERE ruling_id=%s", (ident,)).fetchone()
            pages = conn.execute(
                "SELECT n,text_ar,low_confidence,model FROM ruling_page WHERE ruling_id=%s ORDER BY n", (ident,)
            ).fetchall()
            if file:
                out.update(
                    pdf_url=f"https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID={ident}&type=list",
                    page_count=file["pages"] or 0,
                )
            if pages:
                out["pages"] = [
                    {**{k: p[k] for k in ("n", "text_ar")}, **({"low_confidence": True} if p["low_confidence"] else {})}
                    for p in pages
                ]
                out["pages_model"] = pages[0]["model"]
                out["page_count"] = max(out.get("page_count", 0), pages[-1]["n"])
                out["pages_note"] = (
                    "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference."
                )
                if any(p["low_confidence"] for p in pages):
                    out["pages_note"] += (
                        " Pages with low_confidence: true are hard to read (mostly handwriting): expect errors."
                    )
            if row["text_ar"] is None:
                out["note"] = (
                    "The Centre holds this judgment as a scanned file; its machine reading is in pages."
                    if pages
                    else "The Centre holds this judgment as a scanned file only; its text has not been read yet."
                    if file
                    else "The Centre holds this judgment as a file only, not as typed text."
                )
            return out

    def gazette(self, ident):
        with self.connection() as conn:
            row = conn.execute("SELECT * FROM gazette_issue WHERE id=%s", (ident,)).fetchone()
            if row is None:
                raise GeneralNotFound("gazette issue not found")
            rows = conn.execute(
                "SELECT "
                + LAW_COLS
                + LAW_FROM
                + " JOIN gazette_entry e ON e.law_id=l.id WHERE e.issue_id=%s ORDER BY l.id",
                (ident,),
            ).fetchall()
            return _clean(
                {
                    **row,
                    "texts": [self._summary(law, self._source(conn, "law", law["id"])) for law in rows],
                    "url": self._url("gazette", ident),
                    "source_url": self._source(conn, "journal", ident),
                }
            )

    def gazette_search(self, query, limit=10, offset=0):
        limit, offset = _clamp(limit, 10, 1, 100), _clamp(offset, 0, 0, 1 << 30)
        where, args = "number ILIKE %s", ["%" + query + "%"]
        try:
            start, end = date_bound(query), date_bound(query, True)
        except ValueError:
            pass
        else:
            where += " OR published_on BETWEEN %s AND %s"
            args.extend([start, end])
        with self.connection() as conn:
            total = conn.execute("SELECT count(*) AS total FROM gazette_issue WHERE " + where, args).fetchone()["total"]
            rows = conn.execute(
                "SELECT id,number,published_on FROM gazette_issue WHERE "
                + where
                + " ORDER BY published_on DESC NULLS LAST,id DESC LIMIT %s OFFSET %s",
                [*args, limit, offset],
            ).fetchall()
            return {
                "gazette": [_clean({**row, "url": self._url("gazette", row["id"])}) for row in rows],
                "gazette_count": total,
            }

    def laws(self, **query):
        return self._list(False, list_params(**query))

    def gazettes(self, **query):
        return self._list(True, list_params(**query))

    def _list(self, issues, p):
        expr = "g.published_on" if issues else "coalesce(l.gazette_date,l.issued_on)"
        conditions, args = ([] if issues else [expr + " IS NOT NULL"]), []
        for bound, operator in (("start", ">="), ("end", "<=")):
            if p[bound] is not None:
                conditions.append(f"{expr} {operator} %s")
                args.append(p[bound])
        if not issues and p["kind"]:
            conditions.append("(l.kind=%s OR lower(k.name_en)=lower(%s))")
            args.extend([p["kind"], p["kind"]])
        where = " WHERE " + " AND ".join(conditions) if conditions else ""
        table = " FROM gazette_issue g" if issues else LAW_FROM
        direction = "ASC" if p["order"] == "oldest" else "DESC"
        ident = "g.id" if issues else "l.id"
        cols = (
            "g.*, (SELECT count(*) FROM gazette_entry e WHERE e.issue_id=g.id)::int AS text_count"
            if issues
            else LAW_COLS + ", " + expr + " AS date"
        )
        with self.connection() as conn:
            total = conn.execute("SELECT count(*) AS total" + table + where, args).fetchone()["total"]
            rows = conn.execute(
                "SELECT "
                + cols
                + table
                + where
                + f" ORDER BY {expr} {direction} NULLS LAST,{ident} {direction} LIMIT %s OFFSET %s",
                [*args, p["limit"], p["offset"]],
            ).fetchall()
            out = {"since": p["since"], "until": p["until"], "order": p["order"], "total": total, "offset": p["offset"]}
            if issues:
                out["issues"] = [
                    _clean(
                        {
                            **row,
                            "url": self._url("gazette", row["id"]),
                            "source": "scans" if row["id"] >= SCAN_ID_BASE else "centre",
                        }
                    )
                    for row in rows
                ]
            else:
                out["kind"] = p["kind"]
                out["laws"] = [
                    {
                        **self._summary(row),
                        **_clean({k: row[k] for k in ("gazette_number", "gazette_date", "gazette_issue_id", "date")}),
                    }
                    for row in rows
                ]
            if rows and p["offset"] + len(rows) < total:
                out["next_offset"] = p["offset"] + len(rows)
            return out


def create_router(service):
    router = APIRouter(prefix="/api/v1")
    provider = service if callable(service) else lambda: service

    def invoke(name, *args, **kwargs):
        return getattr(provider(), name)(*args, **kwargs)

    def respond(method, *args, **kwargs):
        try:
            return JSONResponse(method(*args, **kwargs), headers={"Cache-Control": CACHE_CONTROL})
        except GeneralNotFound as exc:
            return JSONResponse({"error": str(exc)}, status_code=404, headers={"Cache-Control": "no-store"})
        except ValueError as exc:
            return JSONResponse({"error": str(exc)}, status_code=400, headers={"Cache-Control": "no-store"})
        except (GeneralError, psycopg.Error, PoolTimeout):
            return JSONResponse(
                {"error": "service unavailable"}, status_code=503, headers={"Cache-Control": "no-store"}
            )

    @router.get("/")
    def root():
        return respond(invoke, "root")

    @router.get("/laws/")
    def laws(request: Request):
        return respond(invoke, "laws", **dict(request.query_params))

    @router.get("/gazette/")
    def gazettes(request: Request):
        return respond(invoke, "gazettes", **dict(request.query_params))

    def resource(method, ident, *args):
        if not re.fullmatch(r"[0-9]+", ident) or not 0 < int(ident) < (1 << 31):
            return JSONResponse({"error": "resource not found"}, status_code=404, headers={"Cache-Control": "no-store"})
        return respond(invoke, method, int(ident), *args)

    @router.get("/law/{ident}/")
    def law(ident: str):
        return resource("law", ident)

    @router.get("/law/{ident}/article/{number:path}/")
    def article(ident: str, number: str):
        return resource("article", ident, number)

    @router.get("/ruling/{ident}/")
    def ruling(ident: str):
        return resource("ruling", ident)

    @router.get("/gazette/{ident}/")
    def gazette(ident: str):
        return resource("gazette", ident)

    return router
