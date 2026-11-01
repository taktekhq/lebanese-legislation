package cite

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	lebdb "github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// LawIndex loads what the resolver needs to know of every text.
func LawIndex(ctx context.Context, db *pgxpool.Pool) (*amend.Index, error) {
	rows, err := db.Query(ctx, `SELECT l.id, l.kind, coalesce(l.number, ''), l.issued_on, l.gazette_date, l.title,
		l.status = 'in_force', EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id) FROM legislation l`)
	if err != nil {
		return nil, err
	}
	laws, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (amend.Law, error) {
		var l amend.Law
		var issued, gazette *time.Time
		err := row.Scan(&l.ID, &l.Kind, &l.Number, &issued, &gazette, &l.Title, &l.InForce, &l.Code)
		if issued != nil {
			l.Issued = *issued
		}
		if gazette != nil {
			l.Gazette = *gazette
		}
		return l, err
	})
	if err != nil {
		return nil, err
	}
	ix := amend.NewIndex(laws)
	names, err := amend.ParseNameAliases(lebdb.Aliases)
	if err != nil {
		return nil, err
	}
	ix.SetNameAliases(names)
	ix.AllowLater()
	ix.AllowDateSlip()
	return ix, nil
}

// Link is one citation matched against the data.
type Link struct {
	Change
	LawID  int    // 0: not in our data (yet)
	How    string // how it was matched, or why not
	HasArt bool   // the matched text has that article
}

// Resolve matches one article's changes against the data. byLaw is the
// amending text (for "this law" and the date before which targets lie).
func Resolve(ix *amend.Index, byLaw int, c Citation) []Link {
	var before time.Time
	if l := ix.Law(byLaw); l != nil {
		before = l.Issued
	}
	return resolve(ix, byLaw, before, c)
}

// resolve matches one reader's references. byLaw is 0 for a ruling, which
// has no text of its own to be "this text"; before is the date its targets
// lie before (zero: any).
func resolve(ix *amend.Index, byLaw int, before time.Time, c Citation) []Link {
	var out []Link
	for _, ch := range c.Changes {
		lk := Link{Change: ch}
		switch {
		case !ch.Valid:
			lk.How = "quote not in the article"
		case ch.Action == "general_repeal":
			lk.How = "names no text"
		case ch.Target.Self && byLaw == 0:
			lk.How = "names no text"
		case ch.Target.Self:
			lk.LawID, lk.How = byLaw, "this text"
		case ch.Target.Number == "" && ch.Target.Date == "" && generic[amend.FoldString(ch.Target.Name)]:
			lk.How = "names no text"
		default:
			// The Centre's notes ("amended by ...") name texts made after
			// the one they sit in, so no date to lie before.
			cutoff := before
			if strings.HasPrefix(ch.Action, "note_") {
				cutoff = time.Time{}
			}
			lk.LawID, lk.How = ix.Resolve(ch.Target.Ref(), cutoff)
			if lk.LawID == 0 && lk.How == "" {
				lk.How = "not found"
			}
		}
		out = append(out, lk)
	}
	return out
}

// Phrases that name no particular text.
var generic = map[string]bool{
	"قانون خاص": true, "قانون": true, "مرسوم": true, "قرار": true, "نص خاص": true,
	"قانون قطع الحساب": true, "قانون الموازنه": true, "قانون موازنه": true,
}

// ResolveAll matches every article's citations, then settles numbers that
// fit several texts when another article of the same text cites that number
// with a date that settles it ("القانون رقم 75" after "القانون رقم 75 تاريخ
// 1/2/2001"). Keyed like the citations.
func ResolveAll(ix *amend.Index, cs map[string]Citation) map[string][]Link {
	return resolveAll(ix, cs, func(k string) (string, int, time.Time) {
		var id int
		fmt.Sscanf(k, "%d/", &id)
		var before time.Time
		if l := ix.Law(id); l != nil {
			before = l.Issued
		}
		return strconv.Itoa(id), id, before
	})
}

// ResolveRulings matches the references of rulings (keys "ruling/<id>"), the
// same way, with targets earlier than the day the ruling was decided
// (dates, by ruling id; none known: any). A ruling is not a text, so it has
// no "this law", and a number settled by one ruling does not settle another's.
func ResolveRulings(ix *amend.Index, cs map[string]Citation, dates map[int]time.Time) map[string][]Link {
	return resolveAll(ix, cs, func(k string) (string, int, time.Time) {
		return k, 0, dates[RulingID(k)]
	})
}

