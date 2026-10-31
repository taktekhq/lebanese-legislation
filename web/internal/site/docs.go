package site

import (
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type DocRow struct {
	Type    string
	ID      int
	Title   string
	Kicker  string
	Date    *time.Time
	Snippet string
}

func scanDocRows(rows pgx.Rows) ([]DocRow, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (DocRow, error) {
		var d DocRow
		var snip *string
		err := row.Scan(&d.Type, &d.ID, &d.Title, &d.Kicker, &d.Date, &snip)
		if snip != nil {
			d.Snippet = *snip
		}
		return d, err
	})
}

func (d DocRow) URL() string {
	if d.Type == "law" {
		return lawPath(d.ID)
	}
	return fmt.Sprintf("/%s/%d/", d.Type, d.ID)
}

func (d DocRow) Highlight() template.HTML { return mark(d.Snippet) }

func (l LawRow) Highlight() template.HTML { return mark(l.Snippet) }

const headlineMarks = "StartSel=\x02, StopSel=\x03"

func mark(s string) template.HTML {
	s = html.EscapeString(s)
	s = strings.ReplaceAll(s, "\x02", "<mark>")
	return template.HTML(strings.ReplaceAll(s, "\x03", "</mark>"))
}

type docKind struct {
	key, list, table, title, eyebrow, h1, noun, many, desc string
	listSQL                                                string
	orderBy                                                string
}

var docKinds = map[string]docKind{
	"opinion": {key: "opinion", list: "opinions", table: "opinion", noun: "opinion", many: "opinions",
		title: "Legal opinions", eyebrow: "Legal opinions · الآراء الاستشارية", h1: "Advisory opinions.",
		desc:    "Advisory opinions on Lebanese law, with their full text. Free.",
		listSQL: `SELECT 'opinion', id, coalesce(subject, 'رأي استشاري رقم ' || coalesce(number, '') || '/' || coalesce(year::text, '')), coalesce(requester, ''), issued_on, NULL FROM opinion`,
		orderBy: "issued_on DESC NULLS LAST, id DESC"},
	"agreement": {key: "agreement", list: "agreements", table: "agreement", noun: "agreement", many: "agreements",
		title: "Agreements", eyebrow: "Agreements · الاتفاقيات", h1: "Treaties and agreements.",
		desc:    "Lebanon's bilateral and international agreements, with the text that ratified them. Free.",
		listSQL: `SELECT 'agreement', id, title, coalesce(CASE scope WHEN 'bilateral' THEN 'Bilateral' WHEN 'international' THEN 'International' END, ''), signed_on, NULL FROM agreement`,
		orderBy: "signed_on DESC NULLS LAST, id DESC"},
	"doctrine": {key: "doctrine", list: "doctrine", table: "doctrine_article", noun: "article", many: "articles",
		title: "Doctrine", eyebrow: "Doctrine · المقالات", h1: "Articles and commentary.",
		desc:    "Articles from Lebanese legal periodicals, with their abstracts. Free.",
		listSQL: `SELECT 'doctrine', id, title, coalesce(array_to_string(authors, '، '), ''), NULL::date, NULL FROM doctrine_article`,
		orderBy: "id DESC"},
	"research": {key: "research", list: "research", table: "research", noun: "study", many: "studies",
		title: "Research", eyebrow: "Research · الأبحاث والدراسات", h1: "Studies.",
		desc:    "Legal studies published by the Lebanese University's Centre for Legal Informatics. Free.",
		listSQL: `SELECT 'research', id, title, coalesce(author, ''), published_on, NULL FROM research`,
		orderBy: "published_on DESC NULLS LAST, id DESC"},
}

func (s *Server) docList(k docKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := s.ctx(r)
		defer cancel()
		q := r.URL.Query()
		var total int
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM "+k.table).Scan(&total); err != nil {
			s.fail(w, r, err)
			return
		}
		size := perPage
		if s.isStatic {
			size = max(total, 1)
		}
		page, pages, listBase, done := s.listPaging(w, r, "/"+k.list+"/", q, total, size)
		if done {
			return
		}
		rows, err := s.db.Query(ctx, k.listSQL+" ORDER BY "+k.orderBy+fmt.Sprintf(" LIMIT %d OFFSET %d", size, (page-1)*size))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		list, err := scanDocRows(rows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.render(w, r, "list", 200, Page{
			Title:   s.brandTitle(r, s.trOr(r, k.title, k.key+".list_title")) + s.pageSuffix(r, page),
			Desc:    s.trOr(r, k.desc, k.key+".desc"),
			Path:    pagePath("/"+k.list+"/", page),
			NoIndex: r.URL.RawQuery != "",
			Data: map[string]any{"Kind": map[string]string{
				"Eyebrow": s.trOr(r, k.eyebrow, k.key+".eyebrow"),
				"H1":      s.trOr(r, k.h1, k.key+".h1"),
				"Noun":    s.trOr(r, k.noun, k.key+".noun"),
				"Many":    s.trOr(r, k.many, k.key+".many")},
				"List": List{Q: q, Total: total, Page: page, Pages: pages, Rows: list, Base: listBase}}})
	}
}

type Doc struct {
	Crumb, CrumbURL string
	Kicker          []string
	Title           string
	Facts           [][2]any
	Paras           []string
	Note            string
	Source          string
	Cite            string
	Cites           []Ref
	Articles        []CitedArticle
	Laws            []LawRow
	File            *DocFile
	Reading         *DocReading
}

