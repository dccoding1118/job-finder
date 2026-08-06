//go:build windows

package agents

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: run a console application without giving
// it a console of its own. It is not exported by the standard syscall package.
const createNoWindow = 0x08000000

// hideConsole keeps an Agent invocation from putting a window on the desktop.
//
// The resident service is a GUI-subsystem executable with no console of its
// own, so Windows allocates — and shows — a fresh console for every console
// child it starts. An npm-installed Agent CLI is a .cmd shim run through the
// command interpreter, which is exactly such a child, so without this every
// screening, scoring and letter call would flash a window at the user.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
