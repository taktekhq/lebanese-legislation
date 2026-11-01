// Command topicgen writes and checks the topic pages of LebaneseLegislation.
//
//	topicgen prompt  <slug>                       print the prompt a model gets
//	topicgen gen     -family claude <slug>...     write topics/<slug>.json from a spec
//	topicgen check   [slug...]                    check topics against the database
//	topicgen bakeoff                              blind comparison of the model families
//	topicgen judge   [slug...]                    re-ground each page and flag statements
//	topicgen fix     <slug> <issues file>         correct a page from a reviewer's issues (one per line)
//
// Every command needs the database (-db or LEBLEG_DSN). A spec (specs/*.json)
// names the question and the articles a writer may use; the writer is a model
// that sees only those articles. The tool then repairs quotes to the article's
// exact words where they differ only in spelling marks, strips em dashes, and
// runs the same checks as the Go test.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

var (
	dsn        = flag.String("db", os.Getenv("LEBLEG_DSN"), "database connection string")
	specDir    = flag.String("specs", "cmd/topicgen/specs", "directory of specs")
	outDir     = flag.String("out", "internal/site/topics", "directory of topic files")
	workDir    = flag.String("work", "cmd/topicgen/work", "directory for raw model answers and reports")
	famName    = flag.String("family", "claude", "model family: claude, openai or gemini")
	today      = flag.String("date", time.Now().Format("2006-01-02"), "published and last-checked date")
	writerName = flag.String("writer", "openai", "polish: the family that corrects flagged statements")
	rounds     = flag.Int("rounds", 2, "polish: judge and correct at most this many times")
	parallel   = flag.Int("j", 4, "model calls at once")
)

// the order topics are listed in
var order = []string{"end-of-service-indemnity", "nssf-registration-contributions", "working-hours-annual-leave", "rent-law",
	"inheritance", "personal-status-civil-marriage", "nationality", "traffic-fines", "banking-secrecy", "capital-control",
	"cybercrime-electronic-transactions", "company-formation", "income-tax-brackets", "constitution", "code-of-commerce",
	"official-gazette", "founding-an-association", "weapons-law", "penal-code-articles", "code-of-obligations",
	"building-violations-encroachment", "pre-emption-right", "general-amnesty-law", "penal-code-forgery-article-459",
	"traffic-authority-decree-11244", "foreign-ministry-regulations", "notaries-law", "pharmacy-practice-law", "foreign-workers-work-permits"}

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: topicgen [flags] prompt|gen|check|bakeoff|judge [slug...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	cmd, args := flag.Arg(0), flag.Args()[1:]
	ctx := context.Background()
	if *dsn == "" {
		log.Fatal("no database: pass -db or set LEBLEG_DSN")
	}
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	db := topics.DB{Pool: pool}
	if len(args) == 0 {
		args = specSlugs(*specDir)
	}
	switch cmd {
	case "prompt":
		for _, slug := range args {
			s, arts := mustSpec(ctx, db, slug)
			fmt.Print(buildPrompt(s, arts))
		}
	case "gen":
		fam := families[*famName]
		if fam == nil {
			log.Fatalf("unknown family %q", *famName)
		}
		forEach(args, func(slug string) {
			t, st, err := generate(ctx, db, fam, slug)
			if err != nil {
				log.Printf("%s: %v", slug, err)
				return
			}
			if err := writeTopic(t); err != nil {
				log.Printf("%s: %v", slug, err)
				return
			}
			log.Printf("%s: written; quotes %d, exact %d, repaired %d, unrepaired %d", slug, st.Quotes, st.Exact, st.Repaired, st.Lost)
			for _, p := range topics.Check(t, db) {
				log.Print("  ", p)
			}
		})
	case "check":
		bad := 0
		for _, slug := range args {
			t := topics.Get(slug)
			if strings.HasSuffix(slug, ".json") { // a file outside the embedded set
				t = &topics.Topic{}
				if b, err := os.ReadFile(slug); err != nil || json.Unmarshal(b, t) != nil {
					log.Printf("%s: cannot read", slug)
					bad++
					continue
				}
			}
			if t == nil {
				log.Printf("%s: no such topic file", slug)
				bad++
				continue
			}
			for _, p := range topics.Check(t, db) {
				fmt.Println(p)
				bad++
			}
		}
		if bad > 0 {
			os.Exit(1)
		}
	case "bakeoff":
		bakeoff(ctx, db, args)
	case "rescore":
		rescore(db, args)
	case "polish":
		polish(ctx, db, args)
	case "judge":
		judgeTopics(ctx, db, args)
	case "fix": // fix <slug> <issues file>: one issue per line, "key: what to change"
		fixTopic(ctx, db, families[*famName], args)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func mustSpec(ctx context.Context, db topics.DB, slug string) (*Spec, []Article) {
	s, err := loadSpec(*specDir, slug)
	if err != nil {
		log.Fatal(err)
	}
	arts, err := loadPack(ctx, db, s)
	if err != nil {
		log.Fatal(err)
	}
	return s, arts
}

// Stats counts what happened to a model's quotes.
type Stats struct{ Quotes, Exact, Repaired, Lost int }

// rawPage is the shape the model replies in.
type rawPage struct {
	Articles []topics.Cite  `json:"articles"`
	EN       topics.Content `json:"en"`
	AR       topics.Content `json:"ar"`
}

// parseAnswer reads the JSON object out of a model's reply.
func parseAnswer(out string) (*rawPage, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		return nil, fmt.Errorf("no JSON object in the answer: %.200q", out)
	}
	var p rawPage
	dec := json.NewDecoder(strings.NewReader(out[i : j+1]))
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("bad JSON: %w", err)
	}
	return &p, nil
}

