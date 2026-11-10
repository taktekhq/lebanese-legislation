package topics

import "testing"

func sp(s string) *string { return &s }

// The wording the checker reads is the one the article's page prints.
func TestShown(t *testing.T) {
	const centre, clause = "النص الكامل للمادة كما يحفظه المركز", "البند السادس فقرة (4) (ب) (الجديدة)"
	cases := []struct {
		name     string
		note     string
		versions []*string
		want     string
	}{
		{"never amended", "", nil, centre},
		{"versions without a wording", "(عدلت بموجب قانون 277/2022 )", []*string{nil, nil}, centre},
		{"the latest wording wins", "", []*string{sp("القديم"), sp("الجديد")}, "الجديد"},
		// Social Security Law (law 244971) Article 9 on 10 Nov 2026: four
		// versions, and only the third has a wording, the one clause Law 323
		// of 2023 replaced (710 characters of an article of 13,863). The
		// page prints that clause as the article, so a quote from the rest
		// of the article is not on the page it links to.
		{"SSL Article 9: a clause read as the article", "(عدلت بموجب قانون 323/2023 )", []*string{nil, nil, sp(clause), nil}, clause},
		{"the Centre's note says repealed", " مادة ملغاة (الغيت بموجب قانون 1/2000 )", []*string{sp("القديم")}, centre},
	}
	for _, c := range cases {
		if got := Shown(centre, c.note, c.versions); got != c.want {
			t.Errorf("%s: Shown = %q, want %q", c.name, got, c.want)
		}
	}
}
