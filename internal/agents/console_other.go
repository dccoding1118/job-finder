//go:build !windows

package agents

import "os/exec"

// hideConsole is a no-op away from Windows: no other supported platform gives a
// child process a window that would need hiding.
func hideConsole(*exec.Cmd) {}
