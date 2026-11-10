package i18n

// FrenchPages are the pages that have a French version, as root paths. They
// are the site's own pages: the home page, the lists' first pages, the year
// indexes and the pages about the site. A text's pages (about 760,000 of
// them) are not among them: their content is Arabic, and a third copy of each
// would multiply the static export. A French page links a text, an article
// or a ruling to its existing /en/ page. static/language.js holds the same
// list (TestLanguageScriptKnowsTheFrenchPages), and the static export reads
// this one to render and refresh the /fr/ pages.
var FrenchPages = map[string]bool{
	"/": true, "/laws/": true, "/codes/": true, "/topics/": true, "/rulings/": true, "/gazette/": true,
	"/changes/": true, "/opinions/": true, "/agreements/": true, "/doctrine/": true, "/research/": true,
	"/laws/years/": true, "/rulings/years/": true, "/gazette/years/": true, "/search/": true,
	"/data/": true, "/ai/": true, "/run-it-yourself/": true, "/about/": true, "/sources/": true, "/legiliban/": true,
	"/contribute/": true, "/privacy/": true, "/terms/": true,
}