type Ref struct {
	LawID *int
	Label string
	Title *string
	Art   *string
}

func (s *Server) source(ctx context.Context, typ string, id int) string {
	var u string
	_ = s.db.QueryRow(ctx, "SELECT url FROM source_page WHERE doc_type = $1 AND centre_id = $2", typ, id).Scan(&u)
	return u
}

func (s *Server) refs(ctx context.Context, from string, id int) ([]Ref, error) {
	rows, err := s.db.Query(ctx, `SELECT x.law_id, coalesce(x.label, ''), l.title, x.article_number
		FROM reference x LEFT JOIN legislation l ON l.id = x.law_id
		WHERE x.from_type = $1 AND x.from_id = $2 ORDER BY x.law_id, x.article_number`, from, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Ref])
}

func (s *Server) doc(k docKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			s.notFound(w, r)
			return
		}
		ctx, cancel := s.ctx(r)
		defer cancel()
		d := Doc{Crumb: s.trOr(r, k.title, k.key+".list_title"), CrumbURL: "/" + k.list + "/"}
		var text *string
		var err error
		switch k.key {
		case "opinion":
			var num, req, fileNo, subj *string
			var year, fileYear *int
			var on *time.Time
			err = s.db.QueryRow(ctx, `SELECT number, year, issued_on, requester, file_number, file_year, subject, text_ar
				FROM opinion WHERE id = $1`, id).Scan(&num, &year, &on, &req, &fileNo, &fileYear, &subj, &text)
			d.Title = "رأي استشاري رقم " + str(num) + "/" + intStr(year)
			if subj != nil {
				d.Note = *subj
			}
			d.Kicker = []string{s.trOr(r, "Legal opinion", "opinion.kicker"), dmy(on)}
			d.Facts = [][2]any{{s.trOr(r, "Number", "opinion.fact_number"), str(num) + "/" + intStr(year)},
				{s.trOr(r, "Date", "opinion.fact_date"), dmy(on)}, {s.trOr(r, "Requested by", "opinion.fact_requester"), str(req)},
				{s.trOr(r, "File", "opinion.fact_file"), strings.Trim(str(fileNo)+"/"+intStr(fileYear), "/")}}
			d.Cite = d.Title
		case "agreement":
			var scope, by *string
			var law *int
			var on *time.Time
			err = s.db.QueryRow(ctx, `SELECT title, scope, signed_on, ratified_by, ratifying_law_id, text_ar FROM agreement WHERE id = $1`, id).
				Scan(&d.Title, &scope, &on, &by, &law, &text)
			sc := map[string]string{"bilateral": "Bilateral", "international": "International"}[str(scope)]
			sc = map[string]string{"Bilateral": s.trOr(r, "Bilateral", "agreement.bilateral"), "International": s.trOr(r, "International", "agreement.international"), "": ""}[sc]
			d.Kicker = []string{s.trOr(r, "Agreement", "agreement.kicker"), sc, dmy(on)}
			ratifyingText := s.trOr(r, "The ratifying text", "agreement.ratifying_text")
			ratified := any(str(by))
			if law != nil {
				ratified = template.HTML(fmt.Sprintf(`<a class="link" href="%s">%s</a>`, s.u(r, lawPath(*law)), html.EscapeString(orStr(str(by), ratifyingText))))
			}
			d.Facts = [][2]any{{s.trOr(r, "Scope", "agreement.fact_scope"), sc}, {s.trOr(r, "Signed", "agreement.fact_signed"), dmy(on)},
				{s.trOr(r, "Ratified by", "agreement.fact_ratified"), ratified}}
			d.Cite = d.Title
		case "doctrine":
			var authors []string
			var series, pages *string
			err = s.db.QueryRow(ctx, `SELECT title, authors, series, pages, abstract FROM doctrine_article WHERE id = $1`, id).
				Scan(&d.Title, &authors, &series, &pages, &text)
			d.Kicker = []string{s.trOr(r, "Doctrine", "doctrine.kicker"), strings.Join(authors, "، ")}
			d.Facts = [][2]any{{s.trOr(r, "Authors", "doctrine.fact_authors"), strings.Join(authors, "، ")},
				{s.trOr(r, "Published in", "doctrine.fact_published_in"), str(series)}, {s.trOr(r, "Pages", "doctrine.fact_pages"), str(pages)}}
			d.Cite = strings.Join(authors, "، ") + "، " + d.Title
		case "research":
			var author *string
			var on *time.Time
			err = s.db.QueryRow(ctx, `SELECT title, author, published_on, text_ar FROM research WHERE id = $1`, id).
				Scan(&d.Title, &author, &on, &text)
			d.Kicker = []string{s.trOr(r, "Study", "research.kicker"), str(author), dmy(on)}
			d.Facts = [][2]any{{s.trOr(r, "Author", "research.fact_author"), str(author)}, {s.trOr(r, "Published", "research.fact_published"), dmy(on)}}
			d.Cite = str(author) + "، " + d.Title
		}
		if errors.Is(err, pgx.ErrNoRows) {
			s.notFound(w, r)
			return
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
		d.Paras = paras(text)
		srcType := map[string]string{"doctrine": "article"}[k.key]
		if srcType == "" {
			srcType = k.key
		}
		d.Source = s.source(ctx, srcType, id)
		from := map[string]string{"doctrine": "doctrine_article"}[k.key]
		if from == "" {
			from = k.key
		}
		if d.Cites, err = s.refs(ctx, from, id); err != nil {
			s.fail(w, r, err)
			return
		}
		path := fmt.Sprintf("/%s/%d/", k.key, id)
		s.render(w, r, "doc", 200, Page{Lang: "ar", NoIndex: s.noindexed(ctx, k.key, id, 0), Title: s.brandTitle(r, cutWords(d.Title, 60)), Path: path,
			Desc:   cut(d.Title, 150),
			OGType: "article",
			LD: []any{map[string]any{"@context": "https://schema.org", "@type": "CreativeWork", "@id": Base + path, "name": d.Title,
				"inLanguage": "ar", "url": Base + path, "isBasedOn": d.Source, "isAccessibleForFree": true},
				breadcrumbs(k.title, "/"+k.list+"/", cut(d.Title, 80), path)},
			Data: d})
	}
}

