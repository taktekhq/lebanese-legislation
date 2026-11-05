package topics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a Source over the LebaneseLegislation database. An article's text is
// the wording the site shows: the latest version that has a text, else the
// Centre's own.
type DB struct{ Pool *pgxpool.Pool }

const factsSQL = `SELECT coalesce(
		(SELECT v.text_ar FROM article_version v WHERE v.law_id = a.law_id AND v.article_number = a.number AND v.text_ar IS NOT NULL
		 ORDER BY v.major DESC, v.minor DESC LIMIT 1), a.text_ar),
	coalesce(a.note, ''), l.title, coalesce(l.number, ''), l.issued_on,
	coalesce((SELECT string_agg(coalesce(b.number, '') || ' ' || coalesce(to_char(b.issued_on, 'DD/MM/YYYY'), '') || ' ' || b.title, E'\n')
	          FROM article_version v JOIN amendment m ON m.id = v.amendment_id JOIN legislation b ON b.id = m.by_law_id
	          WHERE v.law_id = a.law_id AND v.article_number = a.number), '')
	FROM legislation_article a JOIN legislation l ON l.id = a.law_id WHERE a.law_id = $1 AND a.number = $2`

// readingSQL is a Gazette reading's text and facts (reading.go): an annexed
// table's row (its violation, then its notes) or an article's first wording.
const readingSQL = `SELECT CASE g.kind WHEN 'table_row' THEN g.violation || CASE WHEN coalesce(g.notes, '') <> '' THEN E'\n\n' || g.notes ELSE '' END
		ELSE g.text_ar END,
	concat_ws(E'\n', g.table_title, 'الفئة ' || g.category, 'الرقم ' || g.serial, g.article, g.extra_articles, 'النقاط ' || nullif(g.points, ''),
		g.number, 'الجريدة الرسمية العدد ' || g.gazette_issue, to_char(g.gazette_date, 'DD/MM/YYYY'), g.gazette_pages),
	l.title, coalesce(l.number, ''), l.issued_on
	FROM gazette_annex g JOIN legislation l ON l.id = g.law_id WHERE g.law_id = $1 AND g.anchor = $2`

// Article implements Source.
func (d DB) Article(law int, number string) (Facts, bool, error) {
	if _, ok := ParseReading(number); ok {
		return d.reading(law, number)
	}
	var text, note, title, num, amending string
	var issued *time.Time
	err := d.Pool.QueryRow(context.Background(), factsSQL, law, number).Scan(&text, &note, &title, &num, &issued, &amending)
	if errors.Is(err, pgx.ErrNoRows) {
		return Facts{}, false, nil
	}
	if err != nil {
		return Facts{}, false, err
	}
	extra := []string{note, title, num, amending}
	if issued != nil {
		extra = append(extra, issued.Format("2/1/2006"), fmt.Sprint(issued.Year()))
	}
	return Facts{Text: text, Extra: strings.Join(extra, "\n")}, true, nil
}

// reading looks up a Gazette reading. An older database has no gazette_annex:
// the reading does not exist there.
func (d DB) reading(law int, anchor string) (Facts, bool, error) {
	var text, extra, title, num string
	var issued *time.Time
	err := d.Pool.QueryRow(context.Background(), readingSQL, law, anchor).Scan(&text, &extra, &title, &num, &issued)
	if errors.Is(err, pgx.ErrNoRows) || isUndefinedTable(err) {
		return Facts{}, false, nil
	}
	if err != nil {
		return Facts{}, false, err
	}
	all := []string{extra, title, num, decimals(text)}
	if issued != nil {
		all = append(all, issued.Format("2/1/2006"), fmt.Sprint(issued.Year()))
	}
	return Facts{Text: text, Extra: strings.Join(all, "\n")}, true, nil
}

// isUndefinedTable is PostgreSQL's 42P01, a table the database lacks.
func isUndefinedTable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42P01"
}
