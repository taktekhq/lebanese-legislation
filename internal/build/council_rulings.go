package build

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
)

// councilRow is one line of db/council_rulings.tsv.
type councilRow struct {
	LawID     int
	Number    string // the article; "" for the decision on the text as a whole
	Effect    string
	Decision  string
	On        time.Time
	SourceURL string
	En, Ar    string
}

var councilEffects = map[string]bool{"annulled": true, "annulled_in_part": true, "not_in_force": true, "suspended": true, "upheld": true}

// parseCouncilRulings reads db/council_rulings.tsv. Every decision on a text
// needs its row for the text as a whole (article empty), so the text's page
// can name it; an article row without one is a problem, as is any bad field.
func parseCouncilRulings(text string) ([]councilRow, []string) {
	var out []councilRow
	var problems []string
	whole := map[string]bool{} // law id|decision
	for i, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "law_id\t") {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) != 8 {
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: %d columns, want 8", i+1, len(f)))
			continue
		}
		id, err := strconv.Atoi(f[0])
		on, err2 := time.Parse("2006-01-02", f[4])
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: law id %q", i+1, f[0]))
		case !councilEffects[f[2]]:
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: effect %q", i+1, f[2]))
		case f[2] == "not_in_force" && f[1] != "":
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: not_in_force is for the text as a whole", i+1))
		case f[3] == "":
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: no decision number", i+1))
		case err2 != nil:
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: date %q", i+1, f[4]))
		case !strings.HasPrefix(f[5], "https://") && !strings.HasPrefix(f[5], "http://"):
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: no source address", i+1))
		case f[6] == "" || f[7] == "":
			problems = append(problems, fmt.Sprintf("council_rulings.tsv line %d: a note is empty", i+1))
		default:
			out = append(out, councilRow{LawID: id, Number: f[1], Effect: f[2], Decision: f[3], On: on, SourceURL: f[5], En: f[6], Ar: f[7]})
			if f[1] == "" {
				whole[f[0]+"|"+f[3]] = true
			}
		}
	}
	kept := out[:0]
	for _, r := range out {
		if r.Number != "" && !whole[strconv.Itoa(r.LawID)+"|"+r.Decision] {
			problems = append(problems, fmt.Sprintf("council ruling %s on %d article %s: no row for the text as a whole", r.Decision, r.LawID, r.Number))
			continue
		}
		kept = append(kept, r)
	}
	return kept, problems
}

// councilRulings loads db/council_rulings.tsv: the Constitutional Council's
// decisions on a text, which the site shows next to the text and the
// articles they annulled. A row whose text or article does not exist is
// reported and left out.
func (b *builder) councilRulings() error {
	rows, problems := parseCouncilRulings(db.CouncilRulings)
	for _, p := range problems {
		b.rep.note("%s", p)
	}
	cols := []string{"law_id", "number", "effect", "decision", "decided_on", "source_url", "note_en", "note_ar"}
	r := newRows()
	for _, c := range rows {
		var ok bool
		q := `SELECT EXISTS (SELECT 1 FROM legislation_article WHERE law_id = $1 AND number = $2)`
		args := []any{c.LawID, c.Number}
		if c.Number == "" {
			q, args = `SELECT EXISTS (SELECT 1 FROM legislation WHERE id = $1)`, []any{c.LawID}
		}
		if err := b.conn.QueryRow(b.ctx, q, args...).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			b.rep.note("council ruling %s for %d article %q: no such text or article", c.Decision, c.LawID, c.Number)
			continue
		}
		r.add("council_ruling", cols, c.LawID, c.Number, c.Effect, c.Decision, c.On, c.SourceURL, c.En, c.Ar)
	}
	return b.flush(r, "council_ruling")
}
