package main

import (
	"errors"
	"fmt"
	"os"
)

// validationErr forces exit code 3. Command implementations wrap
// validation failures in it; runtime errors pass through as plain errors.
type validationErr struct{ err error }

func (v validationErr) Error() string { return v.err.Error() }
func (v validationErr) Unwrap() error { return v.err }

// usageErr forces exit code 2 for argument errors detected inside RunE.
type usageErr struct{ err error }

func (u usageErr) Error() string { return u.err.Error() }
func (u usageErr) Unwrap() error { return u.err }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var v validationErr
	if errors.As(err, &v) {
		return 3
	}
	var u usageErr
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "splatter:", err)
		code := exitCode(err)
		// Unknown subcommand: cobra returns a plain error from Execute
		// before any RunE runs; Find failing identifies it as usage.
		if code == 1 {
			if _, _, findErr := root.Find(os.Args[1:]); findErr != nil {
				code = 2
			}
		}
		os.Exit(code)
	}
}
