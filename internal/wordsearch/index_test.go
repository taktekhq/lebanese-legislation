package wordsearch

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestUnionIntersect(t *testing.T) {
	a, b := []int32{1, 3, 5, 9}, []int32{2, 3, 9, 10}
	if got := union(a, b); !reflect.DeepEqual(got, []int32{1, 2, 3, 5, 9, 10}) {
		t.Errorf("union %v", got)
	}
	if got := union(nil, b); !reflect.DeepEqual(got, b) {
		t.Errorf("union with nothing %v", got)
	}
	if got := intersect(slices.Clone(a), b); !reflect.DeepEqual(got, []int32{3, 9}) {
		t.Errorf("intersect %v", got)
	}
	if got := intersect(slices.Clone(a), nil); len(got) != 0 {
		t.Errorf("intersect with nothing %v", got)
	}
}

func TestMemListsLookups(t *testing.T) {
	m := &MemLists{Corpora: map[string]*MemCorpus{
		"law_title_base": memCorpus(map[string][]int32{
			"لبنان": {0, 7}, "لبنانيه": {3, 7, 300000}, "لبناني": {2}, "لب": {9}, "عقد": {1, 2, 3},
		}),
	}}
	got, err := m.Lists(context.Background(), []ListReq{
		{Corpus: "law_title_base", Lexeme: "لبنان"},
		{Corpus: "law_title_base", Lexeme: "لبنان", Prefix: true},
		{Corpus: "law_title_base", Lexeme: "عقد"},
		{Corpus: "law_title_base", Lexeme: "عقود"},
		{Corpus: "law_title_base", Lexeme: "ل", Prefix: true},
		{Corpus: "article_exact", Lexeme: "عقد"},
	})
	want := [][]int32{{0, 7}, {0, 2, 3, 7, 300000}, {1, 2, 3}, nil, {0, 2, 3, 7, 9, 300000}, nil}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("lists %v %v, want %v", got, err, want)
	}
}

// top keeps the first n candidates of the ranking's order.
func TestTopIsTheSortedHead(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, idDesc := range []bool{false, true} {
		q := &idxPage{idDesc: idDesc}
		for round := 0; round < 200; round++ {
			cs := make([]cand, r.Intn(300))
			for i := range cs {
				cs[i] = cand{doc: int32(i), units: int32(r.Intn(3)), code: r.Intn(5) == 0, titled: r.Intn(4) == 0,
					score: float64(r.Intn(20)) / 7, tie: int32(r.Intn(3)), id: int32(r.Intn(50)), pos: int32(i)}
			}
			n := 1 + r.Intn(60)
			got := q.top(cs, n)
			all := slices.Clone(cs)
			sort.SliceStable(all, func(i, j int) bool { return q.before(&all[i], &all[j]) })
			if want := all[:min(n, len(all))]; !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
				t.Fatalf("top %d of %d: %v, want %v", n, len(cs), got, want)
			}
		}
	}
}