func (s *Server) rulings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	q := r.URL.Query()
	var where []string
	var args []any
	if v := q.Get("court"); v != "" {
		args = append(args, v)
		where = append(where, "court = $1")
	}
	if v, err := strconv.Atoi(q.Get("decade")); err == nil && v >= 1900 && v < 2100 {
		args = append(args, v)
		where = append(where, fmt.Sprintf("year >= $%d AND year < $%d + 10", len(args), len(args)))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM ruling"+cond, args...).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	page := min(pageNo(q), max(1, (total+perPage-1)/perPage))
	rows, err := s.db.Query(ctx, `SELECT 'ruling', id, title, coalesce(president, ''), decided_on, left(text_ar, 220) FROM ruling`+cond+
		fmt.Sprintf(" ORDER BY decided_on DESC NULLS LAST, id DESC LIMIT %d OFFSET %d", perPage, (page-1)*perPage), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanDocRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	courts, err := s.chips(ctx, `SELECT r.court, r.court || coalesce(' · ' || c.name_en, ''), count(*) FROM ruling r
		JOIN court c ON c.name_ar = r.court GROUP BY 1, 2 ORDER BY 3 DESC`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var decades []Chip
	for y := 2020; y >= 1950; y -= 10 {
		decades = append(decades, Chip{Value: strconv.Itoa(y), Label: fmt.Sprintf("%ds", y)})
	}
	s.render(w, r, "list", 200, Page{
		Title:   s.brandTitle(r, s.trOr(r, "Lebanese court rulings", "rulings.title_suffix")),
		Path:    "/rulings/",
		NoIndex: r.URL.RawQuery != "",
		Desc: s.trOr(r, fmt.Sprintf("%s rulings of the Lebanese courts: Cassation, the State Council, the Constitutional Council and more. Free.", thousands(total)),
			"rulings.desc", thousands(total)),
		Data: map[string]any{"Kind": map[string]string{
			"Eyebrow": s.trOr(r, "Court rulings · الأحكام", "rulings.eyebrow"),
			"H1":      s.trOr(r, "Rulings of the Lebanese&nbsp;courts.", "rulings.h1"),
			"Noun":    s.trOr(r, "ruling", "rulings.noun"), "Many": s.trOr(r, "rulings", "rulings.many"), "Years": "/rulings/years/"},
			"List": List{Q: q, Total: total, Page: page, Pages: (total + perPage - 1) / perPage, Rows: list, Updated: s.updated(r, "rulings"),
				Params:   []string{"court", "decade"},
				Selected: map[string]string{"court": q.Get("court"), "decade": q.Get("decade")},
				Filters: [][]Chip{prepend(Chip{Label: s.trOr(r, "All courts", "chip.all_courts")}, courts),
					prepend(Chip{Label: s.trOr(r, "Any year", "chip.any_year")}, decades)}}}})
}

func (s *Server) ruling(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	var d Doc
	var court, courtEn, number, president, text *string
	var year *int
	var on *time.Time
	var members []string
	err := s.db.QueryRow(ctx, `SELECT r.title, r.court, c.name_en, r.number, r.year, r.decided_on, r.president, r.members, r.text_ar
		FROM ruling r LEFT JOIN court c ON c.name_ar = r.court WHERE r.id = $1`, id).
		Scan(&d.Title, &court, &courtEn, &number, &year, &on, &president, &members, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Crumb, d.CrumbURL = s.trOr(r, "Rulings", "ruling.back"), "/rulings/"
	kickerEn := ""
	if s.lang(r) != "ar" {
		kickerEn = str(courtEn)
	}
	d.Kicker = []string{kickerEn, dmy(on)}
	d.Facts = [][2]any{{s.trOr(r, "Court", "ruling.fact_court"), template.HTML(`<span lang="ar">` + html.EscapeString(str(court)) + `</span>`)},
		{s.trOr(r, "Number", "ruling.fact_number"), strings.Trim(str(number)+"/"+intStr(year), "/")}, {s.trOr(r, "Date", "ruling.fact_date"), dmy(on)},
		{s.trOr(r, "President", "ruling.fact_president"), str(president)}, {s.trOr(r, "Members", "ruling.fact_members"), strings.Join(members, "، ")}}
	d.Paras = paras(text)
	j, err := s.loadJudgment(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(d.Paras) == 0 {
		d.Note = s.trOr(r, "The Centre holds this judgment as a file only; read it at the Lebanese University.", "ruling.file_only")
	}
	s.judgmentDoc(r, &d, id, j)
	d.Source = s.source(ctx, "ruling", id)
	d.Cite = d.Title
	if d.Articles, err = s.rulingArticles(ctx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if on != nil {
		d.Cite += " تاريخ " + dmy(on)
	}
	if d.Cites, err = s.refs(ctx, "ruling", id); err != nil {
		s.fail(w, r, err)
		return
	}
	path := fmt.Sprintf("/ruling/%d/", id)
	s.render(w, r, "doc", 200, Page{Lang: "ar", NoIndex: s.noindexed(ctx, "ruling", id, 0), Title: s.brandTitle(r, cutWords(d.Title, 60)), Path: path,
		Desc:   cut(strings.TrimSuffix(d.Title+": "+orStr(strings.Join(d.Paras, " "), d.Reading.firstText()), ": "), 155),
		OGType: "article",
		LD:     []any{rulingLD(d, path, on, court, year), breadcrumbs("Rulings", "/rulings/", orStr(intStr(year), "Undated"), fmt.Sprintf("/rulings/year/%s/", orStr(intStr(year), "0")), cut(d.Title, 80), path)},
		Data:   d})
}

func rulingLD(d Doc, path string, on *time.Time, court *string, year *int) map[string]any {
	ld := map[string]any{"@context": "https://schema.org", "@type": "CreativeWork", "@id": Base + path, "url": Base + path,
		"name": d.Title, "genre": "Court ruling", "inLanguage": "ar", "isAccessibleForFree": true,
		"spatialCoverage": map[string]any{"@type": "Place", "name": "Lebanon"}}
	if on != nil {
		ld["datePublished"] = on.Format("2006-01-02")
	}
	if court != nil {
		ld["author"] = map[string]any{"@type": "Organization", "name": *court}
	}
	if d.Source != "" {
		ld["isBasedOn"] = d.Source
	}
	if a := orStr(strings.Join(d.Paras, " "), d.Reading.firstText()); a != "" {
		ld["abstract"] = cut(a, 300)
	}
	if d.File != nil {
		ld["encoding"] = map[string]any{"@type": "MediaObject", "contentUrl": Base + d.File.URL, "encodingFormat": "application/pdf"}
	}
	return ld
}

func (s *Server) gazettes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	q := r.URL.Query()
	var args []any
	cond := ""
	if v, err := strconv.Atoi(q.Get("year")); err == nil && v > 1800 && v < 2100 {
		args = append(args, v)
		cond = " WHERE extract(year FROM published_on) = $1"
	}
	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM gazette_issue"+cond, args...).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	page := min(pageNo(q), max(1, (total+perPage-1)/perPage))
	rows, err := s.db.Query(ctx, `SELECT 'gazette', g.id, 'الجريدة الرسمية، العدد ' || coalesce(g.number, '؟'),
		(SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::text || ' texts', g.published_on, NULL
		FROM gazette_issue g`+cond+fmt.Sprintf(" ORDER BY g.published_on DESC NULLS LAST, g.id DESC LIMIT %d OFFSET %d", perPage, (page-1)*perPage), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanDocRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	years, err := s.chips(ctx, `SELECT y::text, y::text, n FROM (SELECT extract(year FROM published_on)::int y, count(*)::int n
		FROM gazette_issue WHERE published_on IS NOT NULL GROUP BY 1) t ORDER BY y DESC LIMIT 40`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "list", 200, Page{
		Title:   s.brandTitle(r, s.trOr(r, "The Official Gazette", "gazette.title_suffix")),
		Path:    "/gazette/",
		NoIndex: r.URL.RawQuery != "",
		Desc: s.trOr(r, fmt.Sprintf("%s issues of Lebanon's Official Gazette, each with the texts it published. Free.", thousands(total)),
			"gazette.desc", thousands(total)),
		Data: map[string]any{"Kind": map[string]string{
			"Eyebrow": s.trOr(r, "Official Gazette · الجريدة الرسمية", "gazette.eyebrow"),
			"H1":      s.trOr(r, "Every issue of the Official&nbsp;Gazette.", "gazette.h1"),
			"Noun":    s.trOr(r, "issue", "gazette.noun"), "Many": s.trOr(r, "issues", "gazette.many"), "Years": "/gazette/years/"},
			"List": List{Q: q, Total: total, Page: page, Pages: (total + perPage - 1) / perPage, Rows: list, Updated: s.updated(r, "gazette"),
				Params: []string{"year"}, Selected: map[string]string{"year": q.Get("year")},
				Filters: [][]Chip{prepend(Chip{Label: s.trOr(r, "Every year", "chip.every_year")}, years)}}}})
}

func (s *Server) gazette(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	var number *string
	var on *time.Time
	if err := s.db.QueryRow(ctx, "SELECT number, published_on FROM gazette_issue WHERE id = $1", id).Scan(&number, &on); err != nil {
		s.notFound(w, r)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM gazette_entry e JOIN legislation l ON l.id = e.law_id
		JOIN legislation_kind k ON k.name_ar = l.kind WHERE e.issue_id = $1 ORDER BY l.id`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	laws, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := Doc{Crumb: s.trOr(r, "Official Gazette", "gazette.back"), CrumbURL: "/gazette/", Title: "الجريدة الرسمية، العدد " + orStr(str(number), "؟"),
		Kicker: []string{s.trOr(r, "Official Gazette", "gazette.eyebrow"), dmy(on)},
		Facts: [][2]any{{s.trOr(r, "Issue", "gazette.fact_issue"), str(number)}, {s.trOr(r, "Published", "gazette.fact_published"), dmy(on)},
			{s.trOr(r, "Texts", "gazette.fact_texts"), strconv.Itoa(len(laws))}},
		Laws: laws}
	if id >= scanIDBase {
		d.Note = s.trOr(r, "The Centre has not published this issue. These are the texts we read from the scans of its pages.", "gazette.scan_note")
	} else {
		d.Source = s.source(ctx, "journal", id)
		if n := countScans(laws); n > 0 {
			d.Note = s.trOr(r, fmt.Sprintf("%d of these texts are ones we read from the scans of the Gazette's pages, not from the Centre.", n), "gazette.scan_some_note", n)
		}
	}
	s.render(w, r, "doc", 200, Page{Lang: "ar", Title: s.brandTitle(r, d.Title+" "+dmy(on)), Path: fmt.Sprintf("/gazette/%d/", id),
		Desc: s.trOr(r, fmt.Sprintf("Official Gazette issue %s of %s: the %d texts it published.", str(number), dmy(on), len(laws)),
			"gazette.issue_desc", str(number), dmy(on), len(laws)), Data: d})
}

type Results struct {
	Topics             []TopicHit
	MeaningUnavailable bool
	Q                  string
	Laws               []LawRow
	LawCount           int
	Articles           []LawRow
	ArtCount           int
	Rulings            []DocRow
	RulCount           int
	Via                string
	ViaKind            string
	SameNum            []LawRow
	Near               []LawRow
	Num                string
	Year               int
	Download           bool
	Missing            string
	MissingRuling      bool

	rank string
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query().Get("q")
	if r.Method == http.MethodPost {
		if !sameSearchOrigin(r) {
			http.Error(w, "cross-site search is not allowed", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "search form is too large or invalid", http.StatusBadRequest)
			return
		}
		q = r.PostForm.Get("q")
	}
	ctx, cancel := s.ctx(r)
	defer cancel()

	res := Results{Q: arDigits.Replace(strings.TrimSpace(q))}
	if len([]rune(res.Q)) > 500 {
		http.Error(w, "search question is too long", http.StatusBadRequest)
		return
	}
	if res.Q != "" {

		if to := s.articleRef(ctx, res.Q); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		var err error
		res, _, err = s.searchCached(ctx, res.Q)
		if err != nil {
			s.fail(w, r, err)
			return
		}

	}
	title := s.brandTitle(r, s.trOr(r, "Search", "search.title"))
	if res.Q != "" {
		title = res.Q + " · " + title
	}
	s.render(w, r, "search", 200, Page{Title: title, Path: "/search/", NoIndex: true,
		Desc: s.trOr(r, "Search every Lebanese law, article and court ruling, in Arabic or French.", "search.desc"), Data: res})
}

func (s *Server) searchCached(ctx context.Context, q string) (Results, string, error) {
	if res, how, ok := s.scache.get(q); ok {
		return res, how, nil
	}
	res, how, err := s.searchFor(ctx, q)
	if err == nil {
		s.scache.put(q, res, how)
	}
	return res, how, err
}

func (s *Server) searchFor(ctx context.Context, q string) (Results, string, error) {
	t, cut := tidyQuery(q)
	if t == "" {
		t = q
	}
	res, how, err := s.searchTidy(ctx, t)
	res.Q = q
	if cut && res.Via == "" && found(res) {
		res.Via, res.ViaKind = t, "adjusted"
	}
	return res, how, err
}

func (s *Server) searchTidy(ctx context.Context, q string) (Results, string, error) {
	res := Results{Q: q, Download: WantsDownload(q)}
	if rq := readQuery(q); rq.lawCite() || rq.rulingCite() {
		find, how := s.findLawCite, "cite"
		if rq.rulingCite() {
			find, how = s.findRulingCite, "cite_ruling"
		}
		ok, err := find(ctx, &res, rq)
		if err != nil {
			return res, how, err
		}

		if !rq.rulingCite() && len(strings.Fields(q)) >= 5 {
			if row := s.exactTitle(ctx, q); row != nil {
				var added bool
				res.Laws, added = withPin(res.Laws, row, 10)
				if added {
					res.LawCount++
				}
				res.Via, res.ViaKind = "", ""
				return res, how, nil
			}
		}
		if ok {
			return res, how, nil
		}
		res.Missing, res.MissingRuling = rq.label(), rq.rulingCite()
		res.Near = s.nearTitles(ctx, strings.Join(rq.Words, " "))
		if rq.Num > 0 && !rq.rulingCite() {
			res.SameNum = s.sameNumber(ctx, rq.Num)
		}
		return res, how + "_missing", nil
	}
	if err := s.find(ctx, &res); err != nil {
		return res, "", err
	}
	if found(res) {
		if res.LawCount == 0 {

			res.Laws, res.LawCount = s.lawsByEnglishTitle(ctx, q, 10)
		}
		if res.LawCount == 0 {
			res.Near = s.closeTitles(ctx, q)
		}
		return res, "", nil
	}
	alt, how, err := s.rescue(ctx, q)
	if err != nil {
		return res, "", err
	}
	if how == "" {
		if res.Laws, res.LawCount = s.lawsByEnglishTitle(ctx, q, 10); res.LawCount > 0 {
			return res, "english", nil
		}
		res.Near = s.nearTitles(ctx, q)
		return res, "", nil
	}
	if alt.LawCount == 0 {
		alt.Laws, alt.LawCount = s.lawsByEnglishTitle(ctx, q, 10)
	}
	alt.Download = res.Download
	return alt, how, nil
}

func (s *Server) find(ctx context.Context, res *Results) error {
	res.Topics = searchTopics(res.Q)
	if res.Num != "" || res.Year != 0 {
		return s.findLaws(ctx, res)
	}
	errs := make(chan error, 3)
	go func() { errs <- s.findLaws(ctx, res) }()
	go func() { errs <- s.findArticles(ctx, res) }()
	go func() { errs <- s.findRulings(ctx, res) }()
	var first error
	for range 3 {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Server) findLaws(ctx context.Context, res *Results) error {
	if err := s.findLawsBase(ctx, res); err != nil {
		return err
	}
	if res.Num == "" && res.Year == 0 {
		s.addEnglishTitles(ctx, res)
	}

	if res.Num == "" {
		if pin := s.pinnedRow(ctx, res.Q, res.Year); pin != nil {
			var added bool
			res.Laws, added = withPin(res.Laws, pin, 10)
			if added {
				res.LawCount++
			}
		}
	}
	return nil
}

func (s *Server) findLawsBase(ctx context.Context, res *Results) error {
	q := res.Q

	if num, yr, ok := parseCitation(q); ok {
		const cw = `l.number::text = $1 AND l.issued_on >= make_date($2, 1, 1) AND l.issued_on < make_date($2 + 1, 1, 1)`
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l WHERE "+cw, num, yr).Scan(&res.LawCount); err != nil {
			return err
		}
		if res.LawCount > 0 {
			rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
				WHERE `+cw+` ORDER BY l.issued_on DESC, l.id DESC LIMIT 10`, num, yr)
			if err != nil {
				return err
			}
			res.Laws, err = scanLawRows(rows)
			return err
		}
	}

	conds, args := titleConds(strings.Fields(q), 1)
	if n, err := strconv.Atoi(res.Num); err == nil {
		args = append(args, n)
		conds = append(conds, fmt.Sprintf("l.number_int = $%d", len(args)))
	}
	if res.Year != 0 {
		args = append(args, res.Year)
		conds = append(conds, fmt.Sprintf("l.issued_on >= make_date($%[1]d, 1, 1) AND l.issued_on < make_date($%[1]d + 1, 1, 1)", len(args)))
	}
	if len(conds) > 0 {
		cond := strings.Join(conds, " AND ")
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l WHERE "+cond, args...).Scan(&res.LawCount); err != nil {
			return err
		}

		order := ""
		nums := queryNumbers(q)
		if len(nums) > 0 {
			args = append(args, nums)
			order = fmt.Sprintf("(l.number_int = ANY($%d)) IS TRUE DESC, ", len(args))
			if onlyDigits(q) {
				args = append(args, kindGroups["law"])
				order += fmt.Sprintf("(l.kind = ANY($%d)) DESC, (SELECT count(*) FROM (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id LIMIT 10) a) DESC, ", len(args))
			}
		}
		rank := q
		if res.rank != "" {
			rank = res.rank
		}
		args = append(args, rank)
		rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
			WHERE `+cond+fmt.Sprintf(` ORDER BY %s(EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC,
			similarity(ar_norm(l.title), ar_norm($%d)) DESC, l.issued_on DESC NULLS LAST LIMIT 30`, order, len(args)), args...)
		if err != nil {
			return err
		}
		if res.Laws, err = scanLawRows(rows); err != nil {
			return err
		}
		res.Laws = inForceFirst(res.Laws, nums, onlyDigits(q))
		res.Laws = res.Laws[:min(10, len(res.Laws))]
	}
	return nil
}

func inForceFirst(laws []LawRow, nums []int, digits bool) []LawRow {
	if len(laws) < 2 || laws[0].Status != "repealed" {
		return laws
	}
	level := func(l LawRow) string {
		n := 0
		if l.Number != nil {
			n, _ = strconv.Atoi(strings.TrimLeft(*l.Number, " "))
		}
		k := fmt.Sprint(slices.Contains(nums, n), l.Code != nil)
		if digits {
			k += fmt.Sprint(slices.Contains(kindGroups["law"], l.Kind), min(l.Articles, 10))
		}
		return k
	}
	top := level(laws[0])
	for i, l := range laws[1:] {
		if l.Status == "repealed" || level(l) != top {
			continue
		}
		out := append([]LawRow{l}, laws[:i+1]...)
		return append(out, laws[i+2:]...)
	}
	return laws
}

func (s *Server) exactTitle(ctx context.Context, q string) *LawRow {
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE ar_norm(l.title) IN (ar_norm($1), ar_norm($1) || ' .', ar_norm($1) || '.')
		ORDER BY l.issued_on DESC NULLS LAST, l.id DESC LIMIT 1`, q)
	if err != nil {
		return nil
	}
	laws, err := scanLawRows(rows)
	if err != nil || len(laws) == 0 {
		return nil
	}
	return &laws[0]
}

func (s *Server) findArticles(ctx context.Context, res *Results) error {
	q := articleQuery(res.Q)
	const artWhere = `(a.search_exact @@ websearch_to_tsquery('simple', ar_norm($1))
		OR a.search_stem @@ websearch_to_tsquery('arabic', ar_light($1))
		OR a.search_fr @@ websearch_to_tsquery('french', $1))`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM legislation_article a WHERE `+artWhere+` LIMIT 1000) t`, q).
		Scan(&res.ArtCount); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `WITH hits AS (
		SELECT a.law_id, a.number, a.text_ar,
		       (a.search_exact @@ phraseto_tsquery('simple', ar_norm($1)))::int * 4
		     + (a.search_exact @@ websearch_to_tsquery('simple', ar_norm($1)))::int * 2
		     + ts_rank(a.search_stem, websearch_to_tsquery('arabic', ar_light($1)))
		     + (EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id))::int AS score,
		       (EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id))::int AS is_code
		FROM `+articleSource(q, artWhere)+` WHERE `+artWhere+`
		ORDER BY score DESC, is_code DESC LIMIT 15)
		SELECT `+lawRowCols+`, h.number, ts_headline('arabic', h.text_ar, websearch_to_tsquery('arabic', ar_light($1)),
		       '`+headlineMarks+`, MaxWords=40, MinWords=18, ShortWord=2')
		FROM hits h JOIN legislation l ON l.id = h.law_id JOIN legislation_kind k ON k.name_ar = l.kind
		ORDER BY h.score DESC, h.is_code DESC`, q)
	if err != nil {
		return err
	}
	res.Articles, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (LawRow, error) {
		var x LawRow
		err := row.Scan(&x.ID, &x.Kind, &x.KindEn, &x.Number, &x.IssuedOn, &x.Title, &x.Status, &x.Articles, &x.Code, &x.Article, &x.Snippet)
		return x, err
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) findRulings(ctx context.Context, res *Results) error {
	n, hits, err := s.searchRulings(ctx, res.Q, 10, 0)
	if err != nil {
		return err
	}
	res.RulCount = n
	for _, h := range hits {
		res.Rulings = append(res.Rulings, DocRow{Type: "ruling", ID: h.ID, Title: h.Title, Kicker: h.Court, Date: h.On, Snippet: h.Snippet})
	}
	return nil
}

func (s *Server) static(name, title, desc string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, name, 200, Page{Title: title, Desc: desc, Path: r.URL.Path})
	}
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "about", 200, Page{Title: s.brandTitle(r, s.trOr(r, "About", "about.title_suffix")), Path: "/about/",
		Desc: s.trOr(r, "Where the texts on Lebanese Legislation come from, what is included, and who makes it: a free public library of Lebanese law.", "about.desc"),
		Data: st})
}

type TableInfo struct {
	Name    string
	Rows    int
	Comment string
}

func (s *Server) data(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT c.relname, c.reltuples::bigint::int FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY c.relname`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (TableInfo, error) {
		var t TableInfo
		err := row.Scan(&t.Name, &t.Rows)
		t.Comment = tableNotes[t.Name]
		return t, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ds := map[string]any{"@context": "https://schema.org", "@type": "Dataset", "@id": Base + "/data/#dataset", "url": Base + "/data/",
		"name":        "Lebanese Legislation: the relational database of Lebanese law",
		"description": "Every Lebanese law, decree and decision with its chapters and articles (Arabic, and French where published), the Official Gazette, court rulings, advisory opinions, agreements and doctrine, as one PostgreSQL database, with the references between them and the source page of every row.",
		"keywords":    []string{"Lebanon", "Lebanese law", "legislation", "case law", "Official Gazette", "التشريعات اللبنانية", "القانون اللبناني", "législation libanaise"},
		"inLanguage":  []string{"ar", "fr", "en"}, "isAccessibleForFree": true,
		"spatialCoverage": map[string]any{"@type": "Place", "name": "Lebanon"}, "temporalCoverage": "1904/..",
		"creator":          map[string]any{"@type": "Organization", "name": "Taktek, LLC", "url": "https://taktek.io/"},
		"isBasedOn":        map[string]any{"@type": "Organization", "name": "Centre for Research and Studies in Legal Informatics, Lebanese University", "url": "https://legallaw.ul.edu.lb/"},
		"license":          Base + "/data/#license",
		"creditText":       "Source: Centre for Research and Studies in Legal Informatics, Lebanese University",
		"variableMeasured": []string{"legislation", "legislation_article", "ruling", "gazette_issue", "opinion", "agreement", "reference"}}
	s.render(w, r, "data", 200, Page{Title: s.brandTitle(r, s.trOr(r, "The database", "data.title_suffix")), Path: "/data/", LD: []any{ds},
		Desc: s.trOr(r, "All of Lebanese law as one relational PostgreSQL database: its tables, row counts and example queries.", "data.desc"), Data: tables})
}

var tableNotes = map[string]string{
	"legislation":          "Every law, decree and decision: kind, number, date, title, status, Official Gazette publication.",
	"legislation_article":  "Each article, consolidated, in Arabic and in French where published, with its amendment notes.",
	"legislation_section":  "Books, parts, chapters and sections, nested.",
	"legislation_kind":     "The 49 kinds of text, with English and French names.",
	"code":                 "The principal codes, with stable addresses.",
	"amendment":            "Every change one text made to another, read from the amending article: which article, what it did, the new wording, from what date, and whether it was applied.",
	"article_version":      "Each amended article's versions, oldest first, each with its date and the amendment that made it.",
	"gazette_issue":        "Official Gazette issues.",
	"gazette_entry":        "Which texts each issue published, on which page.",
	"ruling":               "Court rulings: court, number, date, panel, text.",
	"ruling_file":          "The scanned full judgment behind a ruling page (a PDF served at /ruling-files/<id>.pdf, not in the database): its size, SHA-256, when it was fetched and its page count once read.",
	"ruling_page":          "The text of those scans, one row per page, read by machine. It may contain errors; the scan is the reference. low_confidence marks pages that are hard to read (mostly handwriting): expect more errors there.",
	"court":                "The courts and chambers.",
	"opinion":              "Advisory opinions.",
	"agreement":            "Treaties and agreements, with the text that ratified them.",
	"doctrine_article":     "Articles from legal periodicals, with abstracts.",
	"research":             "Studies.",
	"reference":            "Every link from a document to a text or article, and whether the Centre's editors, our rules or a model made it.",
	"source_page":          "Public source addresses and publication change dates.",
	"legislation_scan":     "Texts the Lebanese University has not published yet, read by machine from scans of the Gazette's pages: where the scans came from, how many pages, and how far the two readings agree. Words they disagree on are marked in the text: U+E000 reading one, U+E001 reading two, U+E002.",
	"legislation_alias":    "The old address of a scanned text the Lebanese University has since published, and the text it now points to.",
	"centre_error":         "Errors in the Lebanese University's own text of an article (one article repeating another, a stray letter), noted next to the article and never corrected in it. Where a person read the right wording from the printed edition, it is a minor version in article_version.",
	"noindex_page":         "Pages kept out of search results and the sitemaps, with the reason: articles that are only a publication or repeal clause, documents with no title or no text. They stay on the site and linked.",
	"page_state":           "When each text, ruling, Gazette issue and document last changed, with a hash of what its page shows: the date the sitemaps give as lastmod.",
	"legislation_title_en": "An unofficial English rendering of each text's subject line, written by a model (Claude Sonnet) from the Arabic title alone and labelled unofficial on the site. The Arabic title and text are the official version and the more accurate one; the English is there to help people and search engines find a text, and the text itself is never translated.",
	"date_correction":      "Dates the Lebanese University printed wrong (a text signed after its own publication, a year typo, a placeholder): the printed date, the corrected one, how it was settled and the evidence.",
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func intStr(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func orStr(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func (s *Server) articleRef(ctx context.Context, q string) string {
	num, words, ok := parseArticleRef(q)
	if !ok {
		return ""
	}

	if rq := readQuery(strings.Join(words, " ")); rq.Num > 0 && rq.lawCite() {
		var res Results
		if found, err := s.findLawCite(ctx, &res, rq); err == nil && found {
			for _, l := range res.Laws {
				var n string
				if s.db.QueryRow(ctx, "SELECT number FROM legislation_article WHERE law_id = $1 AND number = $2 LIMIT 1", l.ID, num).Scan(&n) == nil {
					return lawPath(l.ID) + "article/" + n + "/"
				}
			}
		}
		return ""
	}
	var conds []string
	var args []any
	for _, wd := range words {
		if r := []rune(wd); len(r) > 3 && r[0] == 'و' {
			wd = string(r[1:])
		}
		args = append(args, "%"+wd+"%")
		conds = append(conds, fmt.Sprintf("ar_norm(l.title) LIKE ar_norm($%d)", len(args)))
	}

	rows, err := s.db.Query(ctx, "SELECT l.id, length(l.title) FROM code c JOIN legislation l ON l.id = c.law_id WHERE "+
		strings.Join(conds, " AND ")+" ORDER BY length(l.title) LIMIT 2", args...)
	if err != nil {
		return ""
	}
	type hit struct{ id, n int }
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (h hit, err error) { err = row.Scan(&h.id, &h.n); return })
	if err == nil && len(hits) == 0 && len(words) >= 2 {

		args = append(args, num)
		rows, err = s.db.Query(ctx, "SELECT l.id, length(l.title) FROM legislation l WHERE "+strings.Join(conds, " AND ")+
			fmt.Sprintf(" AND EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id AND a.number = $%d)", len(args))+
			" ORDER BY length(l.title), l.issued_on LIMIT 2", args...)
		if err != nil {
			return ""
		}
		hits, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (h hit, err error) { err = row.Scan(&h.id, &h.n); return })
	}
	if err != nil || len(hits) == 0 || (len(hits) == 2 && hits[0].n == hits[1].n) {
		return ""
	}
	ids := []int{hits[0].id}
	var n string
	if s.db.QueryRow(ctx, "SELECT number FROM legislation_article WHERE law_id = $1 AND number = $2 LIMIT 1", ids[0], num).Scan(&n) != nil {
		return ""
	}
	return lawPath(ids[0]) + "article/" + n + "/"
}
