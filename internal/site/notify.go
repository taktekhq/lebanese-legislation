package site

import (
	"net/http"
	"net/mail"
	"strings"
)

// NotifyConsent is the wording a sign-up agrees to, kept with each one.
// Change it and the new wording is what later sign-ups keep.
const NotifyConsent = "Email me once if Lebanese Legislation starts answering questions about Lebanese law with AI. " +
	"Use my address for nothing else; I can ask for it to be deleted at any time."

// aiPage is "Lebanese law and AI": what works today, and the sign-up for
// AI answers that don't exist yet.
func (s *Server) aiPage(w http.ResponseWriter, r *http.Request) {
	s.track(r, map[string]any{"k": "page", "path": "/ai/"})
	s.render(w, r, "ai", 200, Page{
		Title: "Lebanese law and AI · Lebanese Legislation",
		Desc:  "How AI assistants can read and cite Lebanese law on this site today, and a way to hear if we ever add AI answers. No AI answers exist here yet.",
		Path:  "/ai/", NoIndex: r.URL.RawQuery != "",
		Data: map[string]any{"Done": r.URL.Query().Get("notify"), "Consent": NotifyConsent, "Static": s.isStatic,
			"Notify": s.ev != nil},
	})
}

// notify takes the sign-up form. It works without JavaScript: the answer is
// a redirect back to the page with ?notify=ok (or =bad).
func (s *Server) notify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	back := "/ai/?notify="
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, back+"bad#notify", http.StatusSeeOther)
		return
	}
	if r.PostForm.Get("website") != "" { // the hidden field only robots fill in
		http.Redirect(w, r, back+"ok#notify", http.StatusSeeOther)
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 200 || r.PostForm.Get("consent") != "yes" {
		http.Redirect(w, r, back+"bad#notify", http.StatusSeeOther)
		return
	}
	from := r.PostForm.Get("from")
	if len(from) > 40 {
		from = from[:40]
	}
	if err := s.ev.Notify(email, from, NotifyConsent); err != nil {
		http.Redirect(w, r, back+"bad#notify", http.StatusSeeOther)
		return
	}
	s.ev.Log(map[string]any{"k": "notify", "from": from}) // the count only; the address stays in notify.jsonl
	http.Redirect(w, r, back+"ok#notify", http.StatusSeeOther)
}
