package site

import (
	"regexp"
	"strings"
)

// DatasetURL is the whole database, public on Hugging Face.
const DatasetURL = "https://huggingface.co/datasets/taktekhq/lebanese-legislation"

// downloadWords are what people type when they want the laws as files, not a
// law: "How can i download the Lebanese laws", "تحميل القوانين", "dataset".
var downloadWords = regexp.MustCompile(`(?i)\b(download|downloads|downloading|dataset|datasets|database dump|bulk|csv|parquet|t[ée]l[ée]charg\w*|export)\b|تحميل|تنزيل|نزّل|حمّل|قاعدة البيانات|قاعدة بيانات`)

// WantsDownload says whether a search asks how to download the laws.
func WantsDownload(q string) bool {
	return downloadWords.MatchString(strings.TrimSpace(q))
}
