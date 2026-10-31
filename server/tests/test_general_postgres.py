"""Resource compatibility against the actual exported PostgreSQL schema."""

import os
import re
import uuid
from importlib.resources import files

import psycopg
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict, make_conninfo

from lebleg.general import GeneralError, GeneralNotFound, GeneralService, create_router
from lebleg.search import SearchService


@pytest.fixture
def general():
    admin_dsn = os.getenv("RUNTIME_TEST_DSN") or os.getenv("LEBLEG_TEST_ADMIN_DSN")
    if not admin_dsn:
        pytest.skip("set RUNTIME_TEST_DSN or LEBLEG_TEST_ADMIN_DSN for disposable PostgreSQL integration")
    name = "general_" + uuid.uuid4().hex
    with psycopg.connect(admin_dsn, autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
    dsn = make_conninfo(**{**conninfo_to_dict(admin_dsn), "dbname": name})
    service = None
    try:
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
            conn.execute("INSERT INTO ruling_file(ruling_id,pages) VALUES(1,2)")
            conn.execute(
                "INSERT INTO ruling_page(ruling_id,n,text_ar,model,low_confidence) VALUES(1,1,'قراءة أصلية {كذا}','fixture',true)"
            )
            conn.execute("""INSERT INTO graph_edge(id,from_type,from_id,from_article,to_law_id,to_article,relation,status,source)
                VALUES(1,'legislation',2,'2',1,'1','amend','linked','fixture'),
                      (2,'ruling',1,NULL,1,'1','cites','linked','fixture')""")
            conn.execute("INSERT INTO possibly_affected VALUES(2,'2',1,'1')")
        search = SearchService(dsn)
        service = GeneralService(search, "https://example.test")
        yield service
    finally:
        if service:
            service.search_service.close()
        with psycopg.connect(admin_dsn, autocommit=True) as admin:
            admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name)))


def test_resource_metadata_original_text_and_complete_outline(general):
    law = general.law(1)
    assert law["title"] == "نص أصلي"
    assert law["title_en"] == "An unofficial title"
    assert law["kind_en"] == "Law"
    assert law["code"] == "Code name"
    assert law["article_count"] == 600
    assert len(law["articles"]) == 601
    assert law["source_url"] == "https://example.org/law/1"
    assert law["amended_by"][0]["law"]["id"] == 2
    assert general.law(2)["amended"][0]["law"]["id"] == 1
    article = general.article(1, "1")
    assert article["text_ar"] == "نص تعديل أصلي"
    assert article["centre_text_ar"] == "نص أصلي 1"
    assert article["centre_text_fr"] == "texte original"
    assert article["versions"][-1]["note"] == "source text not yet available"
    assert "text_ar" not in article["versions"][-1]
    assert article["centre_error"] == {"en": "Known error", "ar": "خطأ معروف"}
    assert article["changes"][0]["found_by"] == ["fixture"]
    assert article["possibly_affected_by"][0]["by"]["id"] == 2
    assert article["rulings_count"] == 1
    assert general.article(1, "601")["text_ar"] == "مادة مضافة"
    assert general.summary(1)["article_count"] == 600
    with pytest.raises(GeneralNotFound):
        general.article(1, "missing")
    with pytest.raises(GeneralNotFound):
        general.law(999)


def test_lists_dates_pagination_and_kind(general):
    first = general.laws(since="2020-02", until="2020", limit="1", order="oldest", kind="LAW")
    assert first["total"] == 2
    assert first["laws"][0]["id"] == 1
    assert first["laws"][0]["date"] == "2020-02-29"
    assert first["next_offset"] == 1
    assert first["laws"][0]["gazette_issue_id"] == 1
    last = general.laws(since="2020-02", until="2020", limit="1", offset="1", order="oldest")
    assert last["laws"][0]["id"] == 2
    assert "next_offset" not in last
    assert general.laws(until="2020-02")["total"] == 1
    assert general.gazettes(since="2020", until="2020")["total"] == 2
    assert general.gazettes()["issues"][-1]["source"] == "scans"
    assert general.gazette(1)["texts"][0]["id"] == 1
    assert general.gazette_search("2020-02")["gazette_count"] == 1
    assert general.gazette_search("2")["gazette"][0]["id"] == 1


def test_ruling_machine_reading_uncertainty_and_read_only(general):
    ruling = general.ruling(1)
    assert ruling["pages"][0] == {"n": 1, "text_ar": "قراءة أصلية {كذا}", "low_confidence": True}
    assert "expect errors" in ruling["pages_note"]
    assert ruling["pdf_url"] == "https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=1&type=list"
    with pytest.raises(GeneralError), general.connection() as conn:
        conn.execute("UPDATE legislation SET title='wrong' WHERE id=1")
    assert general.law(1)["title"] == "نص أصلي"


def test_all_resource_routes_against_snapshot(general):
    app = FastAPI()
    app.include_router(create_router(general))
    with TestClient(app) as client:
        for path in ("/", "/law/1/", "/law/1/article/1/", "/ruling/1/", "/gazette/1/", "/laws/", "/gazette/"):
            response = client.get("/api/v1" + path)
            assert response.status_code == 200, response.text
            assert response.headers["cache-control"].startswith("public,")
        assert client.get("/api/v1/ruling/999/").status_code == 404
        assert client.get("/api/v1/laws/?since=not-a-date").status_code == 400
