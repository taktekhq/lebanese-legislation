"""Read-only exact-first retrieval of original source passages."""

import argparse
import hashlib
import json
import os
import re
import unicodedata
from contextlib import contextmanager
from dataclasses import dataclass
from urllib.parse import urlsplit

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import ConnectionPool

from .embedding import MODEL, Gemini, ProviderError


class SearchError(ValueError):
    pass


@dataclass(frozen=True)
class CitationTarget:
    kind: str | None
    number: str | None
    date: str | None = None
    year: str | None = None


def _citation_normalize(value):
    value = "".join(str(unicodedata.digit(c)) if c.isdigit() else c for c in value)
    value = value.translate(str.maketrans("أإآٱ", "اااا"))
    value = re.sub(r"[ًٌٍَُِّْـ]", "", value)
    return re.sub(r"(?<!\w)ال", "", value).lower()


_INSTRUMENT = r"(?:decree[ -]law|d[ée]cret[ -]loi|d[ée]cret législatif|cabinet decision|decision of (?:the )?council of ministers|مرسوم\s+اشتراعي|قرار\s+مجلس\s+وزراء|loi|décret|decret|décision|arrêt|arret|judgment|حكم|law|decree|decision|قانون|مرسوم|قرار)"
_DATE = r"(?:\d{4}-\d{2}-\d{2}|\d{1,2}[/.-]\d{1,2}[/.-]\d{4})"
_CITATION = re.compile(
    r"(?<!\w)(?P<instrument>"
    + _INSTRUMENT
    + r")\s*(?:no\.?|number|n[°º]|numéro|رقم)?\s*(?P<number>\d+)(?:\s*/\s*(?P<year>\d{4})(?!\d))?(?:\s+(?:of|du|سنة|لعام|لسنة)\s+(?P<word_year>\d{4})(?![\d/.-]))?(?:[\s,;:()\[\]]+(?:(?:issued|on|dated|of|du|صادر|بتاريخ|تاريخ|في)\s+)*(?P<date>"
    + _DATE
    + r"))?",
    re.IGNORECASE,
)


def _date_value(value):
    if re.fullmatch(r"\d{4}-\d{2}-\d{2}", value):
        return value
    day, month, year = re.split(r"[/.-]", value)
    return f"{year}-{int(month):02d}-{int(day):02d}"


def citation_target(query):
    query = _citation_normalize(query)
    match = _CITATION.search(query)
    if match:
        name = re.sub(r"\s+", " ", match["instrument"])
        if name in (
            "decree-law",
            "decree law",
            "décret-loi",
            "décret loi",
            "decret-loi",
            "decret loi",
            "décret législatif",
            "decret législatif",
            "مرسوم اشتراعي",
        ):
            kind = "decree-law"
        elif name in (
            "cabinet decision",
            "decision of council of ministers",
            "decision of the council of ministers",
            "قرار مجلس وزراء",
        ):
            kind = "cabinet-decision"
        else:
            kind = {
                "قانون": "law",
                "مرسوم": "decree",
                "قرار": "decision",
                "loi": "law",
                "décret": "decree",
                "decret": "decree",
                "décision": "decision",
                "arrêt": "ruling",
                "arret": "ruling",
                "judgment": "ruling",
                "حكم": "ruling",
            }.get(name, name)
        return CitationTarget(
            kind,
            match["number"],
            _date_value(match["date"]) if match["date"] else None,
            (match["year"] or match["word_year"]),
        )
    date = re.search(_DATE, query)
    return CitationTarget(None, None, _date_value(date[0])) if date else None


# Normalize metadata for matching only. Original published source text is never changed.
_SOURCE_TYPE = "lower(regexp_replace(translate(regexp_replace(coalesce(metadata->>'source_type',kind),'[ًٌٍَُِّْـ]','','g'),'أإآٱ','اااا'),'(^|[[:space:]])ال','\\1','g'))"


