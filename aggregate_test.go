package main

import (
	"encoding/json"
	"math/big"
	"testing"
)

// A denominator is a finite decimal only when nothing is left of it after the 2s and
// 5s are divided out. Any other factor (3, 7, 11, ...) repeats forever and must get the
// 17-digit answer rather than being cut to the places the 2s and 5s alone would need.
func TestRationalJSONRepeatingDecimals(t *testing.T) {
	cases := []struct {
		num, den int64
		want     string
	}{
		{1, 2, "0.5"},
		{1, 8, "0.125"},
		{7, 20, "0.35"},
		{9007199254741053, 4, "2251799813685263.25"},
		{6, 3, "2"},
		// Expected values for the repeating cases come from Python's decimal module at
		// 17 significant digits, computed separately from this code path.
		{1, 3, "0.33333333333333333"},
		{-1, 3, "-0.33333333333333333"},
		{4, 3, "1.3333333333333333"},
		{2, 7, "0.28571428571428571"},
		{1, 6, "0.16666666666666667"},
		{41597, 770, "54.022077922077922"},
		{382337, 7700, "49.654155844155844"},
	}
	for _, c := range cases {
		got := rationalJSON(big.NewRat(c.num, c.den))
		if got != json.Number(c.want) {
			t.Errorf("rationalJSON(%d/%d) = %s, want %s", c.num, c.den, got, c.want)
		}
	}
}

func TestAverageOfRepeatingQuotientIsNotTruncated(t *testing.T) {
	e, _ := setupDocuments(t)
	for i, age := range []string{"1", "1", "2"} {
		r := document(string(rune('a'+i))+"@x.com", json.Number(age))
		if _, err := e.Put(r, 0); err != nil {
			t.Fatal(err)
		}
	}
	page, err := e.Documents("u", "w", "users", DocumentQuery{
		Aggregate: []Aggregation{{Fn: "avg", Field: "age"}},
	})
	if err != nil || len(page.Groups) != 1 {
		t.Fatal(page, err)
	}
	if got := page.Groups[0].Values["avg_age"]; got != json.Number("1.3333333333333333") {
		t.Fatalf("avg(1,1,2) = %v, want 1.3333333333333333", got)
	}
}
