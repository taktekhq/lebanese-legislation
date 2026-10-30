package topics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct{ Pool *pgxpool.Pool }

const factsSQL = `SELECT coalesce(
		(SELECT v.text_ar FROM article_version v WHERE v.law_id = a.law_id AND v.article_number = a.number AND v.text_ar IS NOT NULL
		 ORDER BY v.major DESC, v.minor DESC LIMIT 1), a.text_ar),
	coalesce(a.note, ''), l.title, coalesce(l.number, ''), l.issued_on,
	coalesce((SELECT string_agg(coalesce(b.number, '') || ' ' || coalesce(to_char(b.issued_on, 'DD/MM/YYYY'), '') || ' ' || b.title, E'\n')
	          FROM article_version v JOIN amendment m ON m.id = v.amendment_id JOIN legislation b ON b.id = m.by_law_id
	          WHERE v.law_id = a.law_id AND v.article_number = a.number), '')
	FROM legislation_article a JOIN legislation l ON l.id = a.law_id WHERE a.law_id = $1 AND a.number = $2`

func (d DB) Article(law int, number string) (Facts, bool, error) {
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
