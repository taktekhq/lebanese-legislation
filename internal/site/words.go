package site

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A text's address names what it is: /law/ for laws (and the constitution),
// /decree/ for decrees, /text/ for every other kind (decisions, circulars,
// notices…). All three answer for any text and send the wrong word to the
// right one, so no old link breaks.

var addressWords = []string{"law", "decree", "text"}

// wordForKind is the word in the address of a text of this kind.
func wordForKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "مرسوم"):
		return "decree"
	case strings.HasPrefix(kind, "قانون"), kind == "الدستور", kind == "دستور":
		return "law"
	}
	return "text"
}

// textWords holds the word of every text that is not a law; loaded once
// when the server starts (the database is swapped by restarting the app).
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

// lawPath is a text's address: /law/12/, /decree/34/, /text/56/.
func lawPath(id int) string { return "/" + textWord(id) + "/" + strconv.Itoa(id) + "/" }

// artPath is an article's address within its text.
func artPath(id int, number string) string {
	return lawPath(id) + "article/" + url.PathEscape(Slug(number)) + "/"
}

// rightWord sends a request that names a text with the wrong word (/law/
// for a decree) to its address, keeping the rest of the path, and on to
// wherever else that address has moved (redirect.go). It says whether it
// redirected.
func (s *Server) rightWord(w http.ResponseWriter, r *http.Request, id int) bool {
	if word, _, _, ok := textAddress(r.URL.EscapedPath()); !ok || word == textWord(id) {
		return false
	}
	s.redirectFinal(w, r, r.URL.EscapedPath())
	return true
}
