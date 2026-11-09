package wordsearch

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"unsafe"
)

// The index file, as BuildIndex writes it and LoadIndexFile maps it:
//
//	"LLSIDX\x00\x01"  magic, 8 bytes
//	uint32            n, the header's length (little-endian)
//	header            n bytes of JSON: format, fingerprint, sections
//	sections          each at an offset that is a multiple of 8, counted
//	                  from the first multiple of 8 after the header
//
// A section is a byte string, or an array of little-endian int32 or uint32:
// a family's ID, Pos, Len, Tie and Year ("doc/article/len"), its Numbers
// end to end and where each ends, a corpus's lexemes, lists and their ends
// ("list/article_exact/post"). The server maps the file read-only and reads
// the lists (76 MB) in place: they are not decoded at start nor on Go's
// heap; the kernel reads their pages as searches touch them and may drop
// them again, as it does Postgres's own files.

// IndexFormat marks an index file; a file of another format is not read.
const IndexFormat = "lebleg-search-index 1"

var indexMagic = []byte("LLSIDX\x00\x01")

type fileHeader struct {
	Format      string
	Fingerprint string // the database it was built from (Fingerprint)
	Sections    []fileSection
}

type fileSection struct {
	Name     string
	Off, Len int64
}

// writeIndexFile writes the families' texts and the lists.
func writeIndexFile(w io.Writer, fingerprint string, fams map[string]*Docs, lists *MemLists) error {
	type part struct {
		name string
		data []byte
	}
	var parts []part
	add := func(name string, data []byte) { parts = append(parts, part{name, data}) }
	i32 := func(xs []int32) []byte {
		b := make([]byte, 4*len(xs))
		for i, x := range xs {
			binary.LittleEndian.PutUint32(b[4*i:], uint32(x))
		}
		return b
	}
	u32 := func(xs []uint32) []byte {
		b := make([]byte, 4*len(xs))
		for i, x := range xs {
			binary.LittleEndian.PutUint32(b[4*i:], x)
		}
		return b
	}
	for _, name := range sortedKeys(fams) {
		d := fams[name]
		for _, col := range []struct {
			name string
			xs   []int32
		}{{"id", d.ID}, {"pos", d.Pos}, {"len", d.Len}, {"tie", d.Tie}, {"year", d.Year}} {
			if col.xs != nil {
				add("doc/"+name+"/"+col.name, i32(col.xs))
			}
		}
		if d.Number != nil {
			var blob []byte
			ends := make([]uint32, len(d.Number))
			for i, n := range d.Number {
				blob = append(blob, n...)
				ends[i] = uint32(len(blob))
			}
			add("doc/"+name+"/number", blob)
			add("doc/"+name+"/number_end", u32(ends))
		}
	}
	for _, name := range sortedKeys(lists.Corpora) {
		c := lists.Corpora[name]
		add("list/"+name+"/lex", c.Lex)
		add("list/"+name+"/lexend", u32(c.LexEnd))
		add("list/"+name+"/post", c.Post)
		add("list/"+name+"/postend", u32(c.PostEnd))
	}
	h := fileHeader{Format: IndexFormat, Fingerprint: fingerprint}
	off := int64(0)
	for _, p := range parts {
		h.Sections = append(h.Sections, fileSection{p.name, off, int64(len(p.data))})
		off = align8(off + int64(len(p.data)))
	}
	hj, err := json.Marshal(h)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 1<<20)
	bw.Write(indexMagic)
	binary.Write(bw, binary.LittleEndian, uint32(len(hj)))
	bw.Write(hj)
	at := int64(len(indexMagic) + 4 + len(hj))
	pad := func(to int64) {
		for ; at < to; at++ {
			bw.WriteByte(0)
		}
	}
	start := align8(at)
	pad(start)
	for i, p := range parts {
		pad(start + h.Sections[i].Off)
		bw.Write(p.data)
		at += int64(len(p.data))
	}
	return bw.Flush()
}

