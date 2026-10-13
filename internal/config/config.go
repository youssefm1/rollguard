// Package config holds the config documents that get rolled out to cells.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Config is a named set of key/value settings pushed to a cell.
type Config struct {
	Name   string         `json:"name"`
	Values map[string]any `json:"values"`
}

// Load reads a JSON config document from disk.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Values == nil {
		c.Values = map[string]any{}
	}
	return c, nil
}

// Version is a short content hash of the values. Two configs with the same
// values have the same version no matter the key order or name.
func (c Config) Version() string {
	b, _ := json.Marshal(c.Values) // map keys marshal sorted
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// Int returns an integer setting, or def if it is missing or not a number.
func (c Config) Int(key string, def int) int {
	switch v := c.Values[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return def
}

// With returns a copy of c with key set to v.
func (c Config) With(key string, v any) Config {
	out := Config{Name: c.Name, Values: make(map[string]any, len(c.Values)+1)}
	for k, val := range c.Values {
		out.Values[k] = val
	}
	out.Values[key] = v
	return out
}

// Change is one key that differs between two configs.
type Change struct {
	Key      string
	Old, New any
}

func (ch Change) String() string {
	switch {
	case ch.Old == nil:
		return fmt.Sprintf("+ %s: %v", ch.Key, ch.New)
	case ch.New == nil:
		return fmt.Sprintf("- %s: %v", ch.Key, ch.Old)
	}
	return fmt.Sprintf("~ %s: %v -> %v", ch.Key, ch.Old, ch.New)
}

// Diff lists the keys that changed from old to new, sorted by key.
func Diff(old, new Config) []Change {
	keys := map[string]bool{}
	for k := range old.Values {
		keys[k] = true
	}
	for k := range new.Values {
		keys[k] = true
	}
	var out []Change
	for k := range keys {
		o, n := old.Values[k], new.Values[k]
		if fmt.Sprint(o) != fmt.Sprint(n) {
			out = append(out, Change{Key: k, Old: o, New: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
