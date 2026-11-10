package staticexport

// The planner reads COPY-text rows, never rendered HTML or database images.
// Both sides of a zero-context git diff are retained: old memberships and
// addresses matter just as much as new ones when a row moves or disappears.
import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

type ChangePlan struct {
	Full  bool
	Paths []string
	// Content are changed templates, strings, topic guides and assets: what
	// they touch needs the render record (PlanContent), so the command maps
	// them. Paths holds only what the data changes touch.
	Content []string
	// Why says why a render is full.
	Why string
}
type changeRow map[string]string
type tableRow struct {
	table string
	row   changeRow
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	return c.Output()
}

// PlanChanges compares the last successfully rendered commit with HEAD.
// Unknown schemas/tables fail closed to a full render. The workflow checks out
// one pinned commit for the plan and every shard, under a single run lock.
func PlanChanges(ctx context.Context, dir, since string, full bool) (ChangePlan, error) {
	var frame bool
	p, e := planChanges(ctx, dir, since, full, &frame)
	if e != nil || p.Full || !frame {
		return p, e
	}
	// A page stored whole carries the frame in its own bytes: render it again.
	seen := map[string]bool{}
	for _, q := range p.Paths {
		seen[q] = true
	}
	for _, q := range WholeFramePages {
		if !seen[q] {
			p.Paths = append(p.Paths, q)
		}
	}
	sort.Strings(p.Paths)
	return p, nil
}

// WholeFramePages are the pages in the layout that are stored whole, because
// the export changes them in the frame (the search page's script, added
// before </body> by SearchShell). A frame-only change renders them again.
var WholeFramePages = []string{"/search/", "/en/search/", "/fr/search/"}

// planChanges is PlanChanges; frame is set when layout.html changed in its
// shared frame only.
func planChanges(ctx context.Context, dir, since string, full bool, frame *bool) (ChangePlan, error) {
	if full {
		return ChangePlan{Full: true, Why: "a full render was asked for"}, nil
	}
	if since == "" {
		return ChangePlan{Full: true, Why: "no earlier render to start from"}, nil
	}
	b, e := gitOutput(ctx, dir, "rev-parse", "--verify", since+"^{commit}")
	if e != nil {
		return ChangePlan{Full: true, Why: "the last rendered commit " + since + " is not in this repository"}, nil
	}
	since = strings.TrimSpace(string(b))
	b, e = gitOutput(ctx, dir, "diff", "--name-status", "--no-renames", since, "HEAD")
	if e != nil {
		return ChangePlan{}, e
	}
	src, e := renderSources(ctx, dir)
	if e != nil {
		return ChangePlan{}, e
	}
	var content []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		status, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if p == layoutFile && frameOnlyChange(ctx, dir, since) {
			*frame = true // every run publishes the frame; no page's parts change
			continue
		}
		if src.changes(p, status == "D") && contentFile(p) {
			content = append(content, p)
			continue
		}
		if src.changes(p, status == "D") {
			return ChangePlan{Full: true, Why: p + " is code the site is built from: a Go change renders everything"}, nil
		}
		if renderInputs[p] {
			return ChangePlan{Full: true, Why: p + " is read by every render"}, nil
		}
		if strings.HasPrefix(p, "data/tables/") && (!strings.HasSuffix(p, ".tsv")) {
			return ChangePlan{Full: true, Why: p + " changes a table's layout"}, nil
		}
	}
	cols := map[string][]string{}
	columns := func(table string) ([]string, error) {
		if c, ok := cols[table]; ok {
			return c, nil
		}
		b, e := os.ReadFile(filepath.Join(dir, "data/tables", table, "COLUMNS"))
		if e != nil {
			return nil, e
		}
		c := strings.Split(strings.TrimSpace(string(b)), ",")
		cols[table] = c
		return c, nil
	}
	var changed []tableRow
	c := exec.CommandContext(ctx, "git", "diff", "--no-ext-diff", "--no-renames", "--unified=0", since, "HEAD", "--", "data/tables")
	c.Dir = dir
	pipe, e := c.StdoutPipe()
	if e != nil {
		return ChangePlan{}, e
	}
	if e = c.Start(); e != nil {
		return ChangePlan{}, e
	}
	table := ""
	e = scanCopyLines(pipe, func(line string) error {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Split(line, "/")
			if len(parts) >= 4 {
				table = parts[len(parts)-2]
			}
			return nil
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || len(line) == 0 || line[0] != '+' && line[0] != '-' {
			return nil
		}
		cs, e := columns(table)
		if e != nil {
			return e
		}
		r, e := copyRow(cs, line[1:])
		if e != nil {
			return e
		}
		changed = append(changed, tableRow{table, r})
		return nil
	})
	if e != nil {
		_ = c.Process.Kill()
	}
	we := c.Wait()
	if e != nil {
		return ChangePlan{}, e
	}
	if we != nil {
		return ChangePlan{}, we
	}
	if len(changed) == 0 {
		return ChangePlan{Paths: []string{}, Content: content}, nil
	}
	tables := map[string][]changeRow{}
	// Only fields used for ownership, membership or links are retained. Large
	// article/ruling bodies and source metadata are streamed and discarded.

	dirs, e := os.ReadDir(filepath.Join(dir, "data/tables"))
	if e != nil {
		return ChangePlan{}, e
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		t := d.Name()
		switch t {
		case "legislation", "legislation_article", "code", "ruling", "gazette_issue", "opinion", "agreement", "doctrine_article", "research", "graph_edge", "reference", "amendment", "gazette_entry", "legislation_alias":
		default:
			continue
		}
		cs, e := columns(t)
		if e != nil {
			return ChangePlan{}, e
		}
		files, e := filepath.Glob(filepath.Join(dir, "data/tables", t, "*.tsv"))
		if e != nil {
			return ChangePlan{}, e
		}
		for _, file := range files {
			f, e := os.Open(file)
			if e != nil {
				return ChangePlan{}, e
			}
			e = scanCopyLines(f, func(s string) error {
				r, e := copyRow(cs, s)
				if e != nil {
					return e
				}
				tables[t] = append(tables[t], r)
				return nil
			})
			f.Close()
			if e != nil {
				return ChangePlan{}, fmt.Errorf("%s: %w", file, e)
			}
		}
	}
	for _, r := range changed {
		tables[r.table] = append(tables[r.table], r.row)
	}
	p := pathsForRows(tables, changed)
	p.Content = content
	return p, nil
}

