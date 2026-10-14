package config

import (
	"strings"
	"testing"
)

func TestVersionIgnoresKeyOrderAndName(t *testing.T) {
	a := Config{Name: "a", Values: map[string]any{"x": 1.0, "y": "z"}}
	b := Config{Name: "b", Values: map[string]any{"y": "z", "x": 1.0}}
	if a.Version() != b.Version() {
		t.Fatalf("versions differ: %s vs %s", a.Version(), b.Version())
	}
	if a.Version() == a.With("x", 2.0).Version() {
		t.Fatal("changing a value should change the version")
	}
}

func TestWithDoesNotMutate(t *testing.T) {
	a := Config{Values: map[string]any{"x": 1}}
	_ = a.With("x", 2)
	if a.Int("x", 0) != 1 {
		t.Fatal("With mutated the original")
	}
}

func TestDiff(t *testing.T) {
	old := Config{Values: map[string]any{"a": 1, "b": 2, "c": 3}}
	new := Config{Values: map[string]any{"a": 1, "b": 5, "d": 4}}
	got := Diff(old, new)
	want := []string{"~ b: 2 -> 5", "- c: 3", "+ d: 4"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("change %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Name: "ok", Values: map[string]any{"upstream_timeout_ms": 500.0, "retries": 1.0}}
	if err := DefaultSchema.Validate(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := Config{Name: "bad", Values: map[string]any{"upstream_timeout_ms": 0.0, "retries": "two"}}
	err := DefaultSchema.Validate(bad)
	if err == nil {
		t.Fatal("bad config accepted")
	}
	for _, s := range []string{"upstream_timeout_ms=0 out of range", "retries must be a number"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q missing %q", err, s)
		}
	}
	if err := DefaultSchema.Validate(Config{Values: map[string]any{}}); err == nil {
		t.Fatal("missing required keys accepted")
	}
}
