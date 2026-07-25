package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain", errors.New("boom"), 1},
		{"usage", usageErr{errors.New("bad")}, 2},
		{"validation", validationErr{errors.New("findings")}, 3},
		{"wrapped usage", fmt.Errorf("ctx: %w", usageErr{errors.New("bad")}), 2},
		{"wrapped validation", fmt.Errorf("ctx: %w", validationErr{errors.New("f")}), 3},
	}
	for _, tc := range cases {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, got)
		}
	}
}
