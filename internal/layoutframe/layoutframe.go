// Package layoutframe holds the shared layout of a language with the page's
// own parts cut out. internal/site renders frames; internal/staticexport
// stores pages as their parts and joins them back. It imports nothing of the
// project, so the static export can use it without reading the site's data
// code.
package layoutframe

// Frame is layout.html for one interface language with the page's own parts
// cut out: Text[0], the page's part for Slots[0], Text[1], ... Text[n] is the
// whole page.
type Frame struct {
	Lang string // "ar" (the root) or "en"
	// Schema names the holes and how each is escaped where it stands. A page
	// split under one schema fits any frame of the same schema; a change to
	// it is a change to every page.
	Schema string
	Text   []string // len(Slots)+1 pieces of shared HTML
	Slots  []string // the "page:" template of each hole, in order
}
