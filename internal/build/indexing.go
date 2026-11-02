package build

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/indexing"
)

// What search engines are told about each page (internal/indexing):
// noindex_page lists the pages to skip, page_state when each page last
// changed. Both feed the sitemaps and the pages' robots tag.

type stateRow struct {
	kind string
	id   int
	hash string
	at   time.Time
}

// priorState keeps page_state of the database about to be replaced, so a
// page whose content did not change keeps its changed_at across rebuilds.
func (b *builder) priorState() error {
	if err := b.readPriorEdges(); err != nil {
		return err
	}
	var ok bool
	if err := b.conn.QueryRow(b.ctx, `SELECT to_regclass('public.page_state') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		return nil // first build, or a database from before page_state
	}
	rows, err := b.conn.Query(b.ctx, `SELECT kind, id, content_hash, changed_at FROM page_state`)
	if err != nil {
		return nil
	}
	b.prior, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (s stateRow, err error) {
		err = r.Scan(&s.kind, &s.id, &s.hash, &s.at)
		return
	})
	return err
}

// shownArticle is the wording an article page prints: the latest version
// that has text, else the Centre's (Article.Shown in internal/site).
const shownArticle = `SELECT a.law_id, a."position", coalesce(v.text_ar, a.text_ar)
	FROM legislation_article a
	LEFT JOIN (SELECT DISTINCT ON (law_id, article_number) law_id, article_number, text_ar
	           FROM article_version WHERE text_ar IS NOT NULL ORDER BY law_id, article_number, major DESC, minor DESC) v
	  ON v.law_id = a.law_id AND v.article_number = a.number`

// englishKind widens the kind check of a database made before the kind
// english existed, so "-only indexing" and "-only english" run on it.
func (b *builder) englishKind() error {
	_, err := b.conn.Exec(b.ctx, `ALTER TABLE noindex_page DROP CONSTRAINT IF EXISTS noindex_page_kind_check,
		ADD CONSTRAINT noindex_page_kind_check CHECK (kind IN ('article', 'ruling', 'opinion', 'agreement', 'doctrine', 'research', 'english'))`)
	return err
}

func (b *builder) indexing() error {
	if err := b.englishKind(); err != nil {
		return err
	}
	if _, err := b.conn.Exec(b.ctx, `TRUNCATE noindex_page`); err != nil {
		return err
	}
	if err := b.thinArticles(); err != nil {
		return err
	}
	// Placeholder documents: a title that says it is missing, or nothing to read.
	for _, q := range []string{
		`INSERT INTO noindex_page SELECT 'agreement', id, 0, CASE WHEN title ~ 'غير متوفر' THEN 'placeholder' ELSE 'empty' END
		   FROM agreement WHERE title ~ 'غير متوفر' OR btrim(coalesce(text_ar, '')) = ''`,
		`INSERT INTO noindex_page SELECT 'doctrine', id, 0, CASE WHEN btrim(title) IN ('', 'للمؤلف') THEN 'placeholder' ELSE 'empty' END
		   FROM doctrine_article WHERE btrim(title) IN ('', 'للمؤلف') OR length(btrim(coalesce(abstract, ''))) < 40`,
		`INSERT INTO noindex_page SELECT 'opinion', id, 0, 'empty' FROM opinion WHERE btrim(coalesce(text_ar, '')) = ''`,
		`INSERT INTO noindex_page SELECT 'research', id, 0, 'empty' FROM research WHERE btrim(coalesce(text_ar, '')) = ''`,
		`INSERT INTO noindex_page SELECT 'ruling', r.id, 0, 'empty' FROM ruling r WHERE btrim(coalesce(r.text_ar, '')) = ''
		   AND NOT EXISTS (SELECT 1 FROM ruling_page p WHERE p.ruling_id = r.id)
		   AND NOT EXISTS (SELECT 1 FROM ruling_file f WHERE f.ruling_id = r.id)`,
	} {
		if _, err := b.conn.Exec(b.ctx, q); err != nil {
			return err
		}
	}
	if err := b.weakPages(); err != nil {
		return err
	}
	if err := b.pageState(); err != nil {
		return err
	}
	rows, err := b.conn.Query(b.ctx, `SELECT kind, reason, count(*) FROM noindex_page GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, reason string
		var n int
		if err := rows.Scan(&kind, &reason, &n); err != nil {
			return err
		}
		b.rep.note("noindex: %d %s pages (%s)", n, kind, reason)
	}
	return rows.Err()
}

