package site

import "github.com/taktekhq/lebanese-legislation/internal/site/topics"

// homeTopicSlugs are the topics the home pages link, the questions people ask
// most first. One hop from the home page, the most crawled page, puts the
// answers in front of search engines and assistants.
var homeTopicSlugs = []string{
	"rent-law",
	"end-of-service-indemnity",
	"inheritance",
	"income-tax-brackets",
	"traffic-fines",
	"nssf-registration-contributions",
	"nationality",
	"personal-status-civil-marriage",
	"company-formation",
	"working-hours-annual-leave",
	"banking-secrecy",
	"capital-control",
	"cybercrime-electronic-transactions",
	"foreign-workers-work-permits",
	"building-violations-encroachment",
	"pre-emption-right",
	"dismissal-notice-period",
	"pretrial-detention",
	"anti-money-laundering-law-44",
}

// homeTopicRefs are those topics in one language; a slug with no topic file
// is left out (the tests catch it).
func homeTopicRefs(lang string) []TopicRef {
	var out []TopicRef
	for _, slug := range homeTopicSlugs {
		t := topics.Get(slug)
		if t == nil {
			continue
		}
		c := t.Lang(lang)
		out = append(out, TopicRef{t.Slug, c.Title, c.Question})
	}
	return out
}
