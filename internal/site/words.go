package site

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var addressWords = []string{"law", "decree", "text"}

func wordForKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "مرسوم"):
		return "decree"
	case strings.HasPrefix(kind, "قانون"), kind == "الدستور", kind == "دستور":
		return "law"
	}
	return "text"
}

var textWords = map[int]string{}

func loadTextWords(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, `SELECT id, kind FROM legislation`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[int]string{}
	for rows.Next() {
		var id int
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return err
		}
		if w := wordForKind(kind); w != "law" {
			m[id] = w
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	textWords = m
	return nil
}

func textWord(id int) string {
	if w, ok := textWords[id]; ok {
		return w
	}
	return "law"
}

func lawPath(id int) string { return "/" + textWord(id) + "/" + strconv.Itoa(id) + "/" }

func artPath(id int, number string) string {
	return lawPath(id) + "article/" + url.PathEscape(Slug(number)) + "/"
}

func (s *Server) rightWord(w http.ResponseWriter, r *http.Request, id int) bool {
	if word, _, _, ok := textAddress(r.URL.EscapedPath()); !ok || word == textWord(id) {
		return false
	}
	s.redirectFinal(w, r, r.URL.EscapedPath())
	return true
}
