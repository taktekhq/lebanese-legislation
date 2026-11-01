package build

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/parse"
)

var (
	lawCols = []string{"id", "kind", "number", "number_int", "issued_on", "title", "status",
		"gazette_number", "gazette_date", "gazette_pages", "preamble", "explanatory_note", "closing"}
	sectionCols = []string{"id", "law_id", "parent_id", "position", "depth", "heading", "note"}
	// source_kind and source_id name the archived page the text was read
	// from, written with it (sources.go): the wording guard checks the text
	// against that page, never against a hash a writer chose.
	articleCols = []string{"law_id", "position", "centre_id", "section_id", "number", "number_int", "note",
		"text_ar", "text_fr", "images", "source_kind", "source_id"}
	refCols = []string{"from_type", "from_id", "relation", "law_id", "article_number", "article_centre_id",
		"label", "method"}
)

type lawDoc struct {
	entry archive.Entry
	law   *parse.Law
	text  *parse.LawText
	textE *archive.Entry
}

func (b *builder) legislation() error {
	laws, err := b.Archive.Index("law")
	if err != nil {
		return err
	}
	texts := map[int]archive.Entry{}
	if ts, err := b.Archive.Index("lawtext"); err == nil {
		for _, e := range ts {
			texts[e.ID] = e
		}
	}
	return chunked(b, laws, func(e archive.Entry) (lawDoc, error) {
		raw, err := b.Archive.Read("law", e.ID)
		if err != nil {
			return lawDoc{}, err
		}
		d := lawDoc{entry: e}
		if d.law, err = parse.ParseLaw(e.ID, raw); err != nil {
			return d, err
		}
		if te, ok := texts[e.ID]; ok {
			raw, err := b.Archive.Read("lawtext", e.ID)
			if errors.Is(err, os.ErrNotExist) {
				return d, nil // listed but not exported yet
			} else if err != nil {
				return d, err
			}
			if d.text, err = parse.ParseLawText(raw); err != nil {
				return d, err
			}
			d.textE = &te
		}
		return d, nil
	}, func(docs []lawDoc) error {
		r := newRows()
		for _, d := range docs {
			if err := b.ensure("legislation_kind", "kind", d.law.Kind, b.kinds); err != nil {
				return err
			}
			b.addLaw(r, d)
		}
		return b.flush(r, "legislation", "legislation_section", "legislation_article")
	})
}

func (b *builder) addLaw(r *rows, d lawDoc) {
	l := d.law
	status := "in_force"
	if l.Repealed {
		status = "repealed"
	}
	var closing *string
	if d.text != nil {
		closing = d.text.Closing
	}
	title := l.Title
	if title == "" && d.text != nil {
		title = d.text.Title
	}
	r.add("legislation", lawCols, l.ID, l.Kind, l.Number, l.NumberInt, l.IssuedOn, title, status,
		l.GazetteNumber, l.GazetteDate, l.GazettePages, l.Preamble, l.ExplanatoryNote, closing)
	b.laws[l.ID] = true
	b.rep.Laws++
	if l.Repealed {
		b.rep.Repealed++
	}
	gd := ""
	if l.GazetteDate != nil {
		gd = l.GazetteDate.Format("2006-01-02")
	}
	if l.GazetteNumber != nil {
		b.gazette[l.ID] = [2]string{*l.GazetteNumber, gd}
	}
	source(r, d.entry)
	if l.RepealedBy != nil {
		r.add("reference", refCols, "legislation", l.RepealedBy.LawID, "repeals", l.ID, nil, nil,
			l.RepealedBy.Label, "centre")
		b.rep.Refs["legislation repeals (centre)"]++
	}
	if d.text == nil {
		return
	}
	source(r, *d.textE)
	b.rep.WithText++
	kept := map[int]bool{}
	for _, s := range d.text.Sections {
		if b.sections[s.ID] {
			b.rep.note("section %d appears again in law %d; kept the first", s.ID, l.ID)
			continue
		}
		parent := s.ParentID
		if parent != nil && !kept[*parent] {
			parent = nil
		}
		r.add("legislation_section", sectionCols, s.ID, l.ID, parent, s.Position, s.Depth, s.Heading, s.Note)
		b.sections[s.ID], kept[s.ID] = true, true
	}
	for _, a := range d.text.Articles {
		sec := a.SectionID
		if sec != nil && !kept[*sec] {
			sec = nil
		}
		cid := a.CentreID
		if cid != nil {
			if b.articleIDs[*cid] {
				cid = nil
			} else {
				b.articleIDs[*cid] = true
			}
		}
		r.add("legislation_article", articleCols, l.ID, a.Position, cid, sec, a.Number, a.NumberInt, a.Note,
			a.TextAr, a.TextFr, a.Images, SourceUL, ulSource(l.ID))
		switch {
		case len(a.Images) > 0 && a.TextAr == "":
			b.rep.ImageArticles++
		case a.TextAr == "" && a.Note == nil:
			b.rep.EmptyArticles++
		}
	}
	b.rep.Articles += len(d.text.Articles)
	b.rep.Repeats += d.text.Repeats
	printed := -1
	if d.text.PrintedCount != nil {
		printed = *d.text.PrintedCount
	}
	switch {
	case len(d.text.Articles) == 0 && printed > 0:
		b.rep.NoArticles = append(b.rep.NoArticles, l.ID)
	case printed >= 0 && printed != d.text.Counted():
		b.rep.CountMismatch = append(b.rep.CountMismatch, [3]int{l.ID, printed, d.text.Counted()})
	default:
		b.rep.CountMatch++
	}
}

