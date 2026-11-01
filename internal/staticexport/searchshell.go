package staticexport

import (
	"bytes"
	_ "embed"
	"html"
	"strings"
)

// The search page is the one page that cannot be a file: its content depends
// on what was typed. SearchShell turns the live /search/ page (the form, the
// hint, the layout) into a static page that draws its results in the browser
// from the JSON API. The script is a same-origin file, so the site's
// Content-Security-Policy (script-src 'self') allows it; apiBase is the API's
// origin, empty for the same origin.

//go:embed searchshell.js
var searchShellJS []byte

// SearchShellFiles are the files the search page loads, by path under the
// output directory.
func SearchShellFiles() map[string][]byte {
	return map[string][]byte{"assets/search-shell.js": searchShellJS}
}

// SearchShell returns a Transform that adds the script to /search/ and
// /en/search/.
func SearchShell(apiBase string) func(path, contentType string, body []byte) []byte {
	tag := `<script defer src="/assets/search-shell.js" data-api="` + html.EscapeString(strings.TrimRight(apiBase, "/")) + `"></script>`
	return func(p, contentType string, body []byte) []byte {
		if p != "/search/" && p != "/en/search/" || !strings.HasPrefix(contentType, "text/html") {
			return body
		}
		i := bytes.LastIndex(body, []byte("</body>"))
		if i < 0 {
			return body
		}
		out := make([]byte, 0, len(body)+len(tag)+1)
		out = append(out, body[:i]...)
		out = append(out, tag...)
		out = append(out, '\n')
		return append(out, body[i:]...)
	}
}
