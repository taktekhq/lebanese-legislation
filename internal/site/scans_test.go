package site

import "testing"

func TestMarks(t *testing.T) {
	const title = `title="قراءتان مختلفتان: راجع صورة الصفحة"`
	cases := map[string]string{
		"نص <عادي>": "نص &lt;عادي&gt;",
		"الإمضاء: \uE000إميل لحود\uE001جوزاف عون\uE002": `الإمضاء: <mark class="unsure" ` + title + `>إميل لحود<span class="unsure__or"> / </span>جوزاف عون</mark>`,
		"\uE000\uE001|\uE002 ١": `<mark class="unsure" ` + title + `>…<span class="unsure__or"> / </span>|</mark> ١`,
	}
	for in, want := range cases {
		if got := string(marks(in)); got != want {
			t.Errorf("marks(%q) = %q, want %q", in, got, want)
		}
	}
}
