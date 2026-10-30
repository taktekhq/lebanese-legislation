package site

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Change struct {
	ID        int
	Action    string
	Article   *string
	Part      *string
	TextAr    *string
	Effective *time.Time
	Status    string
	By        LawRow
	ByArticle string
}

func (c *Change) Verb() string {
	switch {
	case c.Part != nil && *c.Part == "+":
		return "Supplemented"
	case c.Part != nil:
		return "Changed in part"
	case c.Action == "repeal":
		return "Repealed"
	case c.Action == "insert":
		return "Added"
	}
	return "Replaced"
}

func (c *Change) VerbKey() string {
	switch {
	case c.Part != nil && *c.Part == "+":
		return "supplemented"
	case c.Part != nil:
		return "part"
	case c.Action == "repeal":
		return "repeal"
	case c.Action == "insert":
		return "insert"
	}
	return "default"
}

func (c *Change) PartLabel() string {
	if c.Part == nil || *c.Part == "+" {
		return ""
	}
	return *c.Part
}

type Version struct {
	Major, Minor int
	From         *time.Time
	To           *time.Time
	State        string
	TextAr       *string
	Source       string
	Note         *string
	Change       *Change
}

func (v Version) Label() string { return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) }

func (a *Article) Latest() *Version {
	if n := len(a.Versions); n > 0 {
		return &a.Versions[n-1]
	}
	return nil
}

func (a *Article) current() int {
	for i := len(a.Versions) - 1; i >= 0; i-- {
		if a.Versions[i].TextAr != nil {
			return i
		}
	}
	return -1
}

func (a *Article) Current() *Version {
	if i := a.current(); i >= 0 {
		return &a.Versions[i]
	}
	return nil
}

func (a *Article) Shown() string {
	if c := a.Current(); c != nil && !a.RepealedNote() {
		return *c.TextAr
	}
	return a.TextAr
}

func (a *Article) Ahead() bool {
	i := a.current()
	if i < 0 || a.Versions[i].Source == "centre" {
		return false
	}
	for _, v := range a.Versions[:i] {
		if v.Source == "centre" {
			return true
		}
	}
	return false
}

func (a *Article) Pending() []Version {
	i := a.current()
	major := 0
	if i >= 0 {
		major = a.Versions[i].Major
	}
	var out []Version
	for _, v := range a.Versions {
		if v.Major > major && v.State == "in_force" && v.Change != nil && v.Minor == 0 {
			out = append(out, v)
		}
	}
	return out
}

func (a *Article) Repealed() bool {
	l := a.Latest()
	return l != nil && l.State == "repealed" || a.RepealedNote()
}

var repealedNote = regexp.MustCompile(`^\s*مادة ملغا`)

func (a *Article) RepealedNote() bool {
	return a.Note != nil && repealedNote.MatchString(*a.Note)
}

type ChangeGroup struct {
	By       LawRow
	Date     *time.Time
	Articles []string
}

