package site

import "testing"

func TestGlossQuery(t *testing.T) {
	for in, want := range map[string]string{
		"marriage":               "زواج",
		"The Marriage law":       "زواج",
		"self defense":           "الدفاع المشروع",
		"teaching hours":         "تعليم ساعات",
		"rent":                   "ايجار",
		"search and seizure":     "التفتيش والضبط",
		"civil marriage":         "الزواج المدني",
		"Cybercrime":             "الجرائم المعلوماتية",
		"banking secrecy":        "السرية المصرفية",
		"end of service":         "نهاية الخدمة",
		"nationalité":            "جنسية",
		"travail":                "العمل",
		"Penalty":                "عقوبة",
		"succession":             "ارث",
		"Lebanon’s labor law":    "قانون العمل",
		"homosexual":             "مجامعة على خلاف الطبيعة",
		"زواج":                   "",
		"":                       "",
		"what do you access to?": "",
	} {
		if got := glossQuery(in); got != want {
			t.Errorf("glossQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
