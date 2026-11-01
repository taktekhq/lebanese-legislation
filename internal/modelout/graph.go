package modelout

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/cite"
)

// The graph (db/graph.sql): every link we know between two items, from the
// model's citations (db/model/citations.jsonl, matched here against the data
// every build, so a target that arrives later links itself), the Centre's
// own links (reference), the gazette's contents, and the pattern pass
// (amendment).

type edge struct {
	fromKey, toKey, relation, source, status string
	fromType                                 string
	fromID                                   int
	fromArticle, toArticle, toCite           string
	toLaw                                    int
	part, how, quote, model                  string
}

func lawKey(id int) string { return "law/" + strconv.Itoa(id) }
func artKey(id int, n string) string {
	return "law/" + strconv.Itoa(id) + "/art/" + n
}

// GraphInput is what Graph links against: the build's law index and each
// text's article numbers, and the model's citation files.
type GraphInput struct {
	Index   *amend.Index            // the build's law index (amend step); the person-checked aliases are added here
	Numbers map[int]map[string]bool // each text's article numbers
	// Citations is the model's citations file (default DefaultCitations).
	Citations string
	// RulingCitations is the model's reading of rulings, comma-separated
	// files (default DefaultRulingCitations).
	RulingCitations string
	// Indexes makes graph_edge's indexes: for a graph-only rerun, since a
	// full build makes them with the others.
	Indexes bool
	// PriorIDs is last build's edge ids (ReadPriorEdgeIDs): an edge that is
	// still there keeps its id.
	PriorIDs PriorEdgeIDs
	Note     func(string, ...any)
}

// GraphStats is what Graph wrote.
type GraphStats struct {
	Edges  map[EdgeKind]int // graph edges by source and status
	ArtNum ArtNumStats      // what the one-spelling fold did to the edges
	// Rejected is the model's links a person rejected (db/amendments.tsv).
	Rejected int
}

// dropRejected leaves out the model's links from an amending article that a
// person rejected: the Centre's text of Decree-law 112/1983, article 13,
// names Penal Code article 51, but the wording it gives is article 251's.
func dropRejected(edges []edge, ds []amend.Decision) ([]edge, int) {
	out, n := edges[:0], 0
	for _, e := range edges {
		rejected := false
		if e.source == "model" && e.fromType == "legislation" {
			for _, d := range ds {
				if d.Decision == amend.DecideReject && d.Matches(e.fromID, e.fromArticle, e.toLaw, e.toArticle) {
					rejected = true
					break
				}
			}
		}
		if rejected {
			n++
			continue
		}
		out = append(out, e)
	}
	return out, n
}

// EdgeKind is an edge's source and status, "model linked".
type EdgeKind string

// DefaultCitations is the model's citations of the texts' articles.
const DefaultCitations = "db/model/citations.jsonl"