func (s *Server) changes(ctx context.Context, cond, other string, id int) ([]Change, error) {
	rows, err := s.db.Query(ctx, `SELECT x.id, x.action, x.article_number, x.part, x.text_ar, x.effective_on, x.status, x.by_article, `+lawRowCols+`
		FROM amendment x JOIN legislation l ON l.id = `+other+` JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE `+cond+` AND x.status IN ('applied', 'partial', 'review') AND x.law_id IS NOT NULL
		ORDER BY x.effective_on NULLS FIRST, x.by_law_id, x.by_article, x.seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		dest := append([]any{&c.ID, &c.Action, &c.Article, &c.Part, &c.TextAr, &c.Effective, &c.Status, &c.ByArticle}, lawRowDest(&c.By)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Server) attachVersions(ctx context.Context, l *Law) error {
	all, err := s.changes(ctx, "x.law_id = $1", "x.by_law_id", l.ID)
	if err != nil {
		return err
	}
	l.sortChanges(all)
	byID := map[int]*Change{}
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	rows, err := s.db.Query(ctx, `SELECT article_number, major, minor, valid_from, valid_to, state, text_ar, text_source,
		amendment_id, note FROM article_version WHERE law_id = $1 ORDER BY article_number, major, minor`, l.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byNumber := map[string]*Article{}
	for _, a := range l.List {
		byNumber[a.Number] = a
	}
	for rows.Next() {
		var num string
		var v Version
		var change *int
		if err := rows.Scan(&num, &v.Major, &v.Minor, &v.From, &v.To, &v.State, &v.TextAr, &v.Source, &change, &v.Note); err != nil {
			return err
		}
		if change != nil {
			v.Change = byID[*change]
		}
		a := byNumber[num]
		if a == nil {
			a = &Article{Number: num, Added: true}
			byNumber[num] = a
			l.insertArticle(a)
		}
		a.Versions = append(a.Versions, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range all {
		if c.Status == "review" && c.Article != nil {
			if a := byNumber[*c.Article]; a != nil {
				a.Unsure = append(a.Unsure, c)
			}
		}
	}
	if err := s.attachLinks(ctx, l, byNumber); err != nil {
		return err
	}
	return s.attachRulings(ctx, l, byNumber)
}

func (l *Law) sortChanges(all []Change) {
	for _, c := range all {
		switch {
		case c.Article == nil && c.Action == "repeal" && c.Part == nil && c.Status == "applied":
			l.RepealedByChange = append(l.RepealedByChange, c)
		case c.Status != "review":
			l.Changes = append(l.Changes, c)
		}
	}
	if len(l.RepealedByChange) > 0 {
		l.Status = "repealed"
	}
}

func (l *Law) insertArticle(a *Article) {
	n := leadInt(a.Number)
	at := len(l.List)
	for i, b := range l.List {
		if leadInt(b.Number) > n {
			at = i
			break
		}
	}
	l.List = append(l.List[:at], append([]*Article{a}, l.List[at:]...)...)
	if at == len(l.List)-1 {
		l.Items = append(l.Items, Item{Article: a})
		return
	}
	next := l.List[at+1]
	for i, it := range l.Items {
		if it.Article == next {
			l.Items = append(l.Items[:i], append([]Item{{Article: a}}, l.Items[i:]...)...)
			return
		}
	}
	l.Items = append(l.Items, Item{Article: a})
}

func leadInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

func group(cs []Change) []ChangeGroup {
	var out []ChangeGroup
	at := map[int]int{}
	for _, c := range cs {
		i, ok := at[c.By.ID]
		if !ok {
			i = len(out)
			at[c.By.ID] = i
			out = append(out, ChangeGroup{By: c.By, Date: c.Effective})
		}
		art := ""
		if c.Article != nil {
			art = *c.Article
		}
		g := &out[i]
		dup := false
		for _, x := range g.Articles {
			dup = dup || x == art
		}
		if !dup {
			g.Articles = append(g.Articles, art)
		}
	}
	for i := range out {
		sort.SliceStable(out[i].Articles, func(a, b int) bool {
			return leadInt(out[i].Articles[a]) < leadInt(out[i].Articles[b])
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Date, out[j].Date
		return a != nil && (b == nil || a.After(*b))
	})
	return out
}

func (g ChangeGroup) Arts() string {
	var nums []string
	whole := false
	for _, a := range g.Articles {
		if a == "" {
			whole = true
		} else {
			nums = append(nums, a)
		}
	}
	s := ""
	switch len(nums) {
	case 0:
	case 1:
		s = "Art. " + nums[0]
	default:
		if len(nums) > 12 {
			nums = append(nums[:12], "…")
		}
		s = "Arts. " + strings.Join(nums, ", ")
	}
	if whole {
		if s != "" {
			s += " and "
		}
		s += "the text"
	}
	return s
}

type ChangeEntry struct {
	By      LawRow
	Date    *time.Time
	Targets []ChangeTarget
}

type ChangeTarget struct {
	Law      LawRow
	Articles []string
}

func (t ChangeTarget) Arts() string { return ChangeGroup{Articles: t.Articles}.Arts() }

func (s *Server) latestChanges(ctx context.Context) ([]ChangeEntry, error) {

	rows, err := s.db.Query(ctx, `SELECT e.from_id, coalesce(b.gazette_date, b.issued_on) AS on, coalesce(e.to_article, ''), `+lawRowCols+`
		FROM graph_edge e JOIN legislation b ON b.id = e.from_id
		JOIN legislation l ON l.id = e.to_law_id JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE e.from_type = 'legislation' AND e.status IN ('linked', 'no_article') AND e.to_law_id <> e.from_id
		  AND e.relation IN ('replace', 'amend', 'repeal', 'insert', 'suspend', 'extend')
		  AND coalesce(b.gazette_date, b.issued_on) IS NOT NULL
		ORDER BY 2 DESC, e.from_id DESC, e.to_law_id, e.to_article LIMIT 1500`)
	if err != nil {
		return nil, err
	}
	var entries []ChangeEntry
	at := map[int]int{}
	var byIDs []int
	for rows.Next() {
		var by int
		var date *time.Time
		var art string
		var law LawRow
		if err := rows.Scan(append([]any{&by, &date, &art}, lawRowDest(&law)...)...); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := at[by]
		if !ok {
			if len(entries) == 120 {
				continue
			}
			i = len(entries)
			at[by] = i
			entries = append(entries, ChangeEntry{By: LawRow{ID: by}, Date: date})
			byIDs = append(byIDs, by)
		}
		e := &entries[i]
		j := -1
		for k := range e.Targets {
			if e.Targets[k].Law.ID == law.ID {
				j = k
			}
		}
		if j < 0 {
			e.Targets = append(e.Targets, ChangeTarget{Law: law})
			j = len(e.Targets) - 1
		}
		e.Targets[j].Articles = appendNew(e.Targets[j].Articles, art)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range entries {
		for j := range entries[i].Targets {
			arts := entries[i].Targets[j].Articles
			sort.SliceStable(arts, func(a, b int) bool {
				if x, y := leadInt(arts[a]), leadInt(arts[b]); x != y {
					return x < y
				}
				return arts[a] < arts[b]
			})
		}
	}
	rows, err = s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = ANY($1)`, byIDs)
	if err == nil {
		var bys []LawRow
		if bys, err = scanLawRows(rows); err == nil {
			for _, b := range bys {
				entries[at[b.ID]].By = b
			}
		}
	}
	return entries, err
}

