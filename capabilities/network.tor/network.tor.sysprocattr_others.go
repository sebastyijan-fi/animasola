//go:build !linux
// +build !linux

package tor

import (
	"os/exec"
)

// setSysProcAttr is empty for non-Linux operating systems
// because syscall.SysProcAttr.Pdeathsig is a Linux-only kernel feature.
// On macOS/Windows, we rely on Bubbletea graceful shutdowns and context cancellations.
func setSysProcAttr(cmd *exec.Cmd) {
	// No-op
}
