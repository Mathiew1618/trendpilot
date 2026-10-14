package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trendpilot.env")
	os.WriteFile(p, []byte("# comment\nA=1\nB=US          # country\nC=\"x # y\"\nD='q'\nE=\nbad line\n"), 0o600)
	got := readEnvFile(p)
	want := map[string]string{"A": "1", "B": "US", "C": "x # y", "D": "q", "E": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra keys: %v", got)
	}
}
