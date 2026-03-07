package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	version "github.com/sebastyijan/animasola/capabilities/core.version"
	tui "github.com/sebastyijan/animasola/capabilities/presentation.tui"
)

func init() {
	// Silence verbose quic-go stderr logs that break the Bubbletea TUI frame buffer
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	os.Setenv("QUIC_GO_DISABLE_ECN", "true")
}

func main() {
	ctx := context.Background()

	// Instantiate the root Bubbletea orchestration model in the outermost scope.
	// This ensures that if ANY component in the entire graphical stack crashes,
	// this pointer is still accessible to pull the tor telemetry node from.
	rootModel := tui.NewRootModel(ctx)

	// 0. Ensure Host Terminal Resilience
	// If the application panics inside the Bubbletea alt-screen, the terminal is left permanently broken
	// with a hidden cursor and no echo. This defer catches panics and fires the raw VT100 ANSI sequences
	// to immediately tear down the alt-buffer and re-show the cursor before printing the traceback.
	defer func() {
		if r := recover(); r != nil {
			fmt.Print("\033[?1049l\033[?25h") // Rescue VT100 terminal

			// Phase 20: Broadcast telemetry footprint of the panic anonymously over Tor before dying
			if tel := rootModel.GetTelemetry(); tel != nil {
				tel.RecordEvent(context.Background(), "UI_Panic", 0, map[string]string{
					"error": fmt.Sprintf("%v", r),
					"view":  rootModel.GetCurrentView(),
				}, runtime.GOOS, runtime.GOARCH)

				// Give the async goroutine exactly 250ms to fire the metric into the local
				// Libp2p Tor tunnel before exiting the host OS process entirely.
				time.Sleep(250 * time.Millisecond)
			}

			fmt.Printf("\nFatal Error (Panic Recovery): %v\n", r)
			os.Exit(1)
		}
	}()

	// Phase 14.6: Secure CLI Auto-Updater
	// Bypass the TUI entirely if the user invoked the 'update' command.
	if len(os.Args) > 1 && os.Args[1] == "update" {
		tui.RunAutoUpdater(ctx)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		tui.RunDoctor(ctx)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "verify-bundle" {
		bundleDir := ""
		if len(os.Args) > 2 {
			bundleDir = os.Args[2]
		}
		tui.RunBundleVerifier(ctx, bundleDir)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		tui.RunUninstall(ctx, os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("animasola", tuiVersion())
		return
	}

	if os.Geteuid() == 0 {
		fmt.Println("❌ SECURITY ERROR: Please do not run the Animasola chat client as root.")
		fmt.Println("   Running the application with 'sudo' creates isolated shadow profiles in /root/.config/animasola/.")
		fmt.Println("   Install and run Animasola as your normal user instead.")
		os.Exit(1)
	}

	// Boot the Phase 14 Zero-Argument Root TUI Model
	// This orchestrates the Consent Disclaimer, Profile generation, and deferred setup.
	p := tea.NewProgram(rootModel, tea.WithAltScreen())

	m, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// Phase 19 Hardening: Prevent macOS Tor Zombie processes by forcefully
	// shutting down the background node engines when Bubbletea natively exits.
	if rootModel, ok := m.(*tui.RootModel); ok {
		rootModel.Shutdown()
	}
}

func tuiVersion() string {
	return version.Current
}
