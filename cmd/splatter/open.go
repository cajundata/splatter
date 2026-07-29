package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openInBrowser launches the OS default browser on path. This is the
// codebase's single permitted runtime.GOOS branch (spec §8); no other
// GOOS conditional may exist anywhere.
func openInBrowser(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default:
		return fmt.Errorf("browser-open not supported on %s", runtime.GOOS)
	}
}
