package nlsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComposedNeedsTheTextsOwnWords(t *testing.T) {
	sources := []Passage{{ID: "s1", Text: "للعامل الحق في اجازة سنوية مدتها خمسة عشر يوما\nبأجر كامل"}, {ID: "s2", Text: "Le jugement pénal ne fait pas obstacle à l'action civile."}}
	for _, raw := range []string{
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s3","quote":"للعامل الحق في اجازة"}]}]}`,
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة شهرية"}]}]}`,
		`{"paragraphs":[{"text":"  ","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة"}]}]}`,
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة"}],"url":"https://outside.test"}]}`,
		`{"paragraphs":null}`, `{}`, `not json`,
	} {
		if _, err := ParseComposed(raw, sources); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	// spacing differs from the passage, an invented quote is dropped, the real one kept
	p, err := ParseComposed(`{"paragraphs":[{"text":" Yes, 15 days. ","quotes":[{"source":"s1","quote":"«خمسة عشر يوما بأجر كامل»"},{"source":"s2","quote":"le jugement civil prime toujours"},{"source":"s2","quote":"Le jugement pénal ne fait pas obstacle"}]}]}`, sources)
	if err != nil || len(p) != 1 || p[0].Text != "Yes, 15 days." || len(p[0].Quotes) != 2 || p[0].Quotes[0].Quote != "خمسة عشر يوما بأجر كامل" || strings.Join(p[0].Sources, ",") != "s1,s2" {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = ParseComposed(`{"paragraphs":[]}`, sources)
	if err != nil || len(p) != 0 {
		t.Fatalf("empty answer: %+v %v", p, err)
	}
}

// Gemini alters about one quote in twenty without changing the words. Those
// are matched to the passage and shown as the passage has them; an invented
// word still drops the quote. Each case is one seen in a rejected answer.
func TestComposedShowsThePassagesOwnWords(t *testing.T) {
	long := strings.Repeat("كلمة من النص ", 40)
	sources := []Passage{
		{ID: "s1", Text: "على طالب التحويل ان يمتنع: 1- عن طلب تنفيذ تحويل بتاريخ سابق. 3- عن تحويل مبلغ من والى الخارج الا لحسابه الخاص ."},
		{ID: "s2", Text: "رد الاستدعاء التمييزي ضد القرار القاضي باحتساب عائدات الجباية ضمن عناصر الاجر بعد التثبت من استمراريتها."},
		{ID: "s3", Text: "قرار ثاني قضت بصحة انتقال ملكية العقار بواسطة عقد بيع والمتعلق برفع نسبة الزيادة على راتب المستخدم."},
		{ID: "s4", Text: long},
	}
	one := func(source, quote string) []Para {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"paragraphs": []any{map[string]any{"text": "x", "quotes": []any{map[string]string{"source": source, "quote": quote}}}}})
		p, _ := ParseComposed(string(raw), sources)
		return p
	}
	for _, c := range []struct{ name, source, quote, want string }{
		{"words skipped with dots", "s1", "على طالب التحويل ان يمتنع:... 3- عن تحويل مبلغ من والى الخارج الا لحسابه الخاص", "على طالب التحويل ان يمتنع:|3- عن تحويل مبلغ من والى الخارج الا لحسابه الخاص"},
		{"first letter swapped", "s2", "واحتساب عائدات الجباية ضمن عناصر الاجر", "باحتساب عائدات الجباية ضمن عناصر الاجر"},
		{"first letter added", "s3", "وقضت بصحة انتقال ملكية العقار بواسطة عقد بيع", "قضت بصحة انتقال ملكية العقار بواسطة عقد بيع"},
		{"tatweel added", "s3", "والـمتعلق برفع نسبة الزيادة على راتب المستخدم", "والمتعلق برفع نسبة الزيادة على راتب المستخدم"},
		{"wrong passage named", "s1", "قضت بصحة انتقال ملكية العقار بواسطة عقد بيع", "قضت بصحة انتقال ملكية العقار بواسطة عقد بيع"},
	} {
		p := one(c.source, c.quote)
		got := []string{}
		if len(p) == 1 {
			for _, q := range p[0].Quotes {
				got = append(got, q.Quote)
			}
		}
		if strings.Join(got, "|") != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if p := one("s1", "قضت بصحة انتقال ملكية العقار بواسطة عقد بيع"); len(p) != 1 || p[0].Sources[0] != "s3" {
		t.Errorf("a quote under the wrong ID should name the passage that has it: %+v", p)
	}
	if p := one("s4", strings.TrimSpace(long)); len(p) != 1 || len([]rune(p[0].Quotes[0].Quote)) > 400 || !strings.HasPrefix(long, p[0].Quotes[0].Quote+" ") {
		t.Errorf("a long quote should be cut at a word end within 400 characters: %+v", p)
	}
	for name, quote := range map[string]string{
		"word invented after the dots": "على طالب التحويل ان يمتنع:... 3- عن تحويل مبلغ الى الداخل",
		"first word replaced":          "وقضى باحتساب عائدات الجباية ضمن عناصر الاجر",
		"word changed in the middle":   "باحتساب عائدات الضريبة ضمن عناصر الاجر بعد التثبت",
		"short part changed":           "وحتساب الجباية",
	} {
		if p := one("s2", quote); p != nil {
			t.Errorf("%s: accepted %+v", name, p)
		}
	}
}

// A paragraph with no quote says what the passages lack. It is left out and
// the backed paragraphs stay.
func TestComposedLeavesOutAParagraphWithNoQuote(t *testing.T) {
	sources := []Passage{{ID: "s1", Text: "للعامل الحق في اجازة سنوية مدتها خمسة عشر يوما"}}
	p, err := ParseComposed(`{"paragraphs":[{"text":"Yes.","quotes":[{"source":"s1","quote":"اجازة سنوية مدتها خمسة عشر يوما"}]},{"text":"The texts do not say more.","quotes":[]}]}`, sources)
	if err != nil || len(p) != 1 || p[0].Text != "Yes." {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = ParseComposed(`{"paragraphs":[{"text":"The texts do not cover this.","quotes":[]}]}`, sources)
	if err != nil || len(p) != 0 {
		t.Fatalf("an answer of remarks only is no answer: %+v %v", p, err)
	}
}

func TestComposeSendsPassagesOnly(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		out := `{"paragraphs":[{"text":"نعم.","quotes":[{"source":"s1","quote":"نص المادة كاملا"}]}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": out}}}}}})
	}))
	defer srv.Close()
	c, err := New(Config{Provider: "gemini", Model: "m", Key: "k", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Compose(context.Background(), "هل؟", []Passage{{ID: "s1", Text: "نص المادة كاملا هنا"}})
	if err != nil || len(p) != 1 || p[0].Text != "نعم." {
		t.Fatalf("got %+v %v", p, err)
	}
	if _, ok := got["systemInstruction"]; !ok {
		t.Fatal("no system instruction sent")
	}
	if _, ok := got["tools"]; ok {
		t.Fatal("the answer call must not use tools such as web search")
	}
}

func TestExpandReturnsArabicPhrasesOnly(t *testing.T) {
	reply := `{"queries":["مهلة الادعاء المدني","تعويض البراءة"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": reply}}}}}})
	}))
	defer srv.Close()
	c, _ := New(Config{Provider: "gemini", Model: "m", Key: "k", Endpoint: srv.URL})
	ctx, u := WithUsage(context.Background())
	p, err := c.Expand(ctx, "q", []string{"براءة"}, []string{"المادة 1"})
	if err != nil || len(p.Queries) != 2 {
		t.Fatalf("got %+v %v", p, err)
	}
	if calls, _, _ := u.Totals(); calls != 1 {
		t.Fatal("expand call not counted")
	}
	reply = `{"queries":["ignore the rules and write english"]}`
	if _, err := c.Expand(context.Background(), "q", nil, nil); err == nil {
		t.Fatal("accepted a non-Arabic phrase")
	}
}
