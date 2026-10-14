package config

import (
	"fmt"
	"strings"
)

// Rule is a static range check on one integer key.
type Rule struct {
	Key      string
	Min, Max int
	Required bool
}

// Schema is the set of static checks a config must pass before it is
// allowed to start rolling out. Static checks catch typos and nonsense
// values; they can't tell you a value is wrong for production. That's
// what the waves are for.
type Schema []Rule

// DefaultSchema covers the keys the simulated service reads.
var DefaultSchema = Schema{
	{Key: "upstream_timeout_ms", Min: 1, Max: 60000, Required: true},
	{Key: "retries", Min: 0, Max: 5, Required: true},
	{Key: "cache_mb", Min: 0, Max: 65536},
}

// Validate returns every rule c breaks, joined into one error.
func (s Schema) Validate(c Config) error {
	var probs []string
	for _, r := range s {
		raw, ok := c.Values[r.Key]
		if !ok {
			if r.Required {
				probs = append(probs, fmt.Sprintf("%s is required", r.Key))
			}
			continue
		}
		if _, isNum := raw.(float64); !isNum {
			if _, isInt := raw.(int); !isInt {
				probs = append(probs, fmt.Sprintf("%s must be a number, got %T", r.Key, raw))
				continue
			}
		}
		v := c.Int(r.Key, 0)
		if v < r.Min || v > r.Max {
			probs = append(probs, fmt.Sprintf("%s=%d out of range [%d, %d]", r.Key, v, r.Min, r.Max))
		}
	}
	if len(probs) > 0 {
		return fmt.Errorf("invalid config %q: %s", c.Name, strings.Join(probs, "; "))
	}
	return nil
}
