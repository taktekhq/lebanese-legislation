package site

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// Texts the Lebanese University has not published yet, read from scans of
// the Gazette's own pages (internal/build/scans.go). Their ids start here.
const scanIDBase = 2_000_000_000

// scanRedirect sends old scan file URLs to the original PDF at the source.
func (s *Server) scanRedirect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	var sourceURL string
	if err := s.db.QueryRow(ctx, `SELECT source_url FROM legislation_scan WHERE law_id = $1`, id).Scan(&sourceURL); err != nil {
		s.notFound(w, r)
		return
	}
	http.Redirect(w, r, sourceURL, http.StatusMovedPermanently)
}

// aliasRedirect sends the old address of a scanned text, which the Centre
// has since published, to the Centre's text. It says whether it did.
func (s *Server) aliasRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, id int) bool {
	p := r.URL.EscapedPath()
	if q, ok := s.moveAlias(ctx, p); !ok || q == p {
		return false
	}
	s.redirectFinal(w, r, p)
	return true
}

// marks shows a paragraph of a scanned text. Where the two readings differ
// (U+E000 one U+E001 two U+E002) both are shown, highlighted, so a word one
// reader got wrong never passes for the text: the reader settles it against
// the original PDF. Text without marks comes out as plain escaped text.
func marks(p string) template.HTML {
	if !strings.ContainsRune(p, '\uE000') {
		return template.HTML(template.HTMLEscapeString(p))
	}
	var b strings.Builder
	for {
		i := strings.IndexRune(p, '\uE000')
		if i < 0 {
			break
		}
		b.WriteString(template.HTMLEscapeString(p[:i]))
		rest := p[i+len("\uE000"):]
		j := strings.IndexRune(rest, '\uE002')
		if j < 0 {
			p = rest
			break
		}
		one, two, _ := strings.Cut(rest[:j], "\uE001")
		one, two = strings.TrimSpace(one), strings.TrimSpace(two)
		if one == "" {
			one = "…"
		}
		if two == "" {
			two = "…"
		}
		fmt.Fprintf(&b, `<mark class="unsure" title="%s">%s<span class="unsure__or"> / </span>%s</mark>`,
			template.HTMLEscapeString("قراءتان مختلفتان: راجع ملف PDF الأصلي"), template.HTMLEscapeString(one), template.HTMLEscapeString(two))
		p = rest[j+len("\uE002"):]
	}
	b.WriteString(template.HTMLEscapeString(p))
	return template.HTML(b.String())
}

// SourceName is the archive the scans came from, as people know it.
func (c *Scan) SourceName() string {
	switch c.Source {
	case "saderlex":
		return "SaderLex"
	}
	return c.Source
}

// scanReadings writes a disputed span as ⟦one | two⟧ for the API.
var scanReadings = strings.NewReplacer("\uE000", "⟦", "\uE001", " | ", "\uE002", "⟧").Replace

// countScans is how many of the texts were read from Gazette scans.
func countScans(laws []LawRow) int {
	n := 0
	for _, l := range laws {
		if l.ID >= scanIDBase {
			n++
		}
	}
	return n
}
