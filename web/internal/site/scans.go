package site

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const scanIDBase = 2_000_000_000

func (s *Server) SetScans(dir string) { s.scanDir = dir }

var scanFileName = regexp.MustCompile(`^(?:page-[0-9]{1,3}\.png|source\.pdf)$`)

func (s *Server) scanFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	file := r.PathValue("file")
	if !ok || id < scanIDBase || s.scanDir == "" || !scanFileName.MatchString(file) {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	var src int
	if err := s.db.QueryRow(ctx, `SELECT source_id FROM legislation_scan WHERE law_id = $1`, id).Scan(&src); err != nil {
		s.notFound(w, r)
		return
	}
	path := filepath.Join(s.scanDir, strconv.Itoa(src), file)
	if _, err := os.Stat(path); err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) aliasRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, id int) bool {
	p := r.URL.EscapedPath()
	if q, ok := s.moveAlias(ctx, p); !ok || q == p {
		return false
	}
	s.redirectFinal(w, r, p)
	return true
}

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
			template.HTMLEscapeString("قراءتان مختلفتان: راجع صورة الصفحة"), template.HTMLEscapeString(one), template.HTMLEscapeString(two))
		p = rest[j+len("\uE002"):]
	}
	b.WriteString(template.HTMLEscapeString(p))
	return template.HTML(b.String())
}

func (c *Scan) SourceName() string {
	switch c.Source {
	case "saderlex":
		return "SaderLex"
	}
	return c.Source
}

var scanReadings = strings.NewReplacer("\uE000", "⟦", "\uE001", " | ", "\uE002", "⟧").Replace

func countScans(laws []LawRow) int {
	n := 0
	for _, l := range laws {
		if l.ID >= scanIDBase {
			n++
		}
	}
	return n
}