// assemble turns a model's page into a topic: fixed fields from the spec,
// quotes repaired to the article's own words, dashes stripped.
func assemble(s *Spec, p *rawPage, texts map[string]string, repair bool) (*topics.Topic, Stats) {
	t := &topics.Topic{Slug: s.Slug, Published: *today, Updated: *today, Articles: p.Articles, EN: p.EN, AR: p.AR}
	for i, o := range order {
		if o == s.Slug {
			t.Order = i + 1
		}
	}
	t.EN.Title, t.EN.Question = s.TitleEN, s.QuestionEN
	t.AR.Title, t.AR.Question = s.TitleAR, s.QuestionAR
	stripDashes(t)
	pruneUnused(t)
	var st Stats
	for i := range t.Articles {
		c := &t.Articles[i]
		st.Quotes++
		text := texts[fmt.Sprintf("%d/%s", c.Law, c.Article)]
		switch {
		case strings.Contains(text, c.Quote):
			st.Exact++
		default:
			if r, ok := repairQuote(text, c.Quote); ok && repair {
				c.Quote = r
				st.Repaired++
			} else {
				st.Lost++
			}
		}
	}
	return t, st
}

func packTexts(arts []Article) map[string]string {
	m := map[string]string{}
	for _, a := range arts {
		m[fmt.Sprintf("%d/%s", a.Law, a.Number)] = a.Text
	}
	return m
}

// generate asks the model for a page, then feeds the checks' complaints back
// to it for up to three rounds, keeping the page with the fewest problems.
func generate(ctx context.Context, db topics.DB, fam *family, slug string) (*topics.Topic, Stats, error) {
	s, arts := mustSpec(ctx, db, slug)
	return runLoop(ctx, db, fam, s, arts, buildPrompt(s, arts), nil)
}

// refine asks the model to correct a page where a reviewer flagged
// statements, then runs the same loop on the checks.
func refine(ctx context.Context, db topics.DB, fam *family, t *topics.Topic, issues []string) (*topics.Topic, Stats, error) {
	s, arts := mustSpec(ctx, db, t.Slug)
	base := buildPrompt(s, arts) + "\n\nA REVIEWER, READING THE PAGE AGAINST THE ARTICLES, FLAGGED THESE STATEMENTS (keys: answer, sNpM = section N paragraph M, sNrM = section N table row M)\n- " +
		strings.Join(issues, "\n- ") + "\n\nCorrect each flagged statement so that every claim in it, in both languages, is stated by the articles it cites: restate it as the article says, add the missing condition, cite the article that holds the claim, or drop the claim (drop the whole paragraph or row if nothing supportable is left, in both languages). Keep everything not flagged as it is, unless a fix needs a change there too."
	return runLoop(ctx, db, fam, s, arts, base, t)
}

