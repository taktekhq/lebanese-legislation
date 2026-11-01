package site

import "testing"

func TestIsQuestion(t *testing.T) {
	for q, want := range map[string]bool{
		// questions get an AI answer
		"اذا صدر حكم جزائي ببراءة المدعى عليه. هل يمكن للمدعي اللجوء الى القضاء المدني لمطالبة المدعى عليه بالتعويض؟ بالتعويض": true,
		"هل يحق للعامل تعويض صرف":                    true,
		"كم يوم اجازة سنوية للعامل":                  true,
		"ما هي عقوبة الشيك بدون رصيد":                true,
		"شو بيصير اذا ما دفعت الايجار":               true,
		"how is end of service indemnity calculated": true,
		"can my landlord evict me":                   true,
		"combien de jours de congé":                  true,
		"تعويض نهاية الخدمة للموظف في القطاع الخاص عند الاستقالة": true,
		// lookups go straight to the texts
		"قانون العمل":                      false,
		"المادة 248 موجبات وعقود":          false,
		"article 248 code des obligations": false,
		"مرسوم 2552/1992":                  false,
		"القانون رقم 44 تاريخ 11/11/2008":  false,
		"decree 15874":                     false,
		"قانون حماية المستهلك":             false,
		"تعويض":                            false,
		"الصرف التعسفي":                    false,
		"قانون تنظيم مهنة المحاماة في لبنان والنقابات التابعة لها": false,
		"": false,
	} {
		if got := IsQuestion(q); got != want {
			t.Errorf("IsQuestion(%q) = %v, want %v", q, got, want)
		}
	}
}
