package site

import (
	"regexp"
	"strings"
)

const DatasetURL = "https://huggingface.co/datasets/taktekhq/lebanese-legislation"

var downloadWords = regexp.MustCompile(`(?i)\b(download|downloads|downloading|dataset|datasets|database dump|bulk|csv|parquet|t[ée]l[ée]charg\w*|export)\b|تحميل|تنزيل|نزّل|حمّل|قاعدة البيانات|قاعدة بيانات`)

func WantsDownload(q string) bool {
	return downloadWords.MatchString(strings.TrimSpace(q))
}
