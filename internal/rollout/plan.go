// Package rollout pushes a config to a fleet in waves, watches SLOs between
// waves, and rolls back when a wave goes bad.
package rollout

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// DefaultWaves is 1% -> 10% -> 50% -> 100%.
var DefaultWaves = []float64{1, 10, 50, 100}

// Wave is one step of a rollout.
type Wave struct {
	Index      int      // 1-based
	Percent    float64  // cumulative share of the fleet after this wave
	Cells      []string // cells added in this wave
	Cumulative int      // cells on the new config after this wave
}

// Plan is the ordered list of waves for one change.
type Plan struct {
	Waves []Wave
}

// NewPlan splits cells into waves. percents must be increasing and end at
// 100. Every wave gets at least one new cell, so 1% of a small fleet is
// still a real canary.
//
// Cells are shuffled by hash(salt, cell) so each change gets a different
// canary set (a bad cell doesn't keep getting blamed) but the same change
// always plans the same way.
func NewPlan(cells []string, percents []float64, salt string) (Plan, error) {
	if len(cells) == 0 {
		return Plan{}, fmt.Errorf("no cells to roll out to")
	}
	if len(percents) == 0 {
		return Plan{}, fmt.Errorf("no waves")
	}
	prev := 0.0
	for _, p := range percents {
		if p <= prev || p > 100 {
			return Plan{}, fmt.Errorf("wave percents must increase and stay <= 100, got %v", percents)
		}
		prev = p
	}
	if percents[len(percents)-1] != 100 {
		return Plan{}, fmt.Errorf("last wave must be 100%%, got %v", percents[len(percents)-1])
	}

	order := append([]string(nil), cells...)
	sort.Slice(order, func(i, j int) bool {
		hi, hj := rank(salt, order[i]), rank(salt, order[j])
		if hi != hj {
			return hi < hj
		}
		return order[i] < order[j]
	})

	var plan Plan
	done := 0
	for i, p := range percents {
		target := int(math.Ceil(float64(len(order)) * p / 100))
		if target <= done {
			target = done + 1
		}
		if target > len(order) {
			target = len(order)
		}
		if target == done {
			break // fleet smaller than the number of waves
		}
		plan.Waves = append(plan.Waves, Wave{
			Index:      i + 1,
			Percent:    p,
			Cells:      order[done:target],
			Cumulative: target,
		})
		done = target
	}
	return plan, nil
}

func rank(salt, cell string) uint64 {
	h := sha256.Sum256([]byte(salt + "\x00" + cell))
	return binary.BigEndian.Uint64(h[:8])
}
