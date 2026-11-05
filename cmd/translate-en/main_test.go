package main

import (
	"slices"
	"testing"
)

func TestNumberChecks(t *testing.T) {
	for _, c := range []struct {
		ar, en string
		want   []string
	}{
		{"مادة 207: تعتبر دراجة آلية كل مركبة لا يتجاوز وزنها 400 كلغ", "Every motor vehicle whose weight does not exceed 400 kg", nil},
		{"الصادر في 30 ك1 سنة 1926", "issued on 30 December 1926", nil},
		{"غرامة من 100000 الى مليون ليرة", "a fine of 100,000 to one million pounds", nil},
		{"يعاقب بالحبس سنة وبالغرامة 50 الف ليرة", "imprisonment for one year and a fine", []string{"50"}},
	} {
		if got := numberChecks(c.ar, c.en); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.ar, got, c.want)
		}
	}
}
