//go:build linux
// +build linux

package tor

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr configures Linux-specific process attributes.
// The Pdeathsig ensures that if the parent process (Animasola) dies ungracefully,
// the kernel will instantly SIGKILL the Tor child process, preventing zombie daemons.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
}
