package site

import (
	"html/template"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/i18n"
)

var (
	enBundle = i18n.New("en")
	arBundle = i18n.New("ar")
)

func arabicFuncs() template.FuncMap {
	return template.FuncMap{
		"t": func(key string, args ...any) template.HTML { return template.HTML(arBundle.T(key, args...)) },

		"uilang": func() string { return "ar" },
		"dir":    func() string { return "rtl" },

		"switchLabel": func() string { return "English" },
		"altURL":      func(p string) string { return Base + "/en" + p },
		"linkVerb":    func(k Link) template.HTML { return template.HTML(arBundle.T("verb." + k.VerbKey())) },
		"changeVerb":  func(c *Change) template.HTML { return template.HTML(arBundle.T("cverb." + c.VerbKey())) },
		"statusText": func(status string) template.HTML {
			if status == "repealed" {
				return template.HTML(arBundle.T("status.repealed"))
			}
			return template.HTML(arBundle.T("status.in_force"))
		},
		"backArrow": func() template.HTML { return "&rarr;" },
		"fwdArrow":  func() template.HTML { return "&larr;" },
		"plural":    func(n int, one, many string) string { return arBundle.PluralAr(n, one, many) },
	}
}

func englishFuncs() template.FuncMap {
	return template.FuncMap{
		"u":          func(p string) string { return "/en" + p },
		"lawURL":     func(id int) string { return "/en" + lawPath(id) },
		"artURL":     func(id int, number string) string { return "/en" + artPath(id, number) },
		"rulingsURL": func(id int, number string) string { return "/en" + artPath(id, number) + "rulings/" },
	}
}
