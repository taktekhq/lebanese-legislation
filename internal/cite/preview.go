package cite

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A preview of the linked display, for review before the site changes:
// each amended article as the Centre publishes it, then every amending
// article in the sample that cites it, verbatim, oldest first. Nothing is
// merged into the article and nothing is hidden when the Centre's text
// already carries the change.

type pvChange struct {
	Action, Part, Why, Quote, CiteID string
	ByIdent, ByTitle, ByArticle      string
	ByDate                           string
	ByURL                            string
	Text                             string
	FromParser                       bool
	date                             time.Time
}

type pvArticle struct {
	LawIdent, LawTitle, Article, URL string
	Text                             string
	Changes                          []pvChange
}

type pvGap struct{ CiteID, How, Quote, From string }

// Preview writes an HTML page with up to n amended articles, picked for
// variety (whole replacements, edits, repeals, additions, links only the
// model found), and the citations nothing in the data matched.
func Preview(ctx context.Context, db *pgxpool.Pool, cs map[string]Citation, n int, base string, w io.Writer) error {
	ix, err := LawIndex(ctx, db)
	if err != nil {
		return err
	}
	parsed := map[string]bool{}
	rows, err := db.Query(ctx, `SELECT DISTINCT by_law_id || '/' || by_article || '>' || coalesce(law_id, 0) || '/' || coalesce(article_number, '') FROM amendment`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k string
		rows.Scan(&k)
		parsed[k] = true
	}
	rows.Close()

	type tkey struct {
		law int
		art string
	}
	byTarget := map[tkey][]pvChange{}
	var gaps []pvGap
	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var byLaw int
		var byArt string
		fmt.Sscanf(k, "%d/", &byLaw)
		byArt = k[strings.Index(k, "/")+1:]
		for _, lk := range Resolve(ix, byLaw, cs[k]) {
			if !lk.Valid {
				continue
			}
			if lk.LawID == 0 {
				gaps = append(gaps, pvGap{CiteID: lk.Target.ID(), How: lk.How, Quote: lk.Quote, From: k})
				continue
			}
			if lk.Target.Article == "" {
				continue // a change to a text as a whole shows on the text's page, not an article's
			}
			by := ix.Law(byLaw)
			c := pvChange{Action: lk.Action, Part: lk.Target.Part, Why: lk.Why, Quote: lk.Quote, CiteID: lk.Target.ID(),
				ByArticle: byArt, ByURL: fmt.Sprintf("%s/law/%d/article/%s/", base, byLaw, byArt),
				FromParser: parsed[fmt.Sprintf("%s>%d/%s", k, lk.LawID, lk.Target.Article)]}
			if by != nil {
				c.ByTitle = by.Title
				c.date = by.Issued
				if !by.Gazette.IsZero() {
					c.date = by.Gazette
				}
				if !c.date.IsZero() {
					c.ByDate = c.date.Format("2 Jan 2006")
				}
			}
			byTarget[tkey{lk.LawID, lk.Target.Article}] = append(byTarget[tkey{lk.LawID, lk.Target.Article}], c)
		}
	}

	// Pick for variety: one of each action first, then links only the
	// model found, then the rest, all in a fixed order.
	var targets []tkey
	for t := range byTarget {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].law != targets[j].law {
			return targets[i].law < targets[j].law
		}
		return targets[i].art < targets[j].art
	})
	var picked []tkey
	seen := map[tkey]bool{}
	take := func(ok func([]pvChange) bool, max int) {
		for _, t := range targets {
			if len(picked) >= 2*n || max == 0 {
				return
			}
			if !seen[t] && ok(byTarget[t]) {
				seen[t] = true
				picked = append(picked, t)
				max--
			}
		}
	}
	has := func(action string) func([]pvChange) bool {
		return func(cs []pvChange) bool {
			for _, c := range cs {
				if c.Action == action {
					return true
				}
			}
			return false
		}
	}
	take(has("replace"), 2)
	take(has("amend"), 2)
	take(has("repeal"), 1)
	take(has("insert"), 1)
	take(func(cs []pvChange) bool {
		for _, c := range cs {
			if !c.FromParser {
				return true
			}
		}
		return false
	}, 3)
	take(func([]pvChange) bool { return true }, 2*n)

	var page []pvArticle
	for _, t := range picked {
		if len(page) >= n {
			break
		}
		var a pvArticle
		err := db.QueryRow(ctx, `SELECT l.kind || coalesce(' رقم ' || l.number, '') || coalesce(' تاريخ ' || to_char(l.issued_on, 'DD/MM/YYYY'), ''),
			coalesce(l.title, ''), coalesce(a.text_ar, '') FROM legislation l JOIN legislation_article a ON a.law_id = l.id
			WHERE l.id = $1 AND a.number = $2`, t.law, t.art).Scan(&a.LawIdent, &a.LawTitle, &a.Text)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the citation names an article the text doesn't have
		}
		if err != nil {
			return fmt.Errorf("article %d/%s: %w", t.law, t.art, err)
		}
		a.Article = t.art
		a.URL = fmt.Sprintf("%s/law/%d/article/%s/", base, t.law, t.art)
		chs := byTarget[t]
		for i := range chs {
			var byLaw int
			fmt.Sscanf(chs[i].ByURL[len(base):], "/law/%d/", &byLaw)
			db.QueryRow(ctx, `SELECT l.kind || coalesce(' رقم ' || l.number, '') || coalesce(' تاريخ ' || to_char(l.issued_on, 'DD/MM/YYYY'), ''),
				coalesce(a.text_ar, '') FROM legislation l JOIN legislation_article a ON a.law_id = l.id WHERE l.id = $1 AND a.number = $2`,
				byLaw, chs[i].ByArticle).Scan(&chs[i].ByIdent, &chs[i].Text)
		}
		sort.SliceStable(chs, func(i, j int) bool { return chs[i].date.Before(chs[j].date) })
		a.Changes = chs
		page = append(page, a)
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].CiteID < gaps[j].CiteID })
	return previewTmpl.Execute(w, map[string]any{"Articles": page, "Gaps": gaps, "Count": len(cs)})
}

