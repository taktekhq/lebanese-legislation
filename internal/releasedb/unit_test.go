package releasedb

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConninfoAndClientEnvKeepSecretsOffTheCommandLine(t *testing.T) {
	for dsn, want := range map[string]map[string]string{
		"host=/tmp dbname=lebleg":                                     {"host": "/tmp", "dbname": "lebleg"},
		"host=db user=u password='p w\\'x' dbname=d sslmode=disable":  {"host": "db", "user": "u", "password": "p w'x", "dbname": "d", "sslmode": "disable"},
		"postgresql://u:p%40ss@127.0.0.1:5433/lebleg?sslmode=require": {"host": "127.0.0.1", "port": "5433", "user": "u", "password": "p@ss", "dbname": "lebleg", "sslmode": "require"},
	} {
		got, err := conninfo(dsn)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s=%q, want %q", dsn, k, got[k], v)
			}
		}
	}
	env, db, err := clientEnv("host=h password=s3cret dbname=x")
	if err != nil || db != "x" {
		t.Fatal(err)
	}
	if !contains(env, "PGPASSWORD=s3cret") || !contains(env, "PGHOST=h") {
		t.Fatal("settings not passed as PG variables")
	}
	if _, err := conninfo("host='unclosed"); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

func TestSourceDetails(t *testing.T) {
	url, meta := sourceDetails("article", 7, "1  مكرر", "ar", map[string]any{"kind": "مرسوم اشتراعي", "number": "5", "issued_on": "2020-01-02"})
	if url != "https://lebaneselegislation.com/decree/7/article/1-%D9%85%D9%83%D8%B1%D8%B1/" {
		t.Fatalf("url %s", url)
	}
	if meta["identifier"] != "5" || meta["date"] != "2020-01-02" || meta["authoritative_language"] != "ar" || meta["english_unofficial"] != false {
		t.Fatalf("meta %v", meta)
	}
	url, meta = sourceDetails("law", 9, "title", "en", map[string]any{"kind": "الدستور"})
	if url != "https://lebaneselegislation.com/law/9/" || meta["english_unofficial"] != true {
		t.Fatalf("%s %v", url, meta)
	}
	if url, _ = sourceDetails("law", 9, "title", "ar", map[string]any{"kind": "قرار"}); url != "https://lebaneselegislation.com/text/9/" {
		t.Fatal(url)
	}
	if url, _ = sourceDetails("doctrine", 3, "", "ar", map[string]any{}); url != "https://lebaneselegislation.com/doctrine/3/" {
		t.Fatal(url)
	}
	// An explicit date wins; otherwise the first of the source dates.
	_, meta = sourceDetails("ruling", 1, "", "ar", map[string]any{"decided_on": "2001-01-01", "date": "1999-09-09", "low_confidence": true, "year": json.Number("2001")})
	if meta["date"] != "1999-09-09" || meta["low_confidence"] != true || meta["year"] != json.Number("2001") {
		t.Fatalf("%v", meta)
	}
	_, meta = sourceDetails("research", 1, "", "ar", map[string]any{"published_on": "2010-10-10", "model": nil})
	if meta["date"] != "2010-10-10" {
		t.Fatalf("%v", meta)
	}
	if _, ok := meta["model"]; ok {
		t.Fatal("a null field was kept")
	}
	// Go's unicode.IsSpace does not treat the ASCII file separator as space.
	if url, _ = sourceDetails("article", 1, "a\x1cb", "ar", map[string]any{"kind": "قانون"}); !strings.Contains(url, "a%1Cb") {
		t.Fatal(url)
	}
}

func validCalculated() (map[string]any, string) {
	source := "يغرم بمبلغ ألف ليرة"
	return map[string]any{
		"kind": "statutory_monetary_adjustment", "is_published_wording": false, "multiplier": json.Number("50"),
		"authority": map[string]any{"law_id": json.Number("330819"), "article": "1",
			"url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19"},
		"source_text_ar": source, "source_sha256": digest(source), "text_ar": "يغرم بمبلغ خمسين ألف ليرة",
	}, source
}

func TestCalculatedCurrentContract(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	obj, source := validCalculated()
	if err := validateCalculated(244565, "1", digest(source), obj, &source, asOf); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(m map[string]any){
		"kind":       func(m map[string]any) { m["kind"] = "other" },
		"published":  func(m map[string]any) { m["is_published_wording"] = true },
		"multiplier": func(m map[string]any) { m["multiplier"] = json.Number("51") },
		"authority":  func(m map[string]any) { m["authority"].(map[string]any)["article"] = "2" },
		"source":     func(m map[string]any) { m["source_text_ar"] = "x" },
		"unchanged":  func(m map[string]any) { m["text_ar"] = m["source_text_ar"] },
		"blank":      func(m map[string]any) { m["text_ar"] = " " },
	}
	for name, f := range cases {
		obj, source := validCalculated()
		f(obj)
		if err := validateCalculated(244565, "1", digest(source), obj, &source, asOf); !errors.Is(err, ErrCalculated) {
			t.Errorf("%s accepted", name)
		}
	}
	obj, source = validCalculated()
	if validateCalculated(1, "1", digest(source), obj, &source, asOf) == nil {
		t.Error("another law accepted")
	}
	if validateCalculated(244565, "1", digest(source), obj, &source, time.Date(2024, 9, 18, 0, 0, 0, 0, time.UTC)) == nil {
		t.Error("a date before the authority accepted")
	}
}

func TestQuoteMatchesPythonQuote(t *testing.T) {
	if got := quote("a b/c~-._%"); got != "a%20b%2Fc~-._%25" {
		t.Fatal(got)
	}
}