// Graph writes graph_edge (db/graph.sql, its table made by the caller):
// every link between two items, from the model's citations, the Centre's
// own links, the gazette's contents, the pattern pass and db/amounts.tsv.
func Graph(ctx context.Context, conn *pgx.Conn, in GraphInput) (GraphStats, error) {
	var gs GraphStats
	ix, numbers := in.Index, in.Numbers
	// Names a person has checked (db/aliases.tsv) apply to the model's
	// citations only: the pattern pass builds its own index.
	names, err := amend.ParseNameAliases(db.Aliases)
	if err != nil {
		return gs, err
	}
	ix.AllowLater()
	ix.AllowDateSlip()
	for _, id := range ix.SetNameAliases(names) {
		in.Note("aliases.tsv: text %d is not in the data", id)
	}
	var edges []edge

	// The model's citations. A missing file is an empty graph from this source.
	path := in.Citations
	if path == "" {
		path = DefaultCitations
	}
	cs, err := cite.Load(path)
	if err != nil {
		return gs, fmt.Errorf("%s: %w", path, err)
	}
	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	resolved := cite.ResolveAll(ix, cs)
	for _, k := range keys {
		c := cs[k]
		var byLaw int
		if _, err := fmt.Sscanf(k, "%d/", &byLaw); err != nil || ix.Law(byLaw) == nil {
			continue // the amending text is gone from the data
		}
		byArt := fromArticle(k[len(strconv.Itoa(byLaw))+1:], numbers, byLaw, new(ArtNumStats))
		if !numbers[byLaw][byArt] {
			continue
		}
		for _, lk := range resolved[k] {
			if !lk.Valid {
				continue // its quote isn't the article's words: not used
			}
			e := edge{fromKey: artKey(byLaw, byArt), relation: lk.Action, source: "model",
				fromType: "legislation", fromID: byLaw, fromArticle: byArt,
				toArticle: lk.Target.Article, toCite: lk.Target.ID(), part: lk.Target.Part,
				how: lk.How, quote: lk.Quote, model: c.Model}
			switch {
			case lk.Action == "general_repeal":
				e.status = "general"
			case lk.LawID == 0:
				e.status = "unresolved"
			case lk.Target.Article == "":
				e.status, e.toLaw, e.toKey = "linked", lk.LawID, lawKey(lk.LawID)
			case numbers[lk.LawID][lk.Target.Article]:
				e.status, e.toLaw, e.toKey = "linked", lk.LawID, artKey(lk.LawID, lk.Target.Article)
			default:
				// The text is ours but not that article (an article it adds,
				// or a numbering we don't share): link the text.
				e.status, e.toLaw, e.toKey = "no_article", lk.LawID, lawKey(lk.LawID)
			}
			edges = append(edges, e)
		}
	}

	// What the model found rulings apply, cite or interpret.
	rulingEdges, err := rulingEdges(ctx, conn, in.RulingCitations, ix, numbers)
	if err != nil {
		return gs, err
	}
	edges = append(edges, rulingEdges...)

	// The Centre's own links.
	rows, err := conn.Query(ctx, `SELECT from_type, from_id, relation, law_id, coalesce(article_number, '')
		FROM reference WHERE law_id IS NOT NULL ORDER BY from_type, from_id, law_id, article_number`)
	if err != nil {
		return gs, err
	}
	for rows.Next() {
		var typ, rel, art string
		var from, law int
		if err := rows.Scan(&typ, &from, &rel, &law, &art); err != nil {
			return gs, err
		}
		fk := typ + "/" + strconv.Itoa(from)
		if typ == "legislation" {
			fk = lawKey(from)
		}
		e := edge{fromKey: fk, relation: rel, source: "centre", status: "linked", fromType: typ, fromID: from,
			toLaw: law, toArticle: art, toKey: lawKey(law), how: "the Centre's link"}
		if art != "" {
			if numbers[law][art] {
				e.toKey = artKey(law, art)
			} else {
				e.status = "no_article"
			}
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return gs, err
	}

	// Gazette issues and the texts they published.
	rows, err = conn.Query(ctx, `SELECT issue_id, law_id FROM gazette_entry ORDER BY issue_id, law_id`)
	if err != nil {
		return gs, err
	}
	for rows.Next() {
		var issue, law int
		if err := rows.Scan(&issue, &law); err != nil {
			return gs, err
		}
		edges = append(edges, edge{fromKey: "gazette/" + strconv.Itoa(issue), toKey: lawKey(law), relation: "publishes",
			source: "centre", status: "linked", fromType: "gazette", fromID: issue, toLaw: law, how: "the gazette's contents"})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return gs, err
	}

	// The pattern pass, where it found the target.
	rows, err = conn.Query(ctx, `SELECT by_law_id, by_article, action, law_id, coalesce(article_number, ''),
		coalesce(part, ''), coalesce(resolved_by, ''), coalesce(evidence, '')
		FROM amendment WHERE law_id IS NOT NULL AND status <> 'rejected' ORDER BY id`)
	if err != nil {
		return gs, err
	}
	for rows.Next() {
		var byLaw, law int
		var byArt, action, art, part, how, ev string
		if err := rows.Scan(&byLaw, &byArt, &action, &law, &art, &part, &how, &ev); err != nil {
			return gs, err
		}
		e := edge{fromKey: artKey(byLaw, byArt), relation: action, source: "parser", status: "linked",
			fromType: "legislation", fromID: byLaw, fromArticle: byArt, toLaw: law, toArticle: art,
			toKey: lawKey(law), part: part, how: how, quote: ev}
		if art != "" {
			if numbers[law][art] {
				e.toKey = artKey(law, art)
			} else {
				e.status = "no_article"
			}
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return gs, err
	}

	// Texts that change every sum in another (db/amounts.tsv), linked to
	// each article in scope that states one.
	amounts, err := amounts(ctx, conn, numbers, in.Note)
	if err != nil {
		return gs, err
	}
	edges = append(edges, amounts...)

	nz := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	nzi := func(i int) any {
		if i == 0 {
			return nil
		}
		return i
	}
	// One spelling for article numbers, whichever source wrote the edge.
	edges = canonEdges(edges, numbers, &gs.ArtNum)
	// A person's rejections (db/amendments.tsv) take the model's links too.
	decisions, err := amend.ParseDecisions(db.Amendments)
	if err != nil {
		return gs, err
	}
	edges, gs.Rejected = dropRejected(edges, decisions)
	out := make([][]any, len(edges))
	ids := edgeIDs(edges, in.PriorIDs.byIdentity)
	gs.Edges = map[EdgeKind]int{}
	for i, e := range edges {
		out[i] = []any{ids[i], e.fromKey, nz(e.toKey), e.relation, e.source, e.status, e.fromType, e.fromID,
			nz(e.fromArticle), nzi(e.toLaw), nz(e.toArticle), nz(e.toCite), nz(e.part), nz(e.how), nz(e.quote), nz(e.model)}
		gs.Edges[EdgeKind(e.source+" "+e.status)]++
	}
	_, err = conn.CopyFrom(ctx, pgx.Identifier{"graph_edge"},
		[]string{"id", "from_key", "to_key", "relation", "source", "status", "from_type", "from_id",
			"from_article", "to_law_id", "to_article", "to_cite", "part", "how", "quote", "model"},
		pgx.CopyFromRows(out))
	if err != nil {
		return gs, err
	}
	if in.Indexes { // a full build makes them with the other indexes
		if _, err := conn.Exec(ctx, `CREATE INDEX IF NOT EXISTS graph_edge_from ON graph_edge (from_key);
			CREATE INDEX IF NOT EXISTS graph_edge_to ON graph_edge (to_key);
			CREATE INDEX IF NOT EXISTS graph_edge_to_law ON graph_edge (to_law_id);
			CREATE INDEX IF NOT EXISTS graph_edge_from_id ON graph_edge (from_type, from_id)`); err != nil {
			return gs, err
		}
	}
	return gs, nil
}

// rulingEdges turns the model's reading of rulings (the ruling citations
// file) into edges from each ruling to the texts and articles it names,
// resolved by the same resolver as the articles' citations, with the
// ruling's date as the day targets must lie before. A missing file is none.
func rulingEdges(ctx context.Context, conn *pgx.Conn, paths string, ix *amend.Index, numbers map[int]map[string]bool) ([]edge, error) {
	cs, err := loadRulingCitations(paths)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, nil
	}
	dates := map[int]time.Time{}
	rows, err := conn.Query(ctx, `SELECT id, decided_on FROM ruling`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		var d *time.Time
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		if d != nil {
			dates[id] = *d
		} else {
			dates[id] = time.Time{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rulingLinks(cs, cite.ResolveRulings(ix, cs, dates), dates, numbers), nil
}

// rulingLinks makes the edges of the resolved readings of rulings, in key
// order: only answers whose quote is the ruling's own words, only rulings
// still in the data (dates has them all), a linked edge to the article when
// the text has it and to the text when it does not.
func rulingLinks(cs map[string]cite.Citation, resolved map[string][]cite.Link, dates map[int]time.Time, numbers map[int]map[string]bool) []edge {
	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []edge
	for _, k := range keys {
		id := cite.RulingID(k)
		if _, ok := dates[id]; !ok {
			continue // the ruling is gone from the data
		}
		c := cs[k]
		for _, lk := range resolved[k] {
			if !lk.Valid {
				continue // its quote isn't the ruling's words: not used
			}
			e := edge{fromKey: "ruling/" + strconv.Itoa(id), relation: lk.Action, source: "model",
				fromType: "ruling", fromID: id, toArticle: lk.Target.Article, toCite: lk.Target.ID(),
				part: lk.Target.Part, how: lk.How, quote: lk.Quote, model: c.Model}
			switch {
			case lk.LawID == 0:
				e.status = "unresolved"
			case lk.Target.Article == "":
				e.status, e.toLaw, e.toKey = "linked", lk.LawID, lawKey(lk.LawID)
			case numbers[lk.LawID][lk.Target.Article]:
				e.status, e.toLaw, e.toKey = "linked", lk.LawID, artKey(lk.LawID, lk.Target.Article)
			default:
				e.status, e.toLaw, e.toKey = "no_article", lk.LawID, lawKey(lk.LawID)
			}
			out = append(out, e)
		}
	}
	return out
}

// amounts links each text in db/amounts.tsv to every article of the text it
// changes that states a sum in Lebanese pounds (and names a fine, for a
// text that changes fines only), within the listed range.
func amounts(ctx context.Context, conn *pgx.Conn, numbers map[int]map[string]bool, note func(string, ...any)) ([]edge, error) {
	var out []edge
	for _, line := range strings.Split(db.Amounts, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 6 || strings.HasPrefix(line, "#") || f[0] == "by_law_id" {
			continue
		}
		fines := ""
		switch f[5] {
		case "sums":
		case "fines":
			fines = "غرام|يغرم|تغرم|تُغرَّم"
		default:
			return nil, fmt.Errorf("amounts.tsv: %q: changes must be sums or fines", line)
		}
		byLaw, err1 := strconv.Atoi(f[0])
		law, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("amounts.tsv: bad line %q", line)
		}
		byArt := f[1]
		if !numbers[byLaw][byArt] || numbers[law] == nil {
			note("amounts.tsv: %s: a text or article is not in the data", line)
			continue
		}
		from, to := 0, 1<<30
		if f[3] != "" {
			from, _ = strconv.Atoi(f[3])
		}
		if f[4] != "" {
			to, _ = strconv.Atoi(f[4])
		}
		rows, err := conn.Query(ctx, `SELECT number, substring(text_ar FROM '[^.،:؛]{0,60}(?:ليرة|ليرات|ل\.\s?ل)')
			FROM legislation_article
			WHERE law_id = $1 AND coalesce(number_int, 0) BETWEEN $2 AND $3
			  AND text_ar ~ '(ليرة|ليرات|ل\.\s?ل)' AND ($4 = '' OR text_ar ~ $4)
			ORDER BY position`, law, from, to, fines)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var art string
			var quote *string
			if err := rows.Scan(&art, &quote); err != nil {
				rows.Close()
				return nil, err
			}
			e := edge{fromKey: artKey(byLaw, byArt), toKey: artKey(law, art), relation: "amend", source: "editorial",
				status: "linked", fromType: "legislation", fromID: byLaw, fromArticle: byArt, toLaw: law,
				toArticle: art, part: "المبالغ المالية", how: "changes every sum in the text; this article states one"}
			if quote != nil {
				e.quote = strings.TrimSpace(*quote)
			}
			out = append(out, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DefaultRulingCitations are the model's readings of rulings: the full texts
// and the short summaries, read separately (citeagy -sample all / short).
const DefaultRulingCitations = "db/model/ruling_citations.jsonl,db/model/ruling_citations_short.jsonl"

// loadRulingCitations reads a comma-separated list of ruling citation files
// ("" is DefaultRulingCitations) into one set; a key in a later file wins.
func loadRulingCitations(paths string) (map[string]cite.Citation, error) {
	if paths == "" {
		paths = DefaultRulingCitations
	}
	all := map[string]cite.Citation{}
	for _, path := range strings.Split(paths, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cs, err := cite.Load(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for k, c := range cs {
			all[k] = c
		}
	}
	return all, nil
}
