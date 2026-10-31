package site

import (
	"sort"
	"strings"
	"unicode"
)

var glossPhrases = map[string]string{
	"self defense":        "الدفاع المشروع",
	"self defence":        "الدفاع المشروع",
	"legitimate defense":  "الدفاع المشروع",
	"labor law":           "قانون العمل",
	"labour law":          "قانون العمل",
	"civil status":        "الاحوال الشخصية",
	"personal status":     "الاحوال الشخصية",
	"driving license":     "رخصة قيادة",
	"driving licence":     "رخصة قيادة",
	"road traffic":        "السير",
	"code of obligations": "قانون الموجبات والعقود",
	"penal code":          "قانون العقوبات",
	"civil procedure":     "اصول المحاكمات المدنية",
	"criminal procedure":  "اصول المحاكمات الجزائية",
	"droit du travail":    "قانون العمل",
	"code pénal":          "قانون العقوبات",
	"code penal":          "قانون العقوبات",
}

var glossWords = map[string]string{
	"marriage": "زواج", "mariage": "زواج", "married": "زواج", "wedding": "زواج",
	"divorce": "طلاق", "inheritance": "ارث", "succession": "ارث", "heritage": "ارث",
	"amnesty": "عفو", "pardon": "عفو", "amnistie": "عفو",
	"rent": "ايجار", "rental": "ايجار", "lease": "ايجار", "tenant": "ايجار", "loyer": "ايجار", "bail": "ايجار",
	"traffic": "السير", "driving": "قيادة", "driver": "قيادة", "car": "سيارات", "cars": "سيارات", "vehicle": "مركبات", "vehicles": "مركبات",
	"tax": "ضريبة", "taxes": "ضريبة", "impot": "ضريبة", "impôt": "ضريبة", "vat": "القيمة المضافة", "tva": "القيمة المضافة",
	"nationality": "جنسية", "citizenship": "جنسية", "nationalité": "جنسية",
	"company": "شركة", "companies": "شركات", "société": "شركة", "societe": "شركة", "sarl": "محدودة المسؤولية", "sal": "مغفلة",
	"contract": "عقد", "contrat": "عقد", "contracts": "عقود",
	"labor": "العمل", "labour": "العمل", "work": "العمل", "employment": "العمل", "travail": "العمل", "salary": "اجور", "wage": "اجور", "wages": "اجور",
	"bank": "مصارف", "banks": "مصارف", "banking": "مصارف", "banque": "مصارف",
	"land": "عقار", "property": "عقار", "real-estate": "عقار", "immobilier": "عقار",
	"election": "انتخاب", "elections": "انتخاب", "vote": "انتخاب",
	"constitution": "الدستور", "homosexual": "مجامعة على خلاف الطبيعة", "homosexuality": "مجامعة على خلاف الطبيعة", "gay": "مجامعة على خلاف الطبيعة", "emir": "امير", "amir": "امير", "prince": "امير", "murder": "قتل", "theft": "سرقة", "fraud": "احتيال",
	"drugs": "المخدرات", "narcotics": "المخدرات", "defamation": "قدح", "libel": "قدح",
	"foreigner": "اجانب", "foreigners": "اجانب", "residency": "اقامة", "visa": "اقامة",
	"school": "مدارس", "schools": "مدارس", "teacher": "معلمين", "teachers": "معلمين", "teaching": "تعليم", "education": "تعليم",
	"hours": "ساعات", "pension": "تقاعد", "retirement": "تقاعد", "social-security": "الضمان الاجتماعي",
	"court": "محكمة", "courts": "محاكم", "judge": "قاضي", "judges": "قضاة", "lawyer": "محاماة", "lawyers": "محاماة",
	"customs": "الجمارك", "import": "استيراد", "export": "تصدير", "insurance": "تأمين", "assurance": "تأمين",
}

var glossStop = map[string]bool{"the": true, "of": true, "a": true, "an": true, "in": true, "and": true,
	"law": true, "laws": true, "lebanon": true, "lebanese": true, "le": true, "la": true, "les": true, "de": true, "du": true, "des": true, "et": true, "on": true, "for": true, "to": true}

func hasArabic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

func latinFold(s string) string {
	return strings.ToLower(latinAccents.Replace(s))
}

var latinAccents = strings.NewReplacer("’", "'", "‘", "'", "é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a",
	"î", "i", "ï", "i", "ô", "o", "ù", "u", "û", "u", "ç", "c", "É", "E", "È", "E", "À", "A", "Ç", "C")

var (
	glossPhraseKeys []string
	glossPhraseAr   = map[string]string{}
	glossWordAr     = map[string]string{}
)

func init() {
	for _, m := range []map[string]string{glossTerms, glossPhrases, glossWords} {
		for k, ar := range m {
			k = latinFold(k)
			if strings.ContainsAny(k, " ") {
				glossPhraseAr[k] = ar
			} else {
				glossWordAr[k] = ar
			}
		}
	}
	for k := range glossPhraseAr {
		glossPhraseKeys = append(glossPhraseKeys, k)
	}
	sort.Slice(glossPhraseKeys, func(i, j int) bool {
		if len(glossPhraseKeys[i]) != len(glossPhraseKeys[j]) {
			return len(glossPhraseKeys[i]) > len(glossPhraseKeys[j])
		}
		return glossPhraseKeys[i] < glossPhraseKeys[j]
	})
}

func latinTokens(q string) []string {
	toks := strings.FieldsFunc(latinFold(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '\''
	})
	for i, t := range toks {
		toks[i] = strings.TrimSuffix(t, "'s")
	}
	return toks
}

func glossQuery(q string) string {
	ar, _ := glossQueryWhole(q)
	return ar
}

func glossQueryWhole(q string) (string, bool) {
	if strings.TrimSpace(q) == "" || hasArabic(q) {
		return "", false
	}
	padded := " " + strings.Join(latinTokens(q), " ") + " "
	var out []string
	for _, p := range glossPhraseKeys {
		if strings.Contains(padded, " "+p+" ") {
			out = append(out, glossPhraseAr[p])
			padded = strings.ReplaceAll(padded, " "+p+" ", " ")
		}
	}
	whole := true
	for _, w := range strings.Fields(padded) {
		if glossStop[w] || arabiziStop[w] {
			continue
		}
		if ar, ok := glossWordAr[w]; ok {
			out = append(out, ar)
		} else {
			whole = false
		}
	}
	return strings.Join(out, " "), whole && len(out) > 0
}