func (s *Server) recentChanges(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	entries, err := s.latestChanges(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "changes", 200, Page{Title: s.brandTitle(r, s.trOr(r, "Changes to Lebanese law", "changes.title")), Path: "/changes/",
		Desc: s.trOr(r, "The latest amendments to Lebanese laws and decrees, newest first: which text changed which articles, and from when. Free.", "changes.desc"),
		Data: entries})
}

type ReviewRow struct {
	Law       LawRow
	Article   string
	Version   string
	By        LawRow
	ByArticle string
	Date      *time.Time
	Reason    string
	Judgement string
	Key       string
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	load := func(sql string) ([]ReviewRow, error) {
		rows, err := s.db.Query(ctx, sql)
		if err != nil {
			return nil, err
		}
		var out []ReviewRow
		var byIDs []int
		for rows.Next() {
			var x ReviewRow
			var byID int
			if err := rows.Scan(append([]any{&x.Article, &x.Version, &byID, &x.ByArticle, &x.Date, &x.Reason, &x.Judgement},
				lawRowDest(&x.Law)...)...); err != nil {
				rows.Close()
				return nil, err
			}
			x.By.ID = byID
			x.Key = fmt.Sprintf("%d\t%s\t%d\t%s", byID, x.ByArticle, x.Law.ID, x.Article)
			out = append(out, x)
			byIDs = append(byIDs, byID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows, err = s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = ANY($1)`, byIDs)
		if err != nil {
			return nil, err
		}
		bys, err := scanLawRows(rows)
		if err != nil {
			return nil, err
		}
		m := map[int]LawRow{}
		for _, b := range bys {
			m[b.ID] = b
		}
		for i := range out {
			out[i].By = m[out[i].By.ID]
		}
		return out, nil
	}
	open, err := load(`SELECT x.article_number, '', x.by_law_id, x.by_article, x.effective_on,
		coalesce(x.reason, ''), coalesce(x.judgement, ''), ` + lawRowCols + `
		FROM amendment x JOIN legislation l ON l.id = x.law_id JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE x.status = 'review' AND x.article_number IS NOT NULL AND x.law_id IN (SELECT law_id FROM code)
		ORDER BY l.id, x.effective_on DESC NULLS LAST`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "review", 200, Page{Title: s.brandTitle(r, s.trOr(r, "For review", "review.title")), Path: "/changes/review/", NoIndex: true,
		Desc: s.trOr(r, "Amendments waiting for a check.", "review.desc"), Data: map[string]any{"Open": open}})
}
