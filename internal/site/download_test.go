package site

import "testing"

func TestWantsDownload(t *testing.T) {
	yes := []string{"How can i download the Lebanese laws", "lebanese laws dataset", "Download", "télécharger les lois", "تحميل القوانين اللبنانية", "تنزيل قاعدة البيانات", "laws csv"}
	no := []string{"قانون السير", "motorcycle", "Lebanese laws", "قرار رقم 513/2005", "حماية القصار", "downtown"}
	for _, q := range yes {
		if !WantsDownload(q) {
			t.Errorf("WantsDownload(%q) = false, want true", q)
		}
	}
	for _, q := range no {
		if WantsDownload(q) {
			t.Errorf("WantsDownload(%q) = true, want false", q)
		}
	}
}