// layoutFile is the frame every page shares (internal/site/layoutframe.go).
const layoutFile = "internal/site/templates/layout.html"

// frameOnlyChange says whether layout.html changed in the shared frame only:
// both versions are pure frames (no defines, no calls but the "page:" parts)
// with the same holes, escaped the same way. Then every page's parts are
// what they were, and the run only has to publish the new frame, which each
// export with the edge layout does. Anything else, including a version that
// is not a frame (every one before the edge layout), renders everything.
// LayoutSchemas reads the frame schemas of a version of layout.html
// (internal/site.LayoutSchemas). The command sets it; this package does not
// import the site, which reads the database. Unset, every layout change
// renders everything.
var LayoutSchemas func(src string) (map[string]string, error)

func frameOnlyChange(ctx context.Context, dir, since string) bool {
	schemas := func(rev string) map[string]string {
		b, e := gitOutput(ctx, dir, "show", rev+":"+layoutFile)
		if e != nil {
			return nil
		}
		if LayoutSchemas == nil {
			return nil
		}
		s, e := LayoutSchemas(string(b))
		if e != nil {
			return nil
		}
		return s
	}
	a, b := schemas(since), schemas("HEAD")
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for lang, s := range a {
		if b[lang] != s {
			return false
		}
	}
	return true
}

func scanCopyLines(r io.Reader, fn func(string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		if e := fn(sc.Text()); e != nil {
			return e
		}
	}
	return sc.Err()
}

// COPY text escapes tabs/newlines/backslashes and uses \N for SQL NULL.
var planningFields = func() map[string]bool {
	fields := map[string]bool{}
	for _, s := range strings.Fields("id kind issued_on year published_on law_id by_law_id number position slug from_type from_id to_law_id issue_id ratifying_law_id ruling_id doc_type centre_id name_ar court") {
		fields[s] = true
	}
	return fields
}()

func copyRow(cols []string, line string) (changeRow, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != len(cols) {
		return nil, fmt.Errorf("COPY row: got %d fields, want %d", len(fields), len(cols))
	}
	r := changeRow{}
	for i, s := range fields {
		if !planningFields[cols[i]] {
			continue
		}
		if s == `\N` {
			continue
		}
		var b strings.Builder
		for j := 0; j < len(s); j++ {
			if s[j] != '\\' {
				b.WriteByte(s[j])
				continue
			}
			j++
			if j == len(s) {
				return nil, fmt.Errorf("unfinished COPY escape")
			}
			switch s[j] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			default:
				b.WriteByte(s[j])
			}
		}
		r[cols[i]] = b.String()
	}
	return r, nil
}

