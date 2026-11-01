package judgecheck

import (
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

func TestValidateItemsRequiresExactCitationAndPart(t *testing.T) {
	item := judge.Item{Key: "2000200207/2/1/5", Article: "5", LawID: 456, Part: "الفقرة ٢ من البند أولاً", Amending: "تلغى الفقرة ٢ من المادة الخامسة.", Centre: "المادة الخامسة\nالبند أولاً\nالفقرة ١: تبقى.\nالفقرة ٢: تلغى."}
	citation := cite.Citation{Key: "2000200207/2", Changes: []cite.Change{{Action: "repeal", Target: cite.Target{Kind: "law", Number: "22", Article: "5", Part: item.Part}, Valid: true}}}
	resolver := func(_ int, _ cite.Citation) []cite.Link {
		return []cite.Link{{Change: citation.Changes[0], LawID: 456, HasArt: true}}
	}
	valid, gaps := ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(valid) != 1 || len(gaps) != 0 {
		t.Fatalf("valid=%d gaps=%v", len(valid), gaps)
	}
	item.Part = "الفقرة ١"
	valid, gaps = ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(valid) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "does not match judge part") {
		t.Fatalf("valid=%d gaps=%v", len(valid), gaps)
	}
}

func TestValidateItemsRejectsMissingAmbiguousAndWrongArticleCitations(t *testing.T) {
	item := judge.Item{Key: "2000200207/2/1/5", Article: "5", LawID: 456, Amending: "repeal article", Centre: "article text"}
	changes := []cite.Change{{Action: "repeal", Target: cite.Target{Article: "8"}, Valid: true}}
	citations := map[string]cite.Citation{"2000200207/2": {Key: "2000200207/2", Changes: changes}}
	valid, gaps := ValidateItems([]judge.Item{item}, citations, func(int, cite.Citation) []cite.Link { return nil })
	if len(valid) != 0 || len(gaps) != 1 {
		t.Fatalf("missing resolution: valid=%d gaps=%v", len(valid), gaps)
	}
	changes = append(changes, cite.Change{Action: "repeal", Target: cite.Target{Article: "5"}, Valid: true})
	citations["2000200207/2"] = cite.Citation{Key: "2000200207/2", Changes: changes}
	resolver := func(int, cite.Citation) []cite.Link {
		return []cite.Link{{Change: changes[1], LawID: 456}, {Change: changes[1], LawID: 456}}
	}
	valid, gaps = ValidateItems([]judge.Item{item}, citations, resolver)
	if len(valid) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "uniquely") {
		t.Fatalf("ambiguous resolution: valid=%d gaps=%v", len(valid), gaps)
	}
	if _, _, _, _, ok := itemKey("not/a/key"); ok {
		t.Fatal("malformed judge key parsed")
	}
}

func TestValidateItemsRequiresPartSpecificCitation(t *testing.T) {
	item := judge.Item{Key: "2000200207/2/1/5", Article: "5", LawID: 456, Part: "الفقرة ٢ من البند أولاً", Amending: "تلغى الفقرة ٢ من المادة الخامسة.", Centre: "المادة الخامسة\nالبند أولاً\nالفقرة ٢: تلغى."}
	citation := cite.Citation{Key: "2000200207/2", Changes: []cite.Change{{Action: "repeal", Target: cite.Target{Article: "5", Part: item.Part}, Valid: true}}}
	resolver := func(int, cite.Citation) []cite.Link {
		return []cite.Link{{Change: citation.Changes[0], LawID: 456, HasArt: true}}
	}
	items, gaps := ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(items) != 1 || len(gaps) != 0 {
		t.Fatalf("matching cited part: items=%d gaps=%v", len(items), gaps)
	}
	item.Part = "الفقرة ١"
	items, gaps = ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(items) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "does not match judge part") {
		t.Fatalf("wrong cited part: items=%d gaps=%v", len(items), gaps)
	}
	item.Part = ""
	items, gaps = ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(items) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "does not match judge part") {
		t.Fatalf("part citation with whole-article judgement: items=%d gaps=%v", len(items), gaps)
	}
	citation.Changes[0].Target.Part = ""
	item.Part = "الفقرة ٢ من البند أولاً"
	items, gaps = ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(items) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "does not match judge part") {
		t.Fatalf("whole-article citation with part judgement: items=%d gaps=%v", len(items), gaps)
	}
}

func TestValidateItemsRejectsMissingTargetArticle(t *testing.T) {
	item := judge.Item{Key: "2000200207/2/1/5", Article: "5", LawID: 456, Amending: "repeal article", Centre: "article text"}
	citation := cite.Citation{Key: "2000200207/2", Changes: []cite.Change{{Action: "repeal", Target: cite.Target{Article: "5"}, Valid: true}}}
	resolver := func(int, cite.Citation) []cite.Link {
		return []cite.Link{{Change: citation.Changes[0], LawID: 456, HasArt: false}}
	}
	items, gaps := ValidateItems([]judge.Item{item}, map[string]cite.Citation{citation.Key: citation}, resolver)
	if len(items) != 0 || len(gaps) != 1 || !strings.Contains(gaps[0], "does not contain article") {
		t.Fatalf("missing target article: items=%d gaps=%v", len(items), gaps)
	}
}
