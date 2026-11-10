package site

import (
	"fmt"
	"html/template"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// enBundle and arBundle are the two UI-string tables; see internal/site/i18n.
// English pages and Arabic pages each get their own template.FuncMap bound
// to one of these (site.go parses the templates once, then arabicFuncs()
// is bound onto a Clone() for the Arabic set; see newServer).
var (
	enBundle = i18n.New("en")
	arBundle = i18n.New("ar")
	frBundle = i18n.New("fr")
)

// escArgs makes the arguments of a UI string safe to format into HTML. The
// string itself is trusted markup, its arguments are not: a template.HTML
// argument (safe "...", a nested t) stays as it is, a string or Stringer is
// HTML-escaped, and anything else (a number for %d) passes unchanged.
func escArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case template.HTML:
			out[i] = string(v)
		case string:
			out[i] = template.HTMLEscapeString(v)
		case fmt.Stringer:
			out[i] = template.HTMLEscapeString(v.String())
		default:
			out[i] = a
		}
	}
	return out
}

// arabicFuncs are the template funcs re-bound on the Arabic clone of every
// page: UI strings
// come from ar.json, and the handful of language-direction details (the
// root lang/dir, the language switch, the back/forward arrows, and Arabic
// numeral-noun agreement) flip for right-to-left.
func arabicFuncs() template.FuncMap {
	return template.FuncMap{
		"t": func(key string, args ...any) template.HTML { return template.HTML(arBundle.T(key, escArgs(args)...)) },
		// Arabic is the root: links need no prefix, so u, lawURL and artURL
		// stay the shared ones (site.go), also on a page served under /ar/.
		"uilang": func() string { return "ar" },
		"dir":    func() string { return "rtl" },
		// switchLabel names the OTHER language, in its own script, as is
		// conventional for a language switcher.
		"switchLabel": func() string { return "English" },
		"altURL":      func(p string) string { return Base + "/en" + p }, // the English version of this same page
		"linkVerb":    func(k Link) template.HTML { return template.HTML(arBundle.T("verb." + k.VerbKey())) },
		"changeVerb":  func(c *Change) template.HTML { return template.HTML(arBundle.T("cverb." + c.VerbKey())) },
		"statusText": func(status string) template.HTML {
			if status == "repealed" {
				return template.HTML(arBundle.T("status.repealed"))
			}
			return template.HTML(arBundle.T("status.in_force"))
		},
		"backArrow": func() template.HTML { return "&rarr;" }, // points toward reading-start, now on the right
		"fwdArrow":  func() template.HTML { return "&larr;" },
		"plural":    func(n int, one, many string) string { return arBundle.PluralAr(n, one, many) },
	}
}

// englishFuncs are the template funcs bound on the English set of every page
// on the live site: internal links gain the /en prefix. The rest stay the
// shared English defaults of site.go.
func englishFuncs() template.FuncMap {
	return template.FuncMap{
		"u":          func(p string) string { return "/en" + p },
		"lawURL":     func(id int) string { return "/en" + lawPath(id) },
		"artURL":     func(id int, number string) string { return "/en" + artPath(id, number) },
		"rulingsURL": func(id int, number string) string { return "/en" + artPath(id, number) + "rulings/" },
	}
}

// frenchFuncs are the template funcs bound on the French set of the pages
// that have a French version (frenchPaths): UI strings come from fr.json,
// links to those pages stay under /fr, and every other internal link (a
// text, an article, a ruling, a topic page) goes to its existing /en page,
// whose content is unchanged.
func frenchFuncs() template.FuncMap {
	return template.FuncMap{
		"t":          func(key string, args ...any) template.HTML { return template.HTML(frBundle.T(key, escArgs(args)...)) },
		"u":          frLink,
		"lawURL":     func(id int) string { return "/en" + lawPath(id) },
		"artURL":     func(id int, number string) string { return "/en" + artPath(id, number) },
		"rulingsURL": func(id int, number string) string { return "/en" + artPath(id, number) + "rulings/" },
		"uilang":     func() string { return "fr" },
		"altURL":     func(p string) string { return Base + p }, // the Arabic version of this same page
		"linkVerb":   func(k Link) template.HTML { return template.HTML(frBundle.T("verb." + k.VerbKey())) },
		"changeVerb": func(c *Change) template.HTML { return template.HTML(frBundle.T("cverb." + c.VerbKey())) },
		"statusText": func(status string) template.HTML {
			if status == "repealed" {
				return template.HTML(frBundle.T("status.repealed"))
			}
			return template.HTML(frBundle.T("status.in_force"))
		},
		"plural": func(n int, one, many string) string { return frBundle.PluralFr(n, one, many) },
		"num":    i18n.ThousandsFr,
	}
}
