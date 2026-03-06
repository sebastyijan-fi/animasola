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
	// Capture stderr to file for debugging fatal Tor panics
	errFile, _ := os.OpenFile("/tmp/tor_stderr.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	cmd.Stderr = errFile

	// Prevent zombie Tor daemons across OS platforms
	// See sysprocattr_linux.go and sysprocattr_others.go
	setSysProcAttr(cmd)

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

		// DEBUG: dump all of Tor's stdout
		outLog, _ := os.OpenFile("/tmp/tor_stdout.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		defer outLog.Close()

		scanner := bufio.NewScanner(stdout)
		bootstrapped := false
		for scanner.Scan() {
			line := scanner.Text()
			outLog.WriteString(line + "\n")

			if !bootstrapped {
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
					progressCh <- "SUCCESS_100"
					bootstrapped = true
					// DO NOT return here! If we stop reading stdout, Tor's pipe fills up
					// and Linux will send SIGPIPE, instantly killing the Tor anonymity engine!
				}
			}
		}
	}()

	return r, progressCh, nil

}

func (r *Runner) Stop() {
	r.cancel() // This kills the active os/exec command spawned with CommandContext
	r.wg.Wait()
}