func runLoop(ctx context.Context, db topics.DB, fam *family, s *Spec, arts []Article, base string, prev0 *topics.Topic) (*topics.Topic, Stats, error) {
	slug := s.Slug
	texts := packTexts(arts)
	os.MkdirAll(*workDir, 0o755)
	var best *topics.Topic
	var bestStats Stats
	bestN := -1
	prompt := base
	if prev0 != nil {
		prev, _ := json.MarshalIndent(rawPage{Articles: prev0.Articles, EN: prev0.EN, AR: prev0.AR}, "", " ")
		prompt = base + "\n\nTHE PAGE AS IT IS NOW\n" + string(prev) + "\n\nReply with the complete corrected JSON object, same shape, nothing else."
	}
	for round := 0; round < 4; round++ {
		out, err := callFamily(ctx, fam, prompt)
		if err != nil {
			return best, bestStats, err
		}
		os.WriteFile(filepath.Join(*workDir, fmt.Sprintf("%s.%s.raw%d.txt", slug, fam.name, round)), []byte(out), 0o644)
		p, err := parseAnswer(out)
		if err != nil {
			prompt = base + "\n\nYour previous answer could not be read (" + err.Error() + "). Reply again with the complete JSON object only."
			continue
		}
		t, st := assemble(s, p, texts, true)
		if prev0 != nil {
			t.Published, t.Order = prev0.Published, prev0.Order
		}
		problems := topics.Check(t, db)
		log.Printf("%s round %d: %d problems", slug, round, len(problems))
		if bestN < 0 || len(problems) < bestN {
			best, bestStats, bestN = t, st, len(problems)
		}
		if len(problems) == 0 {
			break
		}
		prev, _ := json.MarshalIndent(rawPage{Articles: t.Articles, EN: t.EN, AR: t.AR}, "", " ")
		prompt = base + "\n\nYOUR PREVIOUS ANSWER\n" + string(prev) + "\n\nPROBLEMS FOUND BY THE AUTOMATIC CHECKS\n- " + strings.Join(problems, "\n- ") +
			"\n\nFix every problem and reply with the complete corrected JSON object, same shape, nothing else. Fix a number by removing it, writing the article's own wording, or citing an article that states it (a figure derived or converted by you, such as a percentage from a fraction, must go). Fix a quote by copying the article's exact words. Keep what was already right."
	}
	if best == nil {
		return nil, Stats{}, fmt.Errorf("no readable answer")
	}
	return best, bestStats, nil
}

func writeTopic(t *topics.Topic) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*outDir, t.Slug+".json"), append(b, '\n'), 0o644)
}

// pruneUnused drops quotes no paragraph cites: they would never be shown.
func pruneUnused(t *topics.Topic) {
	used := map[string]bool{}
	mark := func(c *topics.Content) {
		for _, id := range c.Answer.Cites {
			used[id] = true
		}
		for _, sec := range c.Sections {
			for _, p := range sec.Paras {
				for _, id := range p.Cites {
					used[id] = true
				}
			}
			if sec.Table != nil {
				for _, r := range sec.Table.Rows {
					for _, id := range r.Cites {
						used[id] = true
					}
				}
			}
		}
	}
	mark(&t.EN)
	mark(&t.AR)
	var keep []topics.Cite
	for _, c := range t.Articles {
		if used[c.ID] {
			keep = append(keep, c)
		}
	}
	t.Articles = keep
}

// fixTopic corrects a page from a reviewer's issues: the writer gets each
// issue as a flagged statement and runs the same checks as gen.
func fixTopic(ctx context.Context, db topics.DB, fam *family, args []string) {
	if len(args) != 2 || fam == nil {
		log.Fatal("usage: topicgen -family openai fix <slug> <issues file>")
	}
	t := topics.Get(args[0])
	if t == nil {
		log.Fatalf("no such topic %q", args[0])
	}
	b, err := os.ReadFile(args[1])
	if err != nil {
		log.Fatal(err)
	}
	var issues []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			issues = append(issues, l)
		}
	}
	nt, st, err := refine(ctx, db, fam, t, issues)
	if err != nil || nt == nil {
		log.Fatalf("%s: %v", args[0], err)
	}
	nt.Updated = *today
	if err := writeTopic(nt); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: rewritten; quotes %d, exact %d, repaired %d, unrepaired %d", args[0], st.Quotes, st.Exact, st.Repaired, st.Lost)
	for _, p := range topics.Check(nt, db) {
		log.Print("  ", p)
	}
}
