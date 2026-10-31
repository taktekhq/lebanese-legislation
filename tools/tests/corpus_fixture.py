"""A small exported-schema corpus for integration tests, built without any service."""

import hashlib
import os
import re
import uuid
from importlib.resources import files

import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict, make_conninfo
from psycopg.types.json import Jsonb
from test_runtime import manifest

MODEL = "gemini-embedding-001"


def build_corpus(dsn):
    """Load the public schema, the fixture rows and a restored-release read model into dsn."""
    with psycopg.connect(dsn) as conn:
        schema = files("lebleg").joinpath("sql/public_schema.sql").read_text()
        schema = re.sub(r"^--.*$", "", schema, flags=re.MULTILINE)
        statements = [statement.strip() for statement in schema.split(";") if statement.strip()]
        for statement in statements:
            if "FOREIGN KEY" not in statement:
                conn.execute(statement)
        for statement in statements:
            if "FOREIGN KEY" in statement:
                conn.execute(statement)
        conn.execute("INSERT INTO legislation_kind(name_ar,name_en) VALUES('قانون','Law')")
        conn.execute("INSERT INTO court(name_ar,name_en) VALUES('محكمة','Court')")
        conn.execute("""INSERT INTO legislation(id,kind,number,issued_on,title,status,gazette_date,preamble,title_en)
            VALUES(1,'قانون','10','2019-01-01','نص أصلي','in_force','2020-02-29','مقدمة',NULL),
                  (2,'قانون','11','2020-03-01','نص ثان','in_force',NULL,NULL,NULL),
                  (3,'قانون','12',NULL,'غير مؤرخ','in_force',NULL,NULL,NULL)""")
        conn.execute("INSERT INTO legislation_title_en VALUES(1,'An unofficial title','fixture')")
        conn.execute(
            "INSERT INTO gazette_issue VALUES(1,'2','2020-02-29'),(2,'3','2020-03-01'),(2000000001,'4',NULL)"
        )
        conn.execute("INSERT INTO gazette_entry VALUES(1,1,NULL),(2,2,NULL)")
        conn.execute("INSERT INTO code(slug,law_id,name_en) VALUES('fixture',1,'Code name')")
        conn.execute("INSERT INTO legislation_section(id,law_id,position,depth,heading) VALUES(1,1,1,1,'باب')")
        conn.execute("""INSERT INTO legislation_article(law_id,position,number,section_id,note,text_ar,text_fr)
            SELECT 1,n,n::text,1,NULL,'نص أصلي ' || n,'texte original' FROM generate_series(1,600) n""")
        conn.execute("""INSERT INTO article_version(law_id,article_number,major,minor,valid_from,state,text_ar,text_source,note)
            VALUES(1,'1',1,0,'2019-01-01','in_force','نص أصلي 1','centre',NULL),
                  (1,'1',2,0,'2020-03-01','in_force','نص تعديل أصلي','amendment','uncertain date'),
                  (1,'1',3,0,'2021-01-01','in_force',NULL,'pending','source text not yet available'),
                  (1,'601',1,0,'2020-01-01','in_force','مادة مضافة','amendment',NULL)""")
        conn.execute("INSERT INTO centre_error VALUES(1,'1','Known error','خطأ معروف')")
        conn.execute(
            "INSERT INTO source_page(doc_type,centre_id,url) VALUES('law',1,'https://example.org/law/1'),('journal',1,'https://example.org/gazette/1'),('ruling',1,'https://example.org/ruling/1')"
        )
        conn.execute("INSERT INTO ruling(id,court,number,year,title) VALUES(1,'محكمة','4',2020,'حكم أصلي')")
        conn.execute("INSERT INTO ruling_file(ruling_id,pages,bytes) VALUES(1,2,1234)")
        conn.execute(
            "INSERT INTO ruling_page(ruling_id,n,text_ar,model,low_confidence) VALUES(1,1,'قراءة أصلية {كذا}','fixture',true)"
        )
        conn.execute("""INSERT INTO graph_edge(id,from_type,from_id,from_article,to_law_id,to_article,relation,status,source)
            VALUES(1,'legislation',2,'2',1,'1','amend','linked','fixture'),
                  (2,'ruling',1,NULL,1,'1','cites','linked','fixture')""")
        conn.execute("INSERT INTO possibly_affected VALUES(2,'2',1,'1')")
        conn.execute("CREATE EXTENSION IF NOT EXISTS vector")
        conn.execute(
            "CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text,source_url text,metadata jsonb,PRIMARY KEY(kind,source_id,part,lang))"
        )
        conn.execute(
            "CREATE TABLE semantic_chunks(model text,kind text,source_id int,part text,lang text,title text,text text,hash text,chunk_start int,chunk_end int,embedding halfvec(768))"
        )
        conn.execute("CREATE TABLE lebleg_release(release_id text,manifest jsonb)")
        conn.execute("INSERT INTO legislation_article(law_id,position,number,text_ar) VALUES(1,602,'12 مكرر/ب','نص فرعي')")
        m = manifest()
        conn.execute("INSERT INTO lebleg_release VALUES(%s,%s)", (m["release_id"], Jsonb(m)))
        for kind, sid, part, text, year in [
            ("law", 1, "title", "نص أصلي", 2019),
            ("law", 1, "preamble", "مقدمة أصلية", 2019),
            ("law", 2, "title", "نص ثان", 2020),
            ("article", 1, "1", "نص تعديل أصلي", 2019),
        ]:
            title = "نص أصلي" if sid == 1 else "نص ثان"
            digest = hashlib.sha256(text.encode()).hexdigest()
            conn.execute(
                "INSERT INTO source_documents VALUES(%s,%s,%s,'ar',%s,%s,%s,'https://example.org/source',%s)",
                (kind, sid, part, title, text, digest, Jsonb({"issued_on": f"{year}-01-01", "source_type": "law"})),
            )
            conn.execute(
                "INSERT INTO semantic_chunks VALUES(%s,%s,%s,%s,'ar',%s,%s,%s,0,%s,%s::halfvec)",
                (MODEL, kind, sid, part, title, text, digest, len(text), "[" + ",".join(["1"] * 768) + "]"),
            )
    return m


@pytest.fixture
def corpus():
    """A disposable database holding the fixture corpus; yields its DSN."""
    admin_dsn = os.getenv("RUNTIME_TEST_DSN") or os.getenv("LEBLEG_TEST_ADMIN_DSN")
    if not admin_dsn:
        pytest.skip("set RUNTIME_TEST_DSN or LEBLEG_TEST_ADMIN_DSN for disposable PostgreSQL integration")
    name = "corpus_" + uuid.uuid4().hex
    with psycopg.connect(admin_dsn, autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
    dsn = make_conninfo(**{**conninfo_to_dict(admin_dsn), "dbname": name})
    try:
        build_corpus(dsn)
        yield dsn
    finally:
        with psycopg.connect(admin_dsn, autocommit=True) as admin:
            admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name)))
