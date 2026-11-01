package modelout

import "github.com/taktekhq/lebanese-legislation/internal/amend"

// One spelling for article numbers (amend.Canon): the Centre's. Citations
// write "12/1", "19.1", "3,8", "الوحيدة", "103 جديدة"; every edge's articles
// are folded here, when the graph is written, against the numbers of the
// text they name, so the graph holds one spelling and one article never
// shows twice. The model's files are left as the model answered.

// ArtNumStats is what the fold did to the graph's edges.
type ArtNumStats struct {
	Folded      int // article numbers written another way, now in the Centre's
	Split       int // spellings that named several articles (lists, ranges), now one edge each
	Parts       int // "19.1" kept as article 19 with part "1": the text has no article 19-1
	Unreadable  int // spellings naming no article ("البند الثاني"): the edge links the text, the words kept as its part
	NotFound    int // folded numbers the named text does not have (kept)
	Merged      int // edges identical after folding, collapsed
	MergedPlain int // of those, already identical before (the same citation twice)
}

// artSet answers "does this text number an article so" for a law.
func artSet(numbers map[int]map[string]bool, law int) func(string) bool {
	set, ok := numbers[law]
	if !ok {
		return func(string) bool { return false }
	}
	return func(n string) bool { return set[n] }
}

// fromArticle reads the article an edge comes from against the text it is
// in. A list or a part does not make sense here: the first article found.
func fromArticle(raw string, numbers map[int]map[string]bool, law int, st *ArtNumStats) string {
	if raw == "" {
		return ""
	}
	has := artSet(numbers, law)
	for _, n := range amend.Resolve(raw, has) {
		if n.Article != "" && n.Found {
			if n.Article != raw {
				st.Folded++
			}
			return n.Article
		}
	}
	for _, n := range amend.Resolve(raw, nil) {
		if n.Article != "" {
			if n.Article != raw {
				st.Folded++
			}
			return n.Article
		}
	}
	return raw
}

// canonEdges folds every edge's articles and collapses those that become
// identical. An edge naming several articles becomes one edge for each.
func canonEdges(edges []edge, numbers map[int]map[string]bool, st *ArtNumStats) []edge {
	type key struct {
		from, to, toArt, toCite, relation, source, part, quote string
		toLaw                                                  int
	}
	seen := map[key]bool{}
	out := make([]edge, 0, len(edges))
	for _, e := range edges {
		origFrom, origTo := e.fromArticle, e.toArticle
		if e.fromArticle != "" && e.fromType == "legislation" {
			if a := fromArticle(e.fromArticle, numbers, e.fromID, st); a != e.fromArticle {
				e.fromArticle, e.fromKey = a, artKey(e.fromID, a)
			}
		}
		parts := []edge{e}
		if e.toArticle != "" {
			parts = parts[:0]
			var has func(string) bool
			if e.toLaw > 0 {
				has = artSet(numbers, e.toLaw)
			}
			res := amend.Resolve(e.toArticle, has)
			if len(res) == 0 {
				res = []amend.Number{{Part: e.toArticle}}
			}
			if len(res) > 1 {
				st.Split++
			}
			for _, n := range res {
				x := e
				x.toArticle = n.Article
				if n.Article != "" && n.Article != e.toArticle {
					st.Folded++
				}
				if n.Part != "" && x.part == "" {
					x.part = n.Part
					if n.Article != "" {
						st.Parts++
					}
				}
				switch {
				case n.Article == "":
					st.Unreadable++
				case e.toLaw > 0 && !n.Found:
					st.NotFound++
				}
				if e.toLaw > 0 { // where it points follows
					switch {
					case n.Article == "":
						x.status, x.toKey = "no_article", lawKey(e.toLaw)
					case n.Found:
						x.status, x.toKey = "linked", artKey(e.toLaw, n.Article)
					default:
						x.status, x.toKey = "no_article", lawKey(e.toLaw)
					}
				}
				parts = append(parts, x)
			}
		}
		for _, x := range parts {
			k := key{from: x.fromKey, to: x.toKey, toArt: x.toArticle, relation: x.relation, source: x.source, part: x.part,
				quote: x.quote, toLaw: x.toLaw}
			if x.toLaw == 0 { // not found in the data: what it names is all there is to tell two apart
				k.toCite = x.toCite
			}
			if seen[k] {
				st.Merged++
				if x.fromArticle == origFrom && x.toArticle == origTo {
					st.MergedPlain++
				}
				continue
			}
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}
