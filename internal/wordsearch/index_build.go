package wordsearch

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strings"
)

// MemLists hold the lists: per corpus the lexemes sorted (byte order, as
// tsquery compares a prefix), each list as varint gaps, about 1.9 bytes a
// text per lexeme. Read from an index file they point into its mapping.
type MemLists struct {
	Corpora map[string]*MemCorpus
}

// MemCorpus is one corpus's lexemes and lists.
type MemCorpus struct {
	Lex     []byte   // the lexemes, sorted, end to end
	LexEnd  []uint32 // where each ends in Lex
	Post    []byte   // each lexeme's texts, ascending, as varint gaps
	PostEnd []uint32 // where each list ends in Post
}

func (c *MemCorpus) lexeme(i int) string {
	start := uint32(0)
	if i > 0 {
		start = c.LexEnd[i-1]
	}
	return string(c.Lex[start:c.LexEnd[i]])
}

// list is lexeme i's texts.
func (c *MemCorpus) list(i int) []int32 {
	start := uint32(0)
	if i > 0 {
		start = c.PostEnd[i-1]
	}
	b := c.Post[start:c.PostEnd[i]]
	out := make([]int32, 0, len(b))
	prev := int64(-1)
	for len(b) > 0 {
		g, n := binary.Uvarint(b)
		b = b[n:]
		prev += int64(g)
		out = append(out, int32(prev))
	}
	return out
}

// find is the first lexeme not before l.
func (c *MemCorpus) find(l string) int {
	return sort.Search(len(c.LexEnd), func(j int) bool { return c.lexeme(j) >= l })
}

func (m *MemLists) Lists(_ context.Context, reqs []ListReq) ([][]int32, error) {
	out := make([][]int32, len(reqs))
	for i, r := range reqs {
		c := m.Corpora[r.Corpus]
		if c == nil {
			continue
		}
		j := c.find(r.Lexeme)
		if !r.Prefix {
			if j < len(c.LexEnd) && c.lexeme(j) == r.Lexeme {
				out[i] = c.list(j)
			}
			continue
		}
		for ; j < len(c.LexEnd) && strings.HasPrefix(c.lexeme(j), r.Lexeme); j++ {
			out[i] = union(out[i], c.list(j))
		}
	}
	return out, nil
}

// Size is the bytes the lists take.
func (m *MemLists) Size() int {
	n := 0
	for _, c := range m.Corpora {
		n += len(c.Lex) + len(c.Post) + 4*len(c.LexEnd) + 4*len(c.PostEnd)
	}
	return n
}

// memCorpus builds a MemCorpus from lexemes and their texts (ascending).
func memCorpus(lists map[string][]int32) *MemCorpus {
	lex := make([]string, 0, len(lists))
	for l := range lists {
		lex = append(lex, l)
	}
	sort.Strings(lex)
	c := &MemCorpus{}
	var buf [binary.MaxVarintLen64]byte
	for _, l := range lex {
		c.Lex = append(c.Lex, l...)
		c.LexEnd = append(c.LexEnd, uint32(len(c.Lex)))
		prev := int64(-1)
		for _, d := range lists[l] {
			n := binary.PutUvarint(buf[:], uint64(int64(d)-prev))
			c.Post = append(c.Post, buf[:n]...)
			prev = int64(d)
		}
		c.PostEnd = append(c.PostEnd, uint32(len(c.Post)))
	}
	return c
}