// weakPages lists the English pages of every text that is not popular
// (Options.PopularTexts), kept for visitors and hidden from search engines
// (internal/indexing/weak.go).
func (b *builder) weakPages() error {
	if b.PopularTexts == "" {
		b.rep.note("noindex: no popular texts list, English pages left open to search engines")
		return nil
	}
	ids, err := indexing.ReadPopular(b.PopularTexts)
	if err != nil {
		return err
	}
	// One row per text covers its /en/ text page, parts and articles.
	_, err = b.conn.Exec(b.ctx, `INSERT INTO noindex_page SELECT 'english', id, 0, '`+indexing.ReasonMachineEnglish+`'
		FROM legislation WHERE id <> ALL($1::int[])`, ids)
	return err
}

// englishOnly ("-only english") rewrites the english rows alone, leaving the
// other rows and page_state as they are. A database restored from data/ gets
// its english rows this way: the nightly that writes data/ builds without the
// popular list, and the whole indexing step would move page dates (its
// lastmod rule reads current_date).
func (b *builder) englishOnly() error {
	if b.PopularTexts == "" {
		return errors.New("no popular texts list (-popular): it would leave every English page open")
	}
	if err := b.englishKind(); err != nil {
		return err
	}
	if _, err := b.conn.Exec(b.ctx, `DELETE FROM noindex_page WHERE kind = 'english'`); err != nil {
		return err
	}
	if err := b.weakPages(); err != nil {
		return err
	}
	var n int
	if err := b.conn.QueryRow(b.ctx, `SELECT count(*) FROM noindex_page WHERE kind = 'english'`).Scan(&n); err != nil {
		return err
	}
	b.rep.note("noindex: %d english pages (%s)", n, indexing.ReasonMachineEnglish)
	return nil
}