// An index file reads back as written, in place; a file of another format,
// a damaged one or none is not read.
func TestIndexFile(t *testing.T) {
	fams := map[string]*Docs{
		"article":    {ID: []int32{5, 5, 9}, Pos: []int32{1, 2, 1}, Len: []int32{10, 300, 7}, Tie: []int32{0, 0, 0}},
		"article_fr": {Len: []int32{0, 12, 0}},
		"ruling":     {ID: []int32{3}, Len: []int32{900}, Tie: []int32{738000}},
		"law":        {ID: []int32{5, 9}, Len: []int32{40, 61}, Tie: []int32{700000, 0}, Year: []int32{1932, 0}, Number: []string{"340", ""}},
		"law_en":     {ID: []int32{9}, Len: []int32{20}, Tie: []int32{0}, Year: []int32{0}, Number: []string{""}},
	}
	lists := &MemLists{Corpora: map[string]*MemCorpus{}}
	for _, c := range []string{"article_exact", "article_stem", "article_fr", "ruling_exact", "ruling_stem", "law_title", "law_title_base", "law_title_en"} {
		lists.Corpora[c] = memCorpus(map[string][]int32{"عقد": {0, 2}, "contrat": {1}, c: {0}})
	}
	var buf bytes.Buffer
	if err := writeIndexFile(&buf, "fp", fams, lists); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "idx")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := mapFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gotFams, gotLists, h, err := readIndexFile(data)
	if err != nil || h.Fingerprint != "fp" {
		t.Fatalf("read back: %v %v", h, err)
	}
	fams["article_fr"].ID, fams["article_fr"].Pos, fams["article_fr"].Tie = fams["article"].ID, fams["article"].Pos, fams["article"].Tie
	for name, want := range fams {
		got := gotFams[name]
		if got == nil || !reflect.DeepEqual(*got, *want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	for name, want := range lists.Corpora {
		if got := gotLists.Corpora[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	other := bytes.Replace(buf.Bytes(), []byte(IndexFormat), []byte("lebleg-search-index 0"), 1)
	if _, _, _, err := readIndexFile(other); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("another format: %v", err)
	}
	for _, bad := range [][]byte{nil, []byte("LLSIDX"), buf.Bytes()[:len(buf.Bytes())/2], []byte("not an index file at all")} {
		if _, _, _, err := readIndexFile(bad); err == nil {
			t.Errorf("read %d damaged bytes", len(bad))
		}
	}
	if _, err := LoadIndexFile(context.Background(), nil, filepath.Join(t.TempDir(), "none"), nil); err == nil {
		t.Error("no file: no error")
	}
}

// The index's scores are the float8s Postgres computes from the SQL
// candidates() writes, to the bit: a weight sum printed with %g (or, per
// row, a sum of such literals), times the BM25 length factor of normSQL.
func TestScorerMatchesPostgres(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a Postgres database")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	r := rand.New(rand.NewSource(3))
	for round := 0; round < 40; round++ {
		avg := []float64{0, 1, 7, 266, 323, 1234.5, r.Float64() * 5000}[round%7]
		sc := newScorer(avg)
		var exprs []string
		var want []float64
		for i := 0; i < 50; i++ {
			length := int32(r.Intn(200000))
			if i%5 == 0 {
				length = int32(r.Intn(40))
			}
			n := 1 + r.Intn(4)
			ws := make([]float64, n)
			for j := range ws {
				ws[j] = r.Float64() * 14
				if r.Intn(3) == 0 {
					ws[j] *= 0.8
				}
			}
			norm := normSQL(fmt.Sprint(length), avg)
			if i%2 == 0 { // one weight: the sum as Go adds it, printed
				sum := 0.0
				for _, w := range ws {
					sum += w
				}
				exprs = append(exprs, fmt.Sprintf("(%g * %s)::float8", sum, norm))
				want = append(want, sc.score(sc.scaled(sum), length))
			} else { // per row: numeric literals summed
				var terms []string
				for _, w := range ws {
					terms = append(terms, fmt.Sprintf("GREATEST(CASE WHEN true THEN %g ELSE 0 END, CASE WHEN false THEN 99 ELSE 0 END)", w))
				}
				exprs = append(exprs, fmt.Sprintf("((%s) * %s)::float8", strings.Join(terms, " + "), norm))
				want = append(want, sc.score(sc.scaled(ws...), length))
			}
		}
		got := make([]float64, len(exprs))
		dest := make([]any, len(exprs))
		for i := range got {
			dest[i] = &got[i]
		}
		if err := db.QueryRow(ctx, "SELECT "+strings.Join(exprs, ", ")).Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: Postgres %v, index %v", exprs[i], got[i], want[i])
			}
		}
	}
}

// cutLength finds the length a sort would put at the n-th place.
func TestCutLength(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for round := 0; round < 300; round++ {
		lens := make([]int32, 1+r.Intn(3000))
		top := []int32{3, 50, 2000, 1 << 23}[round%4]
		for i := range lens {
			lens[i] = int32(r.Int63n(int64(top)))
		}
		var ds []int32
		for d := range lens {
			if r.Intn(3) > 0 {
				ds = append(ds, int32(d))
			}
		}
		if len(ds) == 0 {
			continue
		}
		n := 1 + r.Intn(len(ds))
		sorted := make([]int32, len(ds))
		for i, d := range ds {
			sorted[i] = lens[d]
		}
		slices.Sort(sorted)
		if got := cutLength(lens, ds, n); got != sorted[n-1] {
			t.Fatalf("%d texts, n %d: %d, want %d", len(ds), n, got, sorted[n-1])
		}
	}
}
