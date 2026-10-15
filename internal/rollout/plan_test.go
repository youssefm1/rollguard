package rollout

import (
	"fmt"
	"testing"
)

func cells(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("c%03d", i)
	}
	return out
}

func TestPlanSizes(t *testing.T) {
	p, err := NewPlan(cells(200), DefaultWaves, "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 20, 100, 200}
	seen := map[string]bool{}
	for i, w := range p.Waves {
		if w.Cumulative != want[i] {
			t.Errorf("wave %d cumulative = %d, want %d", i+1, w.Cumulative, want[i])
		}
		for _, c := range w.Cells {
			if seen[c] {
				t.Errorf("%s in two waves", c)
			}
			seen[c] = true
		}
	}
	if len(seen) != 200 {
		t.Errorf("covered %d cells", len(seen))
	}
}

func TestPlanSmallFleetStillCanaries(t *testing.T) {
	p, err := NewPlan(cells(5), DefaultWaves, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Waves[0].Cells) != 1 {
		t.Fatalf("first wave = %d cells", len(p.Waves[0].Cells))
	}
	if last := p.Waves[len(p.Waves)-1]; last.Cumulative != 5 {
		t.Fatalf("last wave covers %d", last.Cumulative)
	}
}

func TestPlanDeterministicPerChange(t *testing.T) {
	a, _ := NewPlan(cells(200), DefaultWaves, "change-1")
	b, _ := NewPlan(cells(200), DefaultWaves, "change-1")
	c, _ := NewPlan(cells(200), DefaultWaves, "change-2")
	if fmt.Sprint(a.Waves[0].Cells) != fmt.Sprint(b.Waves[0].Cells) {
		t.Error("same change planned differently")
	}
	if fmt.Sprint(a.Waves[1].Cells) == fmt.Sprint(c.Waves[1].Cells) {
		t.Error("different changes got the same canaries")
	}
}

func TestPlanRejectsBadWaves(t *testing.T) {
	for _, w := range [][]float64{nil, {10, 5, 100}, {1, 10, 50}, {0, 100}, {50, 150}} {
		if _, err := NewPlan(cells(10), w, "x"); err == nil {
			t.Errorf("waves %v accepted", w)
		}
	}
	if _, err := NewPlan(nil, DefaultWaves, "x"); err == nil {
		t.Error("empty fleet accepted")
	}
}