def _citation_conditions(target):
    conditions = []
    params = []
    if target.kind:
        label = {
            "law": "قانون",
            "decree": "مرسوم",
            "decree-law": "مرسوم اشتراعي",
            "decision": "قرار",
            "cabinet-decision": "قرار مجلس وزراء",
            "ruling": "حكم",
        }[target.kind]
        if target.kind == "ruling":
            conditions.append("(kind='ruling' OR " + _SOURCE_TYPE + " LIKE %s)")
            params.append(label + "%")
        else:
            conditions.append("(" + _SOURCE_TYPE + " LIKE %s OR " + _SOURCE_TYPE + "=%s)")
            params.extend([label + "%", target.kind])
        if target.kind == "decree":
            conditions.append(_SOURCE_TYPE + " NOT LIKE 'مرسوم اشتراعي%%'")
    if target.number:
        conditions.append("coalesce(metadata->>'identifier',metadata->>'number','')=%s")
        params.append(target.number)
    if target.date:
        conditions.append("coalesce(metadata->>'issued_on',metadata->>'date','')=%s")
        params.append(target.date)
    if target.year:
        conditions.append(
            "coalesce(metadata->>'year',substring(coalesce(metadata->>'issued_on',metadata->>'date','') from 1 for 4))=%s"
        )
        params.append(target.year)
    return conditions, params


def validate_passage(row):
    source = row["source_text"]
    start = row["chunk_start"]
    end = row["chunk_end"]
    digest = hashlib.sha256(source.encode("utf-8")).hexdigest()
    return (
        digest == row["source_hash"] == row["hash"]
        and isinstance(start, int)
        and isinstance(end, int)
        and 0 <= start < end <= len(source)
        and end - start <= 900
        and source[start:end] == row["text"]
    )


def _public_passage(row, exact=False):
    url = row.get("source_url")
    if url:
        parsed = urlsplit(url)
        if parsed.scheme not in ("https", "http") or not parsed.netloc or parsed.username or parsed.password:
            url = None
    ref = {k: row[k] for k in ("kind", "source_id", "part", "lang")}
    identity = json.dumps([*ref.values(), row["hash"], row["chunk_start"]], ensure_ascii=False)
    return {
        "id": hashlib.sha256(identity.encode()).hexdigest()[:24],
        **ref,
        "title": row["title"],
        "text": row["text"],
        "source_url": url,
        "source_hash": row["hash"],
        "chunk_start": row["chunk_start"],
        "chunk_end": row["chunk_end"],
        "exact": exact,
        "metadata": row.get("metadata") or {},
    }


