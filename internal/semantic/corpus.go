package semantic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// The article predicate intentionally matches site.answerArticle. In particular,
// French text is eligible only when the consolidated Arabic is the original.
const articlesSQL = `SELECT DISTINCT ON (f.law_id,f.number) f.law_id,f.number,f.text_ar,
 CASE WHEN f.text_ar=a.text_ar THEN coalesce(a.text_fr,'') ELSE '' END AS text_fr,l.title
 FROM article_in_force f JOIN legislation l ON l.id=f.law_id
 LEFT JOIN legislation_article a ON a.law_id=f.law_id AND a.position=f.position
 WHERE f.state='in_force' AND NOT f.pending AND l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
 AND f.text_ar IS NOT NULL AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
 ORDER BY f.law_id,f.number,f.position NULLS LAST`

const corpusSQL = `WITH articles AS (` + articlesSQL + `)
 SELECT 'article' AS kind,law_id AS source_id,number AS part,'ar' AS lang,title,text_ar AS body FROM articles
 UNION ALL SELECT 'article',law_id,number,'fr',title,text_fr FROM articles WHERE text_fr<>''
 UNION ALL SELECT 'law',l.id,v.part,'ar',l.title,v.body FROM legislation l
 CROSS JOIN LATERAL (VALUES ('title',l.title),('preamble',l.preamble),('explanatory_note',l.explanatory_note),('closing',l.closing)) v(part,body)
 WHERE l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE) AND coalesce(v.body,'')<>''
 UNION ALL SELECT 'law',l.id,'title','en',e.title_en,e.title_en FROM legislation_title_en e JOIN legislation l ON l.id=e.law_id
 WHERE l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
 UNION ALL SELECT 'ruling',id,'','ar',title,coalesce(text_ar,title) FROM ruling
 UNION ALL SELECT 'ruling',p.ruling_id,p.n::text,'ar',r.title,p.text_ar FROM ruling_page p JOIN ruling r ON r.id=p.ruling_id
 UNION ALL SELECT 'opinion',id,'','ar',coalesce(subject,''),coalesce(text_ar,subject,'') FROM opinion
 UNION ALL SELECT 'agreement',id,'','ar',title,coalesce(text_ar,title) FROM agreement
 UNION ALL SELECT 'research',id,'','ar',title,coalesce(text_ar,title) FROM research
 UNION ALL SELECT 'doctrine',id,'','ar',title,coalesce(abstract,title) FROM doctrine_article`

// Load rereads a reference from current canonical data. Indexed text is never used.
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
		// article_in_force's FULL JOIN on coalesced keys prevents PostgreSQL
		// pushing a lookup into the view. Bound both sides before applying the
		// identical latest-version, note, pending and valid_from rules.
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

// validTopic checks every quoted citation against current eligible article text.
// Rejecting the whole topic is conservative when a nightly corpus changes a law.
func validTopic(ctx context.Context, corpus *pgxpool.Pool, t *topics.Topic) error {
	if corpus == nil {
		return errors.New("canonical database unavailable")
	}
	for _, cite := range t.Articles {
		if _, ok := topics.ParseReading(cite.Article); ok { // a reading of the Gazette's pages (gazette_annex)
			f, found, err := topics.DB{Pool: corpus}.Article(cite.Law, cite.Article)
			if err != nil {
				return err
			}
			if !found || cite.Quote == "" || !strings.Contains(f.Text, cite.Quote) {
				return errors.New("topic citation no longer matches the Gazette reading")
			}
			continue
		}
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

// walkCorpus streams one row at a time in a deterministic order, from the
// document after after (none: from the start). The caller can stop by
// returning an error; it never materializes the corpus in Go memory. A resumed
// run starts after a document's key, not after a count of documents: a count
// shifts when the corpus changes between runs, and there is no OFFSET
// anywhere in the project (internal/cursor).
func walkCorpus(ctx context.Context, corpus *pgxpool.Pool, after DocKey, limit int, visit func(Document) error) error {
	// The walk is the SQL corpus in key order, then the embedded topics in
	// their own order. After a topic, the SQL part is done.
	afterTopic := after.Kind == "topic"
	var lim any
	if limit > 0 {
		lim = limit
	}
	cond, args := "", []any{lim}
	if afterTopic {
		cond = " AND false"
	} else if after != (DocKey{}) {
		cond, args = " AND (kind,source_id,part,lang) > ($2,$3,$4,$5)", append(args, after.Kind, after.ID, after.Part, after.Lang)
	}
	rows, err := corpus.Query(ctx, `SELECT kind,source_id,part,lang,title,body FROM (`+corpusSQL+`) corpus WHERE btrim(coalesce(body,''))<>''`+cond+` ORDER BY kind,source_id,part,lang LIMIT $1`, args...)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var d Document
		if err = rows.Scan(&d.Kind, &d.ID, &d.Part, &d.Lang, &d.Title, &d.Text); err != nil {
			rows.Close()
			return err
		}
		count++
		if err = visit(d); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if limit > 0 && count >= limit {
		return nil
	}
	skipping := afterTopic // up to and including the topic the walk resumes after
	for _, t := range topics.All() {
		if err := validTopic(ctx, corpus, t); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		for _, lang := range []string{"ar", "en"} {
			if skipping {
				skipping = DocKey{Kind: "topic", Part: t.Slug, Lang: lang} != after
				continue
			}
			if limit > 0 && count >= limit {
				return nil
			}
			if err = visit(topicDocument(t, lang)); err != nil {
				return err
			}
			count++
		}
	}
	return nil
}

// DocKey is a corpus document's place in the walk: kind, id, part, language.
type DocKey struct {
	Kind string
	ID   int
	Part string
	Lang string
}

// ParseDocKey reads "kind/id/part/lang", as -after takes it (part and lang may
// be empty: "law/244611//ar").
func ParseDocKey(s string) (DocKey, error) {
	f := strings.Split(s, "/")
	if len(f) != 4 || f[0] == "" {
		return DocKey{}, fmt.Errorf("document key %q: want kind/id/part/lang", s)
	}
	id, err := strconv.Atoi(f[1])
	if err != nil {
		return DocKey{}, fmt.Errorf("document key %q: id %q is not a number", s, f[1])
	}
	return DocKey{Kind: f[0], ID: id, Part: f[2], Lang: f[3]}, nil
}

// String is the key as ParseDocKey reads it.
func (k DocKey) String() string { return fmt.Sprintf("%s/%d/%s/%s", k.Kind, k.ID, k.Part, k.Lang) }
