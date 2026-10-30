package site

import (
	"slices"
	"testing"
)

func TestInForceFirst(t *testing.T) {
	num := func(s string) *string { return &s }
	row := func(id int, status string) LawRow { return LawRow{ID: id, Status: status, Number: num("1")} }
	ids := func(ls []LawRow) []int {
		var out []int
		for _, l := range ls {
			out = append(out, l.ID)
		}
		return out
	}
	for _, c := range []struct {
		in   []LawRow
		want []int
	}{

		{[]LawRow{row(1, "repealed"), row(2, "repealed"), row(3, "in_force"), row(4, "in_force")}, []int{3, 1, 2, 4}},

		{[]LawRow{row(1, "in_force"), row(2, "repealed"), row(3, "in_force")}, []int{1, 2, 3}},

		{[]LawRow{{ID: 1, Status: "repealed", Code: num("Penal Code")}, row(2, "in_force")}, []int{1, 2}},
	} {
		if got := ids(inForceFirst(c.in, nil, false)); !slices.Equal(got, c.want) {
			t.Errorf("inForceFirst %v = %v, want %v", ids(c.in), got, c.want)
		}
	}
}
