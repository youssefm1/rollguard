package chart

import "testing"

func TestNice(t *testing.T) {
	for in, want := range map[float64]float64{0.03: 0.05, 0.9: 1, 1.5: 2, 2.2: 2.5, 7: 10, 140: 200} {
		if got := nice(in); got != want {
			t.Errorf("nice(%v) = %v, want %v", in, got, want)
		}
	}
}
