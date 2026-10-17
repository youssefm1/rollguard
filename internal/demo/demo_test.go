package demo

import (
	"bytes"
	"testing"
)

func TestBadConfigsContainedAtOnePercent(t *testing.T) {
	for _, name := range []string{"timeout", "latency"} {
		for seed := int64(1); seed <= 5; seed++ {
			p := DefaultParams()
			p.Seed = seed
			prog, bb, err := Compare(Scenarios[name], p)
			if err != nil {
				t.Fatal(err)
			}
			if prog.Result.FailedWave != 1 || prog.Result.ExposedCells != 2 {
				t.Fatalf("%s seed %d: failed wave %d, exposed %d", name, seed, prog.Result.FailedWave, prog.Result.ExposedCells)
			}
			if bb.Result.ExposedCells != p.Cells {
				t.Fatalf("%s: big bang exposed %d", name, bb.Result.ExposedCells)
			}
			if name == "timeout" && prog.ExcessErrors()*50 > bb.ExcessErrors() {
				t.Errorf("progressive %.0f vs all-at-once %.0f excess errors", prog.ExcessErrors(), bb.ExcessErrors())
			}
		}
	}
}

func TestSafeChangeCompletes(t *testing.T) {
	prog, _, err := Compare(Scenarios["safe"], DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if prog.Result.Outcome != "completed" || prog.ExcessErrors() != 0 {
		t.Fatalf("outcome %s, excess %.0f", prog.Result.Outcome, prog.ExcessErrors())
	}
}

func TestGraphRenders(t *testing.T) {
	sc := Scenarios["timeout"]
	prog, bb, err := Compare(sc, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	svg := Graph(sc, prog, bb)
	for _, want := range []string{"<svg", "SLO breach", "all at once", "</svg>"} {
		if !bytes.Contains(svg, []byte(want)) {
			t.Errorf("svg missing %q", want)
		}
	}
}