func align8(n int64) int64 { return (n + 7) &^ 7 }

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// LoadIndexFile maps an index file BuildIndex wrote, if it was built from a
// database with db's fingerprint; codes are Known.Codes. A file of another
// format or database is an error: the caller searches without it. The
// mapping stays for the life of the process.
func LoadIndexFile(ctx context.Context, db Querier, path string, codes []int32) (*Index, error) {
	data, err := mapFile(path)
	if err != nil {
		return nil, err
	}
	fams, lists, h, err := readIndexFile(data)
	if err != nil {
		unmapFile(data)
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	fp, err := Fingerprint(ctx, db)
	if err != nil {
		unmapFile(data)
		return nil, err
	}
	if fp != h.Fingerprint {
		unmapFile(data)
		return nil, fmt.Errorf("%s: built from another database", path)
	}
	return NewIndex(fams, lists, codes), nil
}

var errBadFile = errors.New("not an index file of this format")

// readIndexFile reads the texts and lists out of a file's bytes, in place.
func readIndexFile(data []byte) (map[string]*Docs, *MemLists, *fileHeader, error) {
	if len(data) < len(indexMagic)+4 || string(data[:len(indexMagic)]) != string(indexMagic) {
		return nil, nil, nil, errBadFile
	}
	n := int64(binary.LittleEndian.Uint32(data[len(indexMagic):]))
	hEnd := int64(len(indexMagic)+4) + n
	if hEnd > int64(len(data)) {
		return nil, nil, nil, errBadFile
	}
	var h fileHeader
	if err := json.Unmarshal(data[len(indexMagic)+4:hEnd], &h); err != nil {
		return nil, nil, nil, errBadFile
	}
	if h.Format != IndexFormat {
		return nil, nil, nil, fmt.Errorf("format %q, want %q", h.Format, IndexFormat)
	}
	start := align8(hEnd)
	sec := map[string][]byte{}
	for _, s := range h.Sections {
		if s.Off < 0 || s.Len < 0 || s.Off%8 != 0 || start+s.Off+s.Len > int64(len(data)) {
			return nil, nil, nil, errBadFile
		}
		sec[s.Name] = data[start+s.Off : start+s.Off+s.Len : start+s.Off+s.Len]
	}
	fams := map[string]*Docs{}
	for _, name := range []string{"article", "article_fr", "ruling", "law", "law_en"} {
		d := &Docs{ID: int32s(sec["doc/"+name+"/id"]), Pos: int32s(sec["doc/"+name+"/pos"]), Len: int32s(sec["doc/"+name+"/len"]),
			Tie: int32s(sec["doc/"+name+"/tie"]), Year: int32s(sec["doc/"+name+"/year"])}
		if blob, ends := sec["doc/"+name+"/number"], uint32s(sec["doc/"+name+"/number_end"]); ends != nil {
			d.Number = make([]string, len(ends))
			prev := uint32(0)
			for i, e := range ends {
				if e < prev || int(e) > len(blob) {
					return nil, nil, nil, errBadFile
				}
				d.Number[i], prev = string(blob[prev:e]), e
			}
		}
		if d.Len == nil {
			continue
		}
		fams[name] = d
	}
	// The texts' columns (12 MB) are copied out: a search reads them at
	// random, and in place that cost 0.3 to 0.6 ms more a search (a 4 KB
	// page each, against the heap's). The lists stay in the mapping.
	for _, d := range fams {
		d.ID, d.Pos, d.Len, d.Tie, d.Year = slices.Clone(d.ID), slices.Clone(d.Pos), slices.Clone(d.Len), slices.Clone(d.Tie), slices.Clone(d.Year)
	}
	a, fr := fams["article"], fams["article_fr"]
	if a == nil || fr == nil || len(fr.Len) != len(a.ID) {
		return nil, nil, nil, errBadFile
	}
	fr.ID, fr.Pos, fr.Tie = a.ID, a.Pos, a.Tie
	for _, d := range fams {
		if len(d.ID) != len(d.Len) || len(d.Tie) != len(d.Len) || (d.Pos != nil && len(d.Pos) != len(d.Len)) ||
			(d.Year != nil && len(d.Year) != len(d.Len)) || (d.Number != nil && len(d.Number) != len(d.Len)) {
			return nil, nil, nil, errBadFile
		}
	}
	lists := &MemLists{Corpora: map[string]*MemCorpus{}}
	for _, name := range []string{"article_exact", "article_stem", "article_fr", "ruling_exact", "ruling_stem", "law_title", "law_title_base", "law_title_en"} {
		c := &MemCorpus{Lex: sec["list/"+name+"/lex"], LexEnd: uint32s(sec["list/"+name+"/lexend"]),
			Post: sec["list/"+name+"/post"], PostEnd: uint32s(sec["list/"+name+"/postend"])}
		if len(c.LexEnd) != len(c.PostEnd) || (len(c.LexEnd) > 0 && (int(c.LexEnd[len(c.LexEnd)-1]) != len(c.Lex) || int(c.PostEnd[len(c.PostEnd)-1]) != len(c.Post))) {
			return nil, nil, nil, errBadFile
		}
		lists.Corpora[name] = c
	}
	return fams, lists, &h, nil
}

// littleEndian: the arrays can be read in place (amd64, arm64).
var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

// int32s is b's little-endian int32s, in place when the machine's order is
// the file's; nil for none.
func int32s(b []byte) []int32 {
	if len(b) == 0 {
		return nil
	}
	if littleEndian && uintptr(unsafe.Pointer(unsafe.SliceData(b)))%4 == 0 {
		return unsafe.Slice((*int32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
	}
	out := make([]int32, len(b)/4)
	for i := range out {
		out[i] = int32(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func uint32s(b []byte) []uint32 {
	if len(b) == 0 {
		return nil
	}
	if littleEndian && uintptr(unsafe.Pointer(unsafe.SliceData(b)))%4 == 0 {
		return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
	}
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return out
}