var actionLabel = map[string]string{
	"replace": "Replaced or reworded", "amend": "Edited in part", "repeal": "Repealed",
	"insert": "Added to", "suspend": "Suspended", "other": "Changed",
}

var previewTmpl = template.Must(template.New("p").Funcs(template.FuncMap{
	"label": func(a string) string {
		if l, ok := actionLabel[a]; ok {
			return l
		}
		return a
	},
	"paras": func(s string) []string {
		var out []string
		for _, p := range strings.Split(s, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Amendment links preview</title>
<style>
:root{--bg:#faf8f4;--fg:#1d1b18;--muted:#6b665d;--line:#e2ddd3;--card:#fff;--accent:#8a4b1f;--new:#f3ecdf}
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){--bg:#171614;--fg:#ece8e1;--muted:#a39d92;--line:#33302b;--card:#1f1d1a;--accent:#e0a36f;--new:#2a2520}}
:root[data-theme="dark"]{--bg:#171614;--fg:#ece8e1;--muted:#a39d92;--line:#33302b;--card:#1f1d1a;--accent:#e0a36f;--new:#2a2520}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:860px;margin:0 auto;padding:24px 16px 64px}
h1{font-size:1.5rem;margin:0 0 4px}.lede{color:var(--muted);margin:0 0 32px}
.art{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:20px;margin:0 0 28px}
.who{font-size:.85rem;color:var(--muted);margin:0 0 4px}
.ar{direction:rtl;text-align:justify;font-family:"Noto Naskh Arabic","Geeza Pro",serif;font-size:1.08rem;line-height:1.9}
.ar p{margin:0 0 .5em}
h2{font-size:1.15rem;margin:0 0 12px;direction:rtl;text-align:right}
.chg{border-top:1px solid var(--line);margin-top:18px;padding-top:14px}
.chg .meta{font-size:.9rem;margin:0 0 8px}.chg .meta b{color:var(--accent)}
.chg .ar{background:var(--new);border-radius:8px;padding:12px 14px}
.tag{display:inline-block;font-size:.75rem;border:1px solid var(--line);border-radius:99px;padding:0 8px;margin-inline-start:6px;color:var(--muted)}
code{font-size:.8rem;word-break:break-all}
table{width:100%;border-collapse:collapse;font-size:.85rem}td{border-top:1px solid var(--line);padding:6px 4px;vertical-align:top}
a{color:var(--accent)}
</style></head><body><main>
<h1>Amendment links: preview</h1>
<p class="lede">{{len .Articles}} amended articles from a test of {{.Count}} amending articles. Each shows the article as the Lebanese University publishes it, then every amending article from the test that cites it, word for word, oldest first. Nothing is merged or hidden. Other amendments outside the test sample may exist.</p>
{{range .Articles}}<section class="art">
<p class="who" dir="rtl">{{.LawIdent}} · {{.LawTitle}}</p>
<h2><a href="{{.URL}}">المادة {{.Article}}</a></h2>
<p class="who">As the Lebanese University publishes it:</p>
<div class="ar">{{range paras .Text}}<p>{{.}}</p>{{end}}</div>
{{range .Changes}}<div class="chg">
<p class="meta"><b>{{label .Action}}</b>{{with .Part}} (<bdi lang="ar">{{.}}</bdi>){{end}} by <a href="{{.ByURL}}"><bdi lang="ar">{{.ByIdent}}</bdi>, article {{.ByArticle}}</a>{{with .ByDate}}, {{.}}{{end}}{{if not .FromParser}}<span class="tag">new: the current parser missed it</span>{{end}}<br><code>{{.CiteID}}</code></p>
<div class="ar">{{range paras .Text}}<p>{{.}}</p>{{end}}</div>
</div>{{end}}
</section>{{end}}
<h2 style="direction:ltr;text-align:left">Citations with no match in our data ({{len .Gaps}})</h2>
<p class="lede">These link themselves once the data has the text.</p>
<table>{{range .Gaps}}<tr><td><code>{{.CiteID}}</code></td><td>{{.How}}</td><td dir="rtl">{{.Quote}}</td></tr>{{end}}</table>
</main></body></html>`))