class SearchService:
    def __init__(self, dsn, provider=None):
        self.provider = provider
        self.pool = ConnectionPool(
            dsn,
            min_size=0,
            max_size=4,
            timeout=5,
            kwargs={
                "row_factory": dict_row,
                "options": "-c default_transaction_read_only=on -c statement_timeout=10000",
            },
            open=True,
        )

    def close(self):
        self.pool.close()

    @contextmanager
    def connection(self):
        with self.pool.connection() as conn, conn.transaction():
            conn.execute("SET TRANSACTION READ ONLY")
            yield conn

    def health(self):
        with self.connection() as conn:
            return bool(
                conn.execute(
                    "SELECT EXISTS(SELECT 1 FROM restore_receipt t JOIN lebleg_release r ON t.manifest->>'release_id'=r.release_id WHERE t.singleton) AS ready"
                ).fetchone()["ready"]
            )

    def search(self, query, limit=8, semantic=True, select=False):
        if not isinstance(query, str) or not query.strip() or len(query) > 500 or not 1 <= limit <= 16:
            raise SearchError("query must contain 1 to 500 characters and limit 1 to 16")
        with self.connection() as conn:
            release = conn.execute(
                "SELECT r.manifest FROM lebleg_release r JOIN restore_receipt t ON t.manifest->>'release_id'=r.release_id WHERE t.singleton LIMIT 1"
            ).fetchone()
            if not release:
                raise SearchError("release identity is missing")
            embedding = release["manifest"]["embedding"]
            if embedding["model"] != MODEL or embedding["dimensions"] != 768:
                raise SearchError("incompatible embedding model")
            target = citation_target(query)
            conditions = []
            params = []
            if target:
                conditions, params = _citation_conditions(target)
            else:
                conditions.append("(title ILIKE %s ESCAPE '\\' OR text ILIKE %s ESCAPE '\\')")
                escaped = query.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
                params.extend(["%" + escaped + "%"] * 2)
            rows = conn.execute(
                "SELECT * FROM source_documents WHERE "
                + " AND ".join(conditions)
                + " ORDER BY kind,source_id,part,lang LIMIT %s",
                params + [limit],
            ).fetchall()
            passages = []
            for row in rows:
                full = row["text"]
                digest = hashlib.sha256(full.encode()).hexdigest()
                if digest != row["hash"] or not full:
                    continue
                start = 0
                if not target:
                    pos = full.casefold().find(query.casefold())
                    if pos >= 0:
                        start = max(0, pos - 150)
                end = min(len(full), start + 900)
                chunk = {
                    **row,
                    "source_text": full,
                    "source_hash": row["hash"],
                    "text": full[start:end],
                    "chunk_start": start,
                    "chunk_end": end,
                }
                passages.append(_public_passage(chunk, True))
            # An explicit citation never falls back to a different source.
            if not target and semantic and self.provider and len(passages) < limit:
                if self.provider.model != embedding["model"]:
                    raise SearchError("incompatible embedding model")
                vector = self.provider.embed([query], query=True)[0]
                literal = "[" + ",".join(str(x) for x in vector) + "]"
                candidates = conn.execute(
                    """SELECT c.*,d.text AS source_text,d.hash AS source_hash,d.source_url,d.metadata
                    FROM semantic_chunks c JOIN source_documents d USING(kind,source_id,part,lang)
                    WHERE c.model=%s ORDER BY c.embedding <=> %s::halfvec LIMIT %s""",
                    (MODEL, literal, limit * 4),
                ).fetchall()
                refs = {(p["kind"], p["source_id"], p["part"], p["lang"]) for p in passages}
                for row in candidates:
                    ref = tuple(row[k] for k in ("kind", "source_id", "part", "lang"))
                    if ref in refs or not validate_passage(row):
                        continue
                    passages.append(_public_passage(row))
                    refs.add(ref)
                    if len(passages) >= limit:
                        break
        if select:
            if not self.provider:
                raise SearchError("Gemini API key is required for source selection")
            if passages:
                passages = self.provider.select(query, passages)
        return {
            "label": "Original source passages, not legal conclusions",
            "english_notice": "English titles are unofficial discovery aids. Arabic source text is authoritative.",
            "passages": passages,
        }


def main(argv=None):
    parser = argparse.ArgumentParser(description="Retrieve original source passages")
    parser.add_argument("--database-url", default=os.getenv("DATABASE_URL"))
    parser.add_argument("--query", required=True)
    parser.add_argument("--limit", type=int, default=8)
    parser.add_argument("--no-semantic", action="store_true")
    parser.add_argument("--select", action="store_true")
    args = parser.parse_args(argv)
    if not args.database_url:
        parser.error("DATABASE_URL is required")
    service = None
    try:
        key = os.getenv("GEMINI_API_KEY")
        service = SearchService(args.database_url, Gemini(key) if key else None)
        print(json.dumps(service.search(args.query, args.limit, not args.no_semantic, args.select), ensure_ascii=False))
        return 0
    except (SearchError, ProviderError, psycopg.Error):
        parser.exit(1, "search failed; check configuration and release readiness\n")
    finally:
        if service:
            service.close()