// resolveAll resolves every citation; scope says, for a key, the group that
// shares settled numbers, the amending text (0: none) and the date targets
// lie before.
func resolveAll(ix *amend.Index, cs map[string]Citation, scope func(k string) (string, int, time.Time)) map[string][]Link {
	out := map[string][]Link{}
	known := map[string]map[int]bool{} // group | kind | number -> texts it settled on
	numKey := func(group string, t Target) string {
		r := t.Ref()
		return fmt.Sprintf("%s|%s|%d", group, r.Kind, r.Num)
	}
	for k, c := range cs {
		group, by, before := scope(k)
		lks := resolve(ix, by, before, c)
		out[k] = lks
		for _, lk := range lks {
			if lk.LawID != 0 && lk.Target.Number != "" && (lk.Target.Date != "" || lk.Target.Year > 0) {
				nk := numKey(group, lk.Target)
				if known[nk] == nil {
					known[nk] = map[int]bool{}
				}
				known[nk][lk.LawID] = true
			}
		}
	}
	for k, lks := range out {
		group, _, _ := scope(k)
		for i, lk := range lks {
			if lk.LawID != 0 || lk.How != "several texts with that number" {
				continue
			}
			if ids := known[numKey(group, lk.Target)]; len(ids) == 1 {
				for id := range ids {
					lks[i].LawID, lks[i].How = id, "number, dated elsewhere in the same text"
				}
			}
		}
	}
	return out
}

// Report compares the citations of the given keys with what the pattern
// pass found (the amendment table) and writes a summary and the differences.
func Report(ctx context.Context, db *pgxpool.Pool, cs map[string]Citation, w io.Writer) error {
	ix, err := LawIndex(ctx, db)
	if err != nil {
		return err
	}
	arts := map[int]map[string]bool{}
	rows, err := db.Query(ctx, `SELECT law_id, number FROM legislation_article`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		if arts[id] == nil {
			arts[id] = map[string]bool{}
		}
		arts[id][n] = true
	}
	rows.Close()
	type pair struct {
		law int
		art string
	}
	parser := map[string]map[pair]bool{}
	rows, err = db.Query(ctx, `SELECT by_law_id || '/' || by_article, coalesce(law_id, 0), coalesce(article_number, '') FROM amendment`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, a string
		var l int
		if err := rows.Scan(&k, &l, &a); err != nil {
			return err
		}
		if parser[k] == nil {
			parser[k] = map[pair]bool{}
		}
		parser[k][pair{l, a}] = true
	}
	rows.Close()

	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	n := map[string]int{}
	var diffs []string
	resolved := ResolveAll(ix, cs)
	for _, k := range keys {
		c := cs[k]
		var byLaw int
		fmt.Sscanf(k, "%d/", &byLaw)
		n["articles"]++
		if parser[k] != nil {
			n["articles the parser read"]++
		}
		if !c.Amends {
			n["articles Gemini says change nothing"]++
		}
		mine := map[pair]bool{}
		for _, lk := range resolved[k] {
			n["changes"]++
			switch {
			case !lk.Valid:
				n["changes: quote not in the article (dropped)"]++
			case lk.LawID == 0:
				n["changes: target not matched ("+lk.How+")"]++
			default:
				lk.HasArt = lk.Target.Article == "" || arts[lk.LawID][lk.Target.Article]
				if lk.HasArt {
					n["changes: linked"]++
				} else {
					n["changes: text found, article not in it"]++
				}
				mine[pair{lk.LawID, lk.Target.Article}] = true
			}
		}
		theirs := parser[k]
		var onlyMine, onlyTheirs []string
		for p := range mine {
			if !theirs[p] {
				onlyMine = append(onlyMine, fmt.Sprintf("%d art %s", p.law, p.art))
			}
		}
		for p := range theirs {
			if p.law != 0 && !mine[p] {
				onlyTheirs = append(onlyTheirs, fmt.Sprintf("%d art %s", p.law, p.art))
			}
		}
		switch {
		case len(theirs) == 0 && len(mine) > 0:
			n["articles: links only Gemini found"]++
		case len(onlyMine) == 0 && len(onlyTheirs) == 0 && len(mine) > 0:
			n["articles: same links as the parser"]++
		case len(onlyTheirs) > 0 || len(onlyMine) > 0:
			n["articles: links differ from the parser"]++
		}
		if len(onlyTheirs) > 0 || (len(onlyMine) > 0 && len(theirs) > 0) {
			sort.Strings(onlyMine)
			sort.Strings(onlyTheirs)
			diffs = append(diffs, fmt.Sprintf("%s  gemini-only: %s  parser-only: %s", k,
				strings.Join(onlyMine, ", "), strings.Join(onlyTheirs, ", ")))
		}
	}
	names := make([]string, 0, len(n))
	for k := range n {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(w, "%-55s %6d\n", k, n[k])
	}
	fmt.Fprintf(w, "\ndifferences with the parser (%d):\n", len(diffs))
	for _, d := range diffs {
		fmt.Fprintln(w, d)
	}
	return nil
}

// TouchesCode says whether any of an article's citations names one of the
// principal codes.
func TouchesCode(ix *amend.Index, byLaw int, c Citation) bool {
	for _, lk := range Resolve(ix, byLaw, c) {
		if l := ix.Law(lk.LawID); l != nil && l.Code && Changing(lk.Action) {
			return true
		}
	}
	return false
}

// Hard says whether a cheaper model left an article unsettled: a quote that
// isn't in the article, or a change whose target matched nothing.
func Hard(ix *amend.Index, byLaw int, c Citation) bool {
	for _, lk := range Resolve(ix, byLaw, c) {
		if !lk.Valid || (lk.LawID == 0 && Changing(lk.Action) && lk.Action != "general_repeal") {
			return true
		}
	}
	return false
}
