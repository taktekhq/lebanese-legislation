package calculatedcurrent

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"
)

// LEBLEG_TEST_DSN must name a disposable PostgreSQL database.
func TestExportWholeLawRepealDB(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to disposable PostgreSQL")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	_, err = conn.Exec(ctx, `CREATE TEMP TABLE legislation(id int,status text);
 CREATE TEMP TABLE legislation_article(law_id int,number text,position int,in_force_from date);
 CREATE TEMP TABLE article_in_force(law_id int,number text,position int,text_ar text,state text,pending bool,valid_from date);
 CREATE TEMP TABLE council_ruling(law_id int,number text,effect text);
 CREATE TEMP TABLE amendment(law_id int,article_number text,action text,part text,status text,effective_on date);
 INSERT INTO legislation VALUES(244565,'in_force');
 INSERT INTO legislation_article VALUES(244565,'86',1,NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	source := ""
	for _, r := range sources(t) {
		if r.Number == "86" {
			source = r.TextAr
		}
	}
	_, err = conn.Exec(ctx, `INSERT INTO article_in_force VALUES(244565,'86',1,$1,'in_force',false,NULL)`, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, article, action, part, status, date string
		want                                      int
	}{
		{"past", "NULL", "repeal", "NULL", "applied", "'2020-01-01'", 0},
		{"unknown date", "NULL", "repeal", "NULL", "applied", "NULL", 0},
		{"future", "NULL", "repeal", "NULL", "applied", "'2999-01-01'", 0},
		{"review", "NULL", "repeal", "NULL", "review", "NULL", 1},
		{"partial", "NULL", "repeal", "'paragraph'", "applied", "NULL", 1},
		{"single article", "'87'", "repeal", "NULL", "applied", "NULL", 1},
		{"replace", "NULL", "replace", "NULL", "applied", "NULL", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err = conn.Exec(ctx, `TRUNCATE amendment;INSERT INTO amendment VALUES(244565,`+tc.article+`,'`+tc.action+`',`+tc.part+`,'`+tc.status+`',`+tc.date+`)`)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := conn.Query(ctx, exportQuery)
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			n, err := writeRows(rows, &b)
			rows.Close()
			if err != nil || n != tc.want {
				t.Fatalf("export %d rows, want %d: %v", n, tc.want, err)
			}
		})
	}
	// Public source documents use the first position for repeated numbers.
	_, err = conn.Exec(ctx, `TRUNCATE amendment;INSERT INTO legislation_article VALUES(244565,'86',2,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO article_in_force VALUES(244565,'86',2,$1,'in_force',false,NULL)`, source)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, exportQuery)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	n, err := writeRows(rows, &b)
	rows.Close()
	if err != nil || n != 1 {
		t.Fatalf("duplicate export=%d %v", n, err)
	}
	var row exportRow
	if err = json.Unmarshal(b.Bytes(), &row); err != nil || row.Position != 1 {
		t.Fatalf("wrong canonical position: %+v %v", row, err)
	}
	// A changed first source must not be replaced by a matching later duplicate.
	_, err = conn.Exec(ctx, `UPDATE article_in_force SET text_ar=text_ar || ' changed' WHERE position=1`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = conn.Query(ctx, exportQuery)
	if err != nil {
		t.Fatal(err)
	}
	b.Reset()
	n, err = writeRows(rows, &b)
	rows.Close()
	if err != nil || n != 0 {
		t.Fatalf("later duplicate escaped canonical source: %d %v", n, err)
	}

}