// thinArticles lists the article pages with nothing to index.
func (b *builder) thinArticles() error {
	type short struct {
		law, pos int
		key      string
		reason   string
	}
	var shorts []short
	copies := map[string]int{}
	rows, err := b.conn.Query(b.ctx, shownArticle)
	if err != nil {
		return err
	}
	for rows.Next() {
		var law, pos int
		var text string
		if err := rows.Scan(&law, &pos, &text); err != nil {
			rows.Close()
			return err
		}
		words := indexing.Body(text)
		if len(words) >= indexing.MaxThinWords {
			continue
		}
		key := strings.Join(words, " ")
		copies[key]++
		shorts = append(shorts, short{law, pos, key, indexing.Reason(text, 0)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var out [][]any
	for _, s := range shorts {
		reason := s.reason
		if reason == "" && copies[s.key] >= indexing.CopiesThreshold {
			reason = indexing.ReasonBoilerplate
		}
		if reason != "" {
			out = append(out, []any{"article", s.law, s.pos, reason})
		}
	}
	_, err = b.conn.CopyFrom(b.ctx, pgx.Identifier{"noindex_page"}, []string{"kind", "id", "position", "reason"}, pgx.CopyFromRows(out))
	return err
}

// pageState hashes what every page shows and dates the change.
func (b *builder) pageState() error {
	for _, q := range []string{
		`CREATE TEMP TABLE prev_state (kind text, id int, content_hash text, changed_at timestamptz) ON COMMIT PRESERVE ROWS`,
		`CREATE TEMP TABLE new_state (kind text, id int, content_hash text, seed date, floor date)`,
	} {
		if _, err := b.conn.Exec(b.ctx, q); err != nil {
			return err
		}
	}
	defer b.conn.Exec(b.ctx, `DROP TABLE IF EXISTS prev_state, new_state`)
	if b.prior != nil {
		out := make([][]any, len(b.prior))
		for i, s := range b.prior {
			out[i] = []any{s.kind, s.id, s.hash, s.at}
		}
		if _, err := b.conn.CopyFrom(b.ctx, pgx.Identifier{"prev_state"}, []string{"kind", "id", "content_hash", "changed_at"}, pgx.CopyFromRows(out)); err != nil {
			return err
		}
	} else if _, err := b.conn.Exec(b.ctx, `INSERT INTO prev_state SELECT kind, id, content_hash, changed_at FROM page_state`); err != nil {
		return err
	}
	// A change the scraper saw at the Centre after it first fetched the page;
	// the first fetch is not a change.
	real := func(alias string) string {
		return `CASE WHEN ` + alias + `.last_changed > ` + alias + `.first_seen + interval '1 day' THEN ` + alias + `.last_changed::date END`
	}
	// floor is the day the page first existed here: when its source was first
	// fetched (else when the first source was). A page cannot have changed
	// before it existed, so its date is never the text's own date (a 1964
	// decree's page did not change in 1964): seed is the floor or a later
	// change (an amendment coming into force, a judgment reading added, a change
	// at the Centre), and a carried date is raised to the floor.
	floor := func(firstSeen string) string {
		return `coalesce((` + firstSeen + `)::date, (SELECT min(first_seen)::date FROM source_page))`
	}
	seed := func(firstSeen string, later ...string) string {
		return `least(current_date, greatest(` + strings.Join(append([]string{floor(firstSeen)}, later...), ", ") + `)), ` + floor(firstSeen)
	}
	for _, q := range []string{
		// texts: the fields, every article, every version, the applied changes
		`INSERT INTO new_state SELECT 'law', l.id,
		   md5(concat_ws(chr(30), l.kind, l.number, l.issued_on::text, l.title, l.status, l.gazette_number, l.gazette_date::text,
		     l.gazette_pages, l.preamble, l.explanatory_note, l.closing, ar.h, ve.h, dc.h)),
		   ` + seed("least(sp.first_seen, st.first_seen)", "am.d", real("sp"), real("st")) + `
		 FROM legislation l
		 LEFT JOIN (SELECT law_id, md5(string_agg(concat_ws(chr(31), "position"::text, number, note, text_ar, text_fr, in_force_from::text), chr(29) ORDER BY "position")) h
		            FROM legislation_article GROUP BY law_id) ar ON ar.law_id = l.id
		 LEFT JOIN (SELECT law_id, md5(string_agg(concat_ws(chr(31), article_number, major::text, minor::text, state, text_ar, valid_from::text, valid_to::text),
		            chr(29) ORDER BY article_number, major, minor)) h FROM article_version GROUP BY law_id) ve ON ve.law_id = l.id
		 LEFT JOIN (SELECT law_id, md5(string_agg(concat_ws(chr(31), field, corrected::text), chr(29) ORDER BY field)) h
		            FROM date_correction GROUP BY law_id) dc ON dc.law_id = l.id
		 LEFT JOIN (SELECT law_id, max(effective_on) d FROM amendment WHERE status IN ('applied', 'partial') AND effective_on <= current_date
		            GROUP BY law_id) am ON am.law_id = l.id
		 LEFT JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
		 LEFT JOIN source_page st ON st.doc_type = 'lawtext' AND st.centre_id = l.id`,
		`INSERT INTO new_state SELECT 'ruling', r.id,
		   md5(concat_ws(chr(30), r.title, r.court, r.number, r.year::text, r.decided_on::text, r.president, array_to_string(r.members, '|'),
		     r.text_ar, f.sha256, pg.h)),
		   ` + seed("sp.first_seen", "pg.d", real("sp")) + `
		 FROM ruling r
		 LEFT JOIN ruling_file f ON f.ruling_id = r.id
		 LEFT JOIN (SELECT ruling_id, max(read_at)::date d, md5(string_agg(text_ar, chr(29) ORDER BY n)) h FROM ruling_page GROUP BY ruling_id) pg ON pg.ruling_id = r.id
		 LEFT JOIN source_page sp ON sp.doc_type = 'ruling' AND sp.centre_id = r.id`,
		`INSERT INTO new_state SELECT 'gazette', g.id,
		   md5(concat_ws(chr(30), g.number, g.published_on::text, e.h)),
		   ` + seed("sp.first_seen", real("sp")) + `
		 FROM gazette_issue g
		 LEFT JOIN (SELECT issue_id, md5(string_agg(concat_ws(chr(31), law_id::text, pages), chr(29) ORDER BY law_id)) h FROM gazette_entry GROUP BY issue_id) e ON e.issue_id = g.id
		 LEFT JOIN source_page sp ON sp.doc_type = 'journal' AND sp.centre_id = g.id`,
		`INSERT INTO new_state SELECT 'opinion', o.id, md5(concat_ws(chr(30), o.number, o.year::text, o.issued_on::text, o.requester, o.file_number, o.subject, o.text_ar)),
		   ` + seed("sp.first_seen", real("sp")) + `
		 FROM opinion o LEFT JOIN source_page sp ON sp.doc_type = 'opinion' AND sp.centre_id = o.id`,
		`INSERT INTO new_state SELECT 'agreement', a.id, md5(concat_ws(chr(30), a.title, a.scope, a.signed_on::text, a.ratified_by, a.ratifying_law_id::text, a.text_ar)),
		   ` + seed("sp.first_seen", real("sp")) + `
		 FROM agreement a LEFT JOIN source_page sp ON sp.doc_type = 'agreement' AND sp.centre_id = a.id`,
		`INSERT INTO new_state SELECT 'doctrine', d.id, md5(concat_ws(chr(30), d.title, array_to_string(d.authors, '|'), d.series, d.pages, d.abstract)),
		   ` + seed("sp.first_seen", real("sp")) + `
		 FROM doctrine_article d LEFT JOIN source_page sp ON sp.doc_type = 'article' AND sp.centre_id = d.id`,
		`INSERT INTO new_state SELECT 'research', x.id, md5(concat_ws(chr(30), x.title, x.author, x.published_on::text, x.text_ar)),
		   ` + seed("sp.first_seen", real("sp")) + `
		 FROM research x LEFT JOIN source_page sp ON sp.doc_type = 'research' AND sp.centre_id = x.id`,
		// unchanged keeps its date, raised to the floor (earlier builds seeded
		// the text's own date); changed is dated today; new takes its seed on
		// the first run and today after
		`TRUNCATE page_state`,
		`INSERT INTO page_state SELECT n.kind, n.id, n.content_hash,
		   CASE WHEN p.kind IS NULL THEN CASE WHEN $1 THEN now() ELSE coalesce(n.seed, current_date)::timestamptz END
		        WHEN p.content_hash = n.content_hash THEN greatest(p.changed_at, n.floor::timestamptz)
		        ELSE now() END
		 FROM new_state n LEFT JOIN prev_state p ON p.kind = n.kind AND p.id = n.id`,
	} {
		var args []any
		if strings.Contains(q, "$1") {
			var n int
			if err := b.conn.QueryRow(b.ctx, `SELECT count(*) FROM prev_state`).Scan(&n); err != nil {
				return err
			}
			args = append(args, n > 0) // not the first run: a page new to us changed today
		}
		if _, err := b.conn.Exec(b.ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}
