// Redirect resolution mirrors workers/static-site/src/route.js.
package main

import (
	"net/url"
	"strconv"
	"strings"
)

type RedirectData struct {
	// Decree and Text list the ids of texts whose address word is not "law",
	// as flattened ranges: [start, count, start, count, ...].
	Decree []int `json:"decree"`
	Text   []int `json:"text"`
	// Aliases map the id of an old scanned text to the text that replaced it.
	Aliases [][2]int `json:"aliases"`

	words map[int]string
	alias map[int]int
}

func (d *RedirectData) index() {
	d.words = map[int]string{}
	for _, r := range []struct {
		word string
		list []int
	}{{"decree", d.Decree}, {"text", d.Text}} {
		for i := 0; i+1 < len(r.list); i += 2 {
			for id := r.list[i]; id < r.list[i]+r.list[i+1]; id++ {
				d.words[id] = r.word
			}
		}
	}
	d.alias = map[int]int{}
	for _, a := range d.Aliases {
		d.alias[a[0]] = a[1]
	}
}

// Word is the address word of text id.
func (d *RedirectData) Word(id int) string {
	if w, ok := d.words[id]; ok {
		return w
	}
	return "law"
}

var addressWords = []string{"law", "decree", "text"}

// textAddress splits /{word}/{id}/rest, as the live handler does.
func textAddress(p string) (word string, id int, rest string, ok bool) {
	parts := strings.SplitN(p, "/", 4) // "", word, id, rest
	if len(parts) < 4 {
		return "", 0, "", false
	}
	for _, w := range addressWords {
		if parts[1] == w {
			if id, err := strconv.Atoi(parts[2]); err == nil && id > 0 {
				return w, id, parts[3], true
			}
		}
	}
	return "", 0, "", false
}

// Resolve applies the live site's moves to an escaped path (no query) until
// none changes it, and returns where it ends up. exists says whether the
// edge holds a page at a path; it decides the trailing-slash move, which the
// live site makes only where its router has a slashed page. Of the article
// spelling moves only Arabic-Indic digits are here (see the file comment).
func (d *RedirectData) Resolve(p string, exists func(string) bool) string {
	for range 10 {
		moved := false
		// /ar/... is the same path at the root
		if p == "/ar" || strings.HasPrefix(p, "/ar/") {
			p, moved = "/"+strings.TrimLeft(p[len("/ar"):], "/"), true
		}
		// a page address without its final slash
		if !strings.HasSuffix(p, "/") && !strings.Contains(p[strings.LastIndex(p, "/"):], ".") && exists(p+"/") {
			p, moved = p+"/", true
		}
		// a text named with the wrong word
		if word, id, rest, ok := textAddress(p); ok {
			if want := d.Word(id); want != word {
				p, moved = "/"+want+"/"+strconv.Itoa(id)+"/"+rest, true
			}
			// an old scanned text the Centre has published
			if to, ok := d.alias[id]; ok {
				if strings.HasPrefix(rest, "article/") {
					p, moved = "/"+d.Word(to)+"/"+strconv.Itoa(to)+"/"+rest, true
				} else {
					p, moved = "/"+d.Word(to)+"/"+strconv.Itoa(to)+"/", true
				}
			}
		}
		// an article number written with Arabic-Indic digits ("٤٢" for 42), when
		// the ASCII spelling is a page (the other spellings stay with the origin)
		if word, id, rest, ok := textAddress(p); ok && strings.HasPrefix(rest, "article/") {
			if seg, tail, found := strings.Cut(rest[len("article/"):], "/"); found {
				if dec, err := url.PathUnescape(seg); err == nil && hasIndic(dec) {
					cand := "/" + word + "/" + strconv.Itoa(id) + "/article/" + encodeURIComponent(asciiDigits(dec)) + "/" + tail
					if exists(cand) {
						p, moved = cand, true
					}
				}
			}
		}
		if !moved {
			break
		}
	}
	return p
}

func hasIndic(s string) bool {
	for _, r := range s {
		if r >= '٠' && r <= '٩' {
			return true
		}
	}
	return false
}

// asciiDigits writes Arabic-Indic digits as ASCII digits.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '٠' && r <= '٩' {
			return '0' + (r - '٠')
		}
		return r
	}, s)
}

// encodeURIComponent uses the Worker's JavaScript escaping, including punctuation.
func encodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.!~*'()", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
