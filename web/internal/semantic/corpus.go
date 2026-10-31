package semantic

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/web/internal/site/topics"
)

const articlesSQL = `SELECT DISTINCT ON (f.law_id,f.number) f.law_id,f.number,f.text_ar,
 CASE WHEN f.text_ar=a.text_ar THEN coalesce(a.text_fr,'') ELSE '' END AS text_fr,l.title
 FROM article_in_force f JOIN legislation l ON l.id=f.law_id
 LEFT JOIN legislation_article a ON a.law_id=f.law_id AND a.position=f.position
 WHERE f.state='in_force' AND NOT f.pending AND l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
 AND f.text_ar IS NOT NULL AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
 ORDER BY f.law_id,f.number,f.position NULLS LAST`

func Load(ctx context.Context, corpus *pgxpool.Pool, ref Document) (Document, error) {
	if ref.Kind == "topic" {
		if ref.Lang != "ar" && ref.Lang != "en" {
			return Document{}, errors.New("unsupported topic language")
		}
		t := topics.Get(ref.Part)
		if t == nil {
			return Document{}, pgx.ErrNoRows
		}
		if err := validTopic(ctx, corpus, t); err != nil {
			return Document{}, err
		}
		return topicDocument(t, ref.Lang), nil
	}
	if corpus == nil {
		return Document{}, errors.New("canonical database unavailable")
	}
	var query string
	args := []any{ref.ID}
	switch ref.Kind {
	case "article":
		if ref.Lang != "ar" && ref.Lang != "fr" {
			return Document{}, pgx.ErrNoRows
		}
		body := "coalesce(w.text_ar,a.text_ar)"
		if ref.Lang == "fr" {
			body = "CASE WHEN coalesce(w.text_ar,a.text_ar)=a.text_ar THEN coalesce(a.text_fr,'') ELSE '' END"
		}

		query = `WITH original AS (SELECT * FROM legislation_article WHERE law_id=$1 AND number=$2),
 latest AS (SELECT * FROM article_version WHERE law_id=$1 AND article_number=$2 ORDER BY major DESC,minor DESC LIMIT 1),
 worded AS (SELECT * FROM article_version WHERE law_id=$1 AND article_number=$2 AND text_ar IS NOT NULL ORDER BY major DESC,minor DESC LIMIT 1)
 SELECT l.title,` + body + ` FROM original a FULL JOIN latest v ON v.law_id=a.law_id AND v.article_number=a.number
 LEFT JOIN worded w ON w.law_id=coalesce(a.law_id,v.law_id) AND w.article_number=coalesce(a.number,v.article_number)
 JOIN legislation l ON l.id=coalesce(a.law_id,v.law_id)
 WHERE CASE WHEN a.note ~ '^\s*مادة ملغا' THEN 'repealed' ELSE coalesce(v.state,'in_force') END='in_force'
 AND NOT coalesce(v.state='in_force' AND v.text_ar IS NULL AND (w.major IS NULL OR w.major<v.major),false)
 AND l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE) AND coalesce(w.text_ar,a.text_ar) IS NOT NULL
 AND (v.valid_from IS NULL OR v.valid_from<=CURRENT_DATE) ORDER BY a.position NULLS LAST LIMIT 1`

		args = append(args, ref.Part)
	case "law":
		if ref.Lang == "en" && ref.Part == "title" {
			query = `SELECT e.title_en,e.title_en FROM legislation l JOIN legislation_title_en e ON e.law_id=l.id WHERE l.id=$1 AND l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)`
		} else if ref.Lang == "ar" {
			column := map[string]string{"title": "title", "preamble": "preamble", "explanatory_note": "explanatory_note", "closing": "closing"}[ref.Part]
			if column != "" {
				query = `SELECT title,coalesce(` + column + `,'') FROM legislation WHERE id=$1 AND status<>'repealed' AND (issued_on IS NULL OR issued_on<=CURRENT_DATE)`
			}
		}
	case "ruling":
		if ref.Lang == "ar" {
			if ref.Part == "" {
				query = `SELECT title,coalesce(text_ar,title) FROM ruling WHERE id=$1`
			} else {
				query = `SELECT r.title,p.text_ar FROM ruling_page p JOIN ruling r ON r.id=p.ruling_id WHERE p.ruling_id=$1 AND p.n::text=$2`
				args = append(args, ref.Part)
			}
		}
	case "opinion":
		if ref.Lang == "ar" && ref.Part == "" {
			query = `SELECT coalesce(subject,''),coalesce(text_ar,subject,'') FROM opinion WHERE id=$1`
		}
	case "agreement":
		if ref.Lang == "ar" && ref.Part == "" {
			query = `SELECT title,coalesce(text_ar,title) FROM agreement WHERE id=$1`
		}
	case "research":
		if ref.Lang == "ar" && ref.Part == "" {
			query = `SELECT title,coalesce(text_ar,title) FROM research WHERE id=$1`
		}
	case "doctrine":
		if ref.Lang == "ar" && ref.Part == "" {
			query = `SELECT title,coalesce(abstract,title) FROM doctrine_article WHERE id=$1`
		}
	}
	if query == "" {
		return Document{}, errors.New("unsupported semantic source reference")
	}
	d := ref
	err := corpus.QueryRow(ctx, query, args...).Scan(&d.Title, &d.Text)
	if err == nil && strings.TrimSpace(d.Text) == "" {
		err = pgx.ErrNoRows
	}
	return d, err
}

func validTopic(ctx context.Context, corpus *pgxpool.Pool, t *topics.Topic) error {
	if corpus == nil {
		return errors.New("canonical database unavailable")
	}
	for _, cite := range t.Articles {
		d, err := Load(ctx, corpus, Document{Kind: "article", ID: cite.Law, Part: cite.Article, Lang: "ar"})
		if err != nil {
			return err
		}
		if cite.Quote == "" || !strings.Contains(d.Text, cite.Quote) {
			return errors.New("topic citation no longer matches current law")
		}
	}
	return nil
}

func topicDocument(t *topics.Topic, lang string) Document {
	c := t.Lang(lang)
	parts := []string{c.Title, c.Question, c.Description, c.Answer.Text}
	for _, s := range c.Sections {
		parts = append(parts, s.Heading)
		for _, p := range s.Paras {
			parts = append(parts, p.Text)
		}
		if s.Table != nil {
			parts = append(parts, s.Table.Caption, strings.Join(s.Table.Head, " | "))
			for _, r := range s.Table.Rows {
				parts = append(parts, strings.Join(r.Cells, " | "))
			}
		}
	}
	return Document{Kind: "topic", Part: t.Slug, Lang: lang, Title: c.Title, Text: strings.Join(parts, "\n\n")}
}
