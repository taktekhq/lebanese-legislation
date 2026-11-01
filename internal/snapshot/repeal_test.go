package snapshot

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"
	"time"
)

func TestSnapshotWholeLawRepealDB(t *testing.T) {
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
 CREATE TEMP TABLE legislation_article(law_id int,position int,number text,note text,text_ar text,text_fr text,in_force_from date,section_id int);
 CREATE TEMP TABLE article_in_force(law_id int,number text,position int,text_ar text,version text,state text,pending bool,valid_from date);
 CREATE TEMP TABLE council_ruling(law_id int,number text,effect text);
 CREATE TEMP TABLE amendment(law_id int,article_number text,action text,part text,status text,effective_on date);
 INSERT INTO legislation VALUES(244565,'in_force');
 INSERT INTO legislation_article VALUES(244565,1,'86',NULL,'source',NULL,NULL,NULL);
 INSERT INTO article_in_force VALUES(244565,'86',1,'source','1.0','in_force',false,NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, date string }{{"past", "'2020-01-01'"}, {"unknown date", "NULL"}, {"future", "'2999-01-01'"}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err = conn.Exec(ctx, `TRUNCATE amendment;INSERT INTO amendment VALUES(244565,NULL,'repeal',NULL,'applied',`+tc.date+`)`)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := conn.Query(ctx, articlesQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("missing original article")
			}
			var lawID, position int
			var number, source, version, state string
			var note, fr, current *string
			var section *int
			var inForce, validFrom *time.Time
			var pending, excluded bool
			err = rows.Scan(&lawID, &position, &number, &note, &source, &fr, &inForce, &section, &current, &version, &state, &pending, &validFrom, &excluded)
			if err != nil || !excluded || source != "source" || state != "in_force" {
				t.Fatalf("must suppress calculation and keep source state: excluded=%v source=%q state=%q err=%v", excluded, source, state, err)
			}
		})
	}
}