func entity(kind, id string) string {
	if id == "" {
		return ""
	}
	switch kind {
	case "law", "legislation", "english", "article":
		kind = "law"
	case "doctrine_article":
		kind = "doctrine"
	case "gazette_issue":
		kind = "gazette"
	}
	return kind + "/" + id
}
func rowEntities(t string, r changeRow) []string {
	var out []string
	add := func(k, id string) {
		if v := entity(k, id); v != "" {
			out = append(out, v)
		}
	}
	switch t {
	case "legislation":
		add("law", r["id"])
	case "legislation_article", "legislation_section", "legislation_scan", "legislation_title_en", "centre_error", "council_ruling", "date_correction", "article_version", "code":
		add("law", r["law_id"])
	case "amendment":
		add("law", r["law_id"])
		add("law", r["by_law_id"])
	case "legislation_alias":
		add("law", r["id"])
		add("law", r["law_id"])
	case "reference", "graph_edge":
		add(r["from_type"], r["from_id"])
		add("law", r["law_id"])
		add("law", r["to_law_id"])
	case "gazette_entry":
		add("gazette", r["issue_id"])
		add("law", r["law_id"])
	case "agreement":
		add("agreement", r["id"])
		add("law", r["ratifying_law_id"])
	case "ruling_page", "ruling_file":
		add("ruling", r["ruling_id"])
	case "page_state", "noindex_page":
		add(r["kind"], r["id"])
	case "source_page":
		add(r["doc_type"], r["centre_id"])
	case "ruling", "gazette_issue", "opinion", "research", "doctrine_article":
		add(t, r["id"])
	}
	return out
}
func textKind(k string) string {
	if strings.HasPrefix(k, "مرسوم") {
		return "decree"
	}
	if strings.HasPrefix(k, "قانون") || k == "الدستور" || k == "دستور" {
		return "law"
	}
	return "text"
}
func rowYear(r changeRow) string {
	if r["year"] != "" {
		return r["year"]
	}
	for _, k := range []string{"issued_on", "published_on"} {
		if len(r[k]) >= 4 {
			return r[k][:4]
		}
	}
	return "0"
}

