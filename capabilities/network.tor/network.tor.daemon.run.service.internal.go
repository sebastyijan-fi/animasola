package tor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type Runner struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func Start(ctx context.Context, binaryPath string, cfg *Config) (*Runner, <-chan string, error) {
	runnerCtx, cancel := context.WithCancel(ctx)
	r := &Runner{
		ctx:    runnerCtx,
		cancel: cancel,
	}

	// Build the command
	cmd := exec.CommandContext(r.ctx, binaryPath, "-f", cfg.TorrcPath) // #nosec G204 -- Binary path is strictly bound to Animasola AppData dir
	cmd.Stderr = os.Stderr

	// Linux Kernel Hack: Pdeathsig guarantees that if the parent (Animasola) dies ungracefully
	// (e.g. kill -9), the kernel will immediately deliver SIGKILL to the Tor child process,
	// preventing a zombie daemon from haunting the background and blocking future boots.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	// Inject Library paths so Tor can find libssl.so and libevent.so
	torDir := filepath.Dir(binaryPath)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+torDir, "DYLD_LIBRARY_PATH="+torDir)

	// Create pipes to capture the Tor startup log so we can verify it bound the SOCKS port
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	r.cmd = cmd

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start Tor daemon: %w", err)
	}

	// Read Tor logs asynchronously and stream them back to the caller
	progressCh := make(chan string)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(progressCh)

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()

			// Only forward notice logs regarding bootstrap to the UI
			if strings.Contains(line, "Bootstrapped") {
				// Strip the timestamp and [notice] prefix for cleaner UI
				parts := strings.Split(line, "[notice]")
				if len(parts) > 1 {
					progressCh <- strings.TrimSpace(parts[1])
				} else {
					progressCh <- line
				}
			}

			if strings.Contains(line, "Bootstrapped 100% (done)") {
				return // We don't need to parse logs after bootstrap
			}
		}
	}()

	return r, progressCh, nil

}

func (r *Runner) Stop() {
	r.cancel() // This kills the active os/exec command spawned with CommandContext
	r.wg.Wait()
}
