package main

import (
	"context"
	"fmt"
	"os"

	tui "github.com/sebastyijan/animasola/capabilities/presentation.tui"
)

func main() {
	// 0. Ensure Host Terminal Resilience
	// If the application panics inside the Bubbletea alt-screen, the terminal is left permanently broken
	// with a hidden cursor and no echo. This defer catches panics and fires the raw VT100 ANSI sequences
	// to immediately tear down the alt-buffer and re-show the cursor before printing the traceback.
	defer func() {
		if r := recover(); r != nil {
			fmt.Print("\033[?1049l\033[?25h")
			fmt.Printf("\nFatal Error (Panic Recovery): %v\n", r)
			os.Exit(1)
		}
	}()

	ctx := context.Background()

	// Phase 14.6: Secure CLI Auto-Updater
	// Bypass the TUI entirely if the user invoked the 'update' command.
	if len(os.Args) > 1 && os.Args[1] == "update" {
		tui.RunAutoUpdater(ctx)
		return
	}

	// Boot the Phase 14 Zero-Argument Root TUI Model
	// This orchestrates the Consent Disclaimer, Profile generation, and deferred setup.
	p := tui.Start(ctx)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