// Fingerprint names a database's searchable texts: each corpus's text count
// and average length (search_term), and each table's rows and highest id.
// An index file is read only on a database with its fingerprint.
func Fingerprint(ctx context.Context, db Querier) (string, error) {
	rows, err := db.Query(ctx, `SELECT concat_ws(' ',
		(SELECT string_agg(corpus || ':' || term || '=' || docs, ',' ORDER BY corpus, term) FROM search_term WHERE term IN ('', '#len')),
		(SELECT count(*) || '/' || max(law_id) FROM legislation_article),
		(SELECT count(*) || '/' || max(id) FROM ruling),
		(SELECT count(*) || '/' || max(id) FROM legislation),
		(SELECT count(*) || '/' || max(law_id) FROM legislation_title_en))`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var f string
	for rows.Next() {
		if err := rows.Scan(&f); err != nil {
			return "", err
		}
	}
	return f, rows.Err()
}

// The families' texts and lists, as BuildIndex reads them: each query
// returns the key columns, then the lexemes of each corpus listed, in the
// order of the texts' numbers. The lengths are candidates()'s.
var buildSQL = []struct {
	fam     string
	corpora []string
	sql     string
}{
	{"article", []string{"article_exact", "article_stem", "article_fr"}, `SELECT a.law_id, a.position, a.text_len, length(coalesce(a.text_fr, '')),
		0, 0, '', tsvector_to_array(a.search_exact), tsvector_to_array(a.search_stem), tsvector_to_array(a.search_fr)
		FROM legislation_article a ORDER BY a.law_id, a.position`},
	{"ruling", []string{"ruling_exact", "ruling_stem"}, `SELECT r.id, 0, r.text_len, 0, coalesce(r.decided_on - date '0001-01-01', 0), 0, '',
		tsvector_to_array(r.search_exact), tsvector_to_array(r.search_stem)
		FROM ruling r ORDER BY r.id`},
	{"law", []string{"law_title", "law_title_base"}, `SELECT l.id, 0, length(l.title), 0, coalesce(l.issued_on - date '0001-01-01', 0),
		coalesce(extract(year FROM l.issued_on)::int, 0), coalesce(l.number, ''), tsvector_to_array(l.title_exact), tsvector_to_array(l.title_base)
		FROM legislation l ORDER BY l.id`},
	{"law_en", []string{"law_title_en"}, `SELECT te.law_id, 0, length(te.title_en), 0, coalesce(l.issued_on - date '0001-01-01', 0),
		coalesce(extract(year FROM l.issued_on)::int, 0), coalesce(l.number, ''), tsvector_to_array(te.search_en)
		FROM legislation_title_en te JOIN legislation l ON l.id = te.law_id ORDER BY te.law_id`},
}

// BuildIndex reads every searchable text's lexemes from db and writes the
// index file to w. A Postgres error (a missing column, a NULL length) fails
// the build: no file is better than a wrong one.
func BuildIndex(ctx context.Context, db Querier, w io.Writer) error {
	fp, err := Fingerprint(ctx, db)
	if err != nil {
		return err
	}
	fams := map[string]*Docs{}
	lists := &MemLists{Corpora: map[string]*MemCorpus{}}
	for _, b := range buildSQL {
		rows, err := db.Query(ctx, b.sql)
		if err != nil {
			return fmt.Errorf("%s: %w", b.fam, err)
		}
		d := &Docs{}
		fr := &Docs{}
		byLex := make([]map[string][]int32, len(b.corpora))
		for i := range byLex {
			byLex[i] = map[string][]int32{}
		}
		for rows.Next() {
			var id, pos, ln, lnFr, tie, year int32
			var number string
			lex := make([][]string, len(b.corpora))
			dest := []any{&id, &pos, &ln, &lnFr, &tie, &year, &number}
			for i := range lex {
				dest = append(dest, &lex[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return fmt.Errorf("%s: %w", b.fam, err)
			}
			n := int32(len(d.ID))
			d.ID, d.Len, d.Tie = append(d.ID, id), append(d.Len, ln), append(d.Tie, tie)
			switch b.fam {
			case "article":
				d.Pos = append(d.Pos, pos)
				fr.Len = append(fr.Len, lnFr)
			case "law", "law_en":
				d.Year, d.Number = append(d.Year, year), append(d.Number, number)
			}
			for i, ls := range lex {
				for _, l := range ls {
					byLex[i][l] = append(byLex[i][l], n)
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("%s: %w", b.fam, err)
		}
		fams[b.fam] = d
		if b.fam == "article" {
			fams["article_fr"] = fr // its ID, Pos and Tie are the articles'
		}
		for i, c := range b.corpora {
			lists.Corpora[c] = memCorpus(byLex[i])
		}
	}
	return writeIndexFile(w, fp, fams, lists)
}