func pathsForRows(tables map[string][]changeRow, changed []tableRow) ChangePlan {
	direct := map[string]bool{}
	affected := map[string]bool{}
	for _, c := range changed {
		switch c.table {
		case "court", "legislation_kind":
			table, field := "ruling", "court"
			if c.table == "legislation_kind" {
				table, field = "legislation", "kind"
			}
			for _, r := range tables[table] {
				if r[field] == c.row["name_ar"] {
					for _, k := range rowEntities(table, r) {
						direct[k] = true
					}
				}
			}
		default:
			es := rowEntities(c.table, c.row)
			if len(es) == 0 {
				return ChangePlan{Full: true}
			}
			for _, k := range es {
				direct[k] = true
			}
		}
	}
	for k := range direct {
		affected[k] = true
	}
	// One hop only: neighbours display the changed item's label/content, but
	// their own labels did not change, so a transitive graph flood is unnecessary.
	for _, t := range []string{"graph_edge", "reference", "amendment", "gazette_entry", "agreement", "legislation_alias"} {
		for _, r := range tables[t] {
			es := rowEntities(t, r)
			hit := false
			for _, k := range es {
				hit = hit || direct[k]
			}
			if hit {
				for _, k := range es {
					affected[k] = true
				}
			}
		}
	}
	// Related texts and previous/next links are bounded by year and address
	// kind. Refresh that cohort's law pages, without all of their articles.
	cohorts := map[string]bool{}
	for _, r := range tables["legislation"] {
		if direct[entity("law", r["id"])] {
			cohorts[rowYear(r)+"/"+textKind(r["kind"])] = true
		}
	}
	related := map[string]bool{}
	for _, r := range tables["legislation"] {
		if cohorts[rowYear(r)+"/"+textKind(r["kind"])] {
			related[entity("law", r["id"])] = true
		}
	}
	paths := map[string]bool{}
	add := func(p string) {
		paths[p] = true
		paths["/en"+p] = true
		if i18n.FrenchPages[p] {
			paths["/fr"+p] = true
		}
	}
	for _, p := range []string{"/", "/changes/", "/changes/review/", "/changes/feed.xml", "/laws/", "/laws/years/", "/rulings/", "/rulings/years/", "/gazette/", "/gazette/years/", "/codes/", "/topics/", "/data/", "/llms.txt"} {
		add(p)
	}
	// All sitemap files are small, and insertions can shift every later shard.
	paths["/sitemap.xml"] = true
	for _, name := range []string{"core", "ar-core", "pages", "ar-pages"} {
		paths["/sitemaps/"+name+".xml"] = true
	}

	for _, set := range []struct{ name, table string }{{"laws", "legislation"}, {"articles", "legislation_article"}, {"gazette", "gazette_issue"}, {"documents", "opinion"}} {
		n := len(tables[set.table])
		if set.name == "documents" {
			n += len(tables["agreement"]) + len(tables["doctrine_article"]) + len(tables["research"])
		}
		for i := 1; i <= (n+44999)/45000+1; i++ {
			paths[fmt.Sprintf("/sitemaps/ar-%s-%d.xml", set.name, i)] = true
			if set.name == "laws" {
				paths[fmt.Sprintf("/sitemaps/laws-%d.xml", i)] = true
			}
		}
	}
	knownLaws := map[string]bool{}
	for _, r := range tables["legislation"] {
		knownLaws[r["id"]] = true
	}
	for key := range affected {
		kind, id, _ := strings.Cut(key, "/")
		if kind == "law" {
			if !knownLaws[id] {
				for _, word := range []string{"law", "decree", "text"} {
					add("/" + word + "/" + id + "/")
				}
			}
		}
	}
	lawRoots := map[string]map[string]bool{}
	articles := map[string]map[string]bool{}
	positions := map[string]map[string]bool{}
	for _, r := range tables["legislation_article"] {
		id := r["law_id"]
		if articles[id] == nil {
			articles[id] = map[string]bool{}
		}
		articles[id][r["number"]] = true
		if positions[id] == nil {
			positions[id] = map[string]bool{}
		}
		positions[id][r["position"]] = true
	}
	dirtyLists := map[string]bool{}
	count := func(base, _ string, dirty bool) { // the lists a changed row is in
		if dirty {
			dirtyLists[base] = true
		}
	}
	for _, r := range tables["legislation"] {
		id := r["id"]
		root := "/" + textKind(r["kind"]) + "/" + id + "/"
		key := entity("law", id)
		if lawRoots[id] == nil {
			lawRoots[id] = map[string]bool{}
		}
		lawRoots[id][root] = true
		if affected[key] || related[key] {
			add(root)
			add(root + "feed.xml")
		}
		dirty := affected[key] || related[key]
		count("/laws/year/"+rowYear(r)+"/", id, dirty)
		count("/laws/year/"+rowYear(r)+"/"+textKind(r["kind"])+"/", id, dirty)
	}
	for id, roots := range lawRoots {
		if affected[entity("law", id)] {
			for root := range roots {
				for n := range articles[id] {
					p := root + "article/" + url.PathEscape(strings.Join(strings.Fields(n), "-")) + "/"
					add(p)
					add(p + "rulings/")
				}
			}
		}
	}
	for _, t := range []string{"ruling", "gazette_issue", "opinion", "agreement", "doctrine_article", "research"} {
		for _, r := range tables[t] {
			k := entity(t, r["id"])
			kind, _, _ := strings.Cut(k, "/")
			dirty := affected[k]
			if dirty {
				add("/" + k + "/")
			}
			list := map[string]string{"ruling": "rulings", "gazette": "gazette", "opinion": "opinions", "agreement": "agreements", "doctrine": "doctrine", "research": "research"}[kind]
			if kind == "ruling" || kind == "gazette" {
				count("/"+list+"/year/"+rowYear(r)+"/", r["id"], dirty)
			} else {
				count("/"+list+"/", r["id"], dirty)
			}
		}
	}
	// A list's later pages are cursor pages the origin answers (internal/site,
	// keyset.go): only its first page is stored. Its old numbered pages still
	// in the manifest are selected by ExpandChangedPaths, render as a move to
	// the first page, and so leave the bucket.
	for base := range dirtyLists {
		add(base)
	}
	for _, r := range tables["code"] {
		id := r["law_id"]
		if affected[entity("law", id)] {
			add("/code/" + r["slug"] + "/")
			for i := 1; i <= max(1, (len(positions[id])+99)/100); i++ {
				p := "/codes/" + r["slug"] + "/"
				if i > 1 {
					p += fmt.Sprintf("page/%d/", i)
				}
				add(p)
			}
		}
	}
	for _, t := range topics.All() {
		for _, a := range t.Articles {
			if affected[entity("law", strconv.Itoa(a.Law))] {
				add("/topic/" + t.Slug + "/")
				break
			}
		}
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return ChangePlan{Paths: out}
}