// cites turns the editors' links into reference rows. A link to a text we
// do not hold keeps its words but no law_id.
func (b *builder) cites(r *rows, fromType string, fromID int, cs []parse.Cite) {
	seen := map[string]bool{}
	for _, c := range cs {
		var law any
		if b.laws[c.LawID] {
			law = c.LawID
		}
		key := fmt.Sprint(law, "|", deref(c.ArticleNumber), "|", derefInt(c.ArticleCentreID), "|", c.Label)
		if seen[key] {
			continue
		}
		seen[key] = true
		if law == nil {
			b.rep.Refs[fromType+" cites a text not in the corpus"]++
		}
		r.add("reference", refCols, fromType, fromID, "cites", law, c.ArticleNumber, c.ArticleCentreID, c.Label, "centre")
		b.rep.Refs[fromType+" cites (centre)"]++
	}
}

func (b *builder) rulings() error {
	cols := []string{"id", "court", "number", "year", "decided_on", "title", "president", "members", "text_ar"}
	return b.simple("ruling", func(e archive.Entry, raw []byte) (any, bool, error) {
		return parse.ParseRuling(e.ID, raw)
	}, func(r *rows, e archive.Entry, v any) error {
		x := v.(*parse.Ruling)
		if x.Court != nil {
			if err := b.ensure("court", "court", *x.Court, b.courts); err != nil {
				return err
			}
		}
		r.add("ruling", cols, x.ID, x.Court, x.Number, x.Year, x.DecidedOn, x.Title, x.President, x.Members, nullable(x.TextAr))
		b.cites(r, "ruling", x.ID, x.Cites)
		return nil
	})
}

func (b *builder) opinions() error {
	cols := []string{"id", "number", "year", "issued_on", "requester", "file_number", "file_year", "subject", "text_ar"}
	return b.simple("opinion", func(e archive.Entry, raw []byte) (any, bool, error) {
		return parse.ParseOpinion(e.ID, raw)
	}, func(r *rows, e archive.Entry, v any) error {
		x := v.(*parse.Opinion)
		r.add("opinion", cols, x.ID, x.Number, x.Year, x.IssuedOn, x.Requester, x.FileNumber, x.FileYear, x.Subject, x.TextAr)
		b.cites(r, "opinion", x.ID, x.Cites)
		return nil
	})
}

func (b *builder) agreements() error {
	cols := []string{"id", "title", "scope", "signed_on", "ratified_by", "ratifying_law_id", "text_ar"}
	return b.simple("agreement", func(e archive.Entry, raw []byte) (any, bool, error) {
		return parse.ParseAgreement(e.ID, raw)
	}, func(r *rows, e archive.Entry, v any) error {
		x := v.(*parse.Agreement)
		var law any
		if x.RatifyingLawID != nil && b.laws[*x.RatifyingLawID] {
			law = *x.RatifyingLawID
		}
		r.add("agreement", cols, x.ID, x.Title, x.Scope, x.SignedOn, x.RatifiedBy, law, x.TextAr)
		return nil
	})
}

func (b *builder) doctrine() error {
	cols := []string{"id", "title", "authors", "series", "pages", "abstract"}
	return b.simple("article", func(e archive.Entry, raw []byte) (any, bool, error) {
		return parse.ParseDoctrine(e.ID, raw)
	}, func(r *rows, e archive.Entry, v any) error {
		x := v.(*parse.Doctrine)
		r.add("doctrine_article", cols, x.ID, x.Title, x.Authors, x.Series, x.Pages, x.Abstract)
		b.cites(r, "doctrine_article", x.ID, x.Cites)
		return nil
	})
}

func (b *builder) research() error {
	cols := []string{"id", "title", "author", "published_on", "text_ar"}
	return b.simple("research", func(e archive.Entry, raw []byte) (any, bool, error) {
		return parse.ParseResearch(e.ID, raw)
	}, func(r *rows, e archive.Entry, v any) error {
		x := v.(*parse.Research)
		r.add("research", cols, x.ID, x.Title, x.Author, x.PublishedOn, x.TextAr)
		b.cites(r, "research", x.ID, x.Cites)
		return nil
	})
}

type parsed struct {
	e  archive.Entry
	v  any
	ok bool
}

// simple loads a document type with one row per page, skipping the Centre's
// blank records and counting them.
func (b *builder) simple(typ string, parseOne func(archive.Entry, []byte) (any, bool, error),
	add func(*rows, archive.Entry, any) error) error {
	entries, err := b.Archive.Index(typ)
	if err != nil {
		return err
	}
	return chunked(b, entries, func(e archive.Entry) (parsed, error) {
		raw, err := b.Archive.Read(typ, e.ID)
		if err != nil {
			return parsed{}, err
		}
		v, ok, err := parseOne(e, raw)
		return parsed{e, v, ok}, err
	}, func(ps []parsed) error {
		r := newRows()
		for _, p := range ps {
			if !p.ok {
				b.rep.Blank[typ]++
				continue
			}
			if err := add(r, p.e, p.v); err != nil {
				return err
			}
			source(r, p.e)
			b.rep.Loaded[typ]++
		}
		return b.flush(r)
	})
}

// gazetteIssues loads each issue's contents; the issue's own number and date
// are the ones printed on most of the texts it lists.
func (b *builder) gazetteIssues() error {
	entries, err := b.Archive.Index("journal")
	if err != nil {
		return err
	}
	return chunked(b, entries, func(e archive.Entry) (parsed, error) {
		raw, err := b.Archive.Read("journal", e.ID)
		if err != nil {
			return parsed{}, err
		}
		g, ok, err := parse.ParseGazette(e.ID, raw)
		return parsed{e, g, ok}, err
	}, func(ps []parsed) error {
		r := newRows()
		for _, p := range ps {
			if !p.ok {
				b.rep.Blank["journal"]++
				continue
			}
			g := p.v.(*parse.GazetteIssue)
			votes := map[[2]string]int{}
			var entries []parse.GazetteEntry
			for _, en := range g.Entries {
				if !b.laws[en.LawID] {
					continue
				}
				entries = append(entries, en)
				if v, ok := b.gazette[en.LawID]; ok {
					votes[v]++
				}
			}
			var best [2]string
			var keys [][2]string
			for k := range votes {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool {
				if votes[keys[i]] != votes[keys[j]] {
					return votes[keys[i]] > votes[keys[j]]
				}
				return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1]
			})
			if len(keys) > 0 {
				best = keys[0]
			}
			r.add("gazette_issue", []string{"id", "number", "published_on"}, g.ID, nullable(best[0]), nullable(best[1]))
			for _, en := range entries {
				r.add("gazette_entry", []string{"issue_id", "law_id", "pages"}, g.ID, en.LawID, en.Pages)
			}
			source(r, p.e)
			b.rep.Loaded["journal"]++
		}
		return b.flush(r, "gazette_issue", "gazette_entry")
	})
}

// codes resolves the curated list by exact title: the text in force with the
// most articles, most recent first.
func (b *builder) codes() error {
	pos := 0
	for _, line := range strings.Split(db.Codes, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || strings.HasPrefix(line, "#") || f[0] == "slug" {
			continue
		}
		var id int
		err := b.conn.QueryRow(b.ctx, `
			SELECT l.id FROM legislation l WHERE l.title = $1
			ORDER BY (l.status = 'in_force') DESC,
			         (SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id) DESC,
			         l.issued_on DESC NULLS LAST, l.id DESC
			LIMIT 1`, f[1]).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			b.rep.note("code %s: no text titled %q", f[0], f[1])
			continue
		} else if err != nil {
			return err
		}
		pos++
		if _, err := b.conn.Exec(b.ctx, "INSERT INTO code (slug, law_id, name_en, name_fr, position) VALUES ($1,$2,$3,$4,$5)",
			f[0], id, f[2], nullable(f[3]), pos); err != nil {
			return err
		}
	}
	b.rep.Codes = pos
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
