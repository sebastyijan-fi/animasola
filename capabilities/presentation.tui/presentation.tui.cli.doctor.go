package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	version "github.com/sebastyijan/animasola/capabilities/core.version"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
)

// RunDoctor handles `animasola doctor`.
func RunDoctor(ctx context.Context) {
	_ = ctx

	configRoot, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("animasola doctor: failed to resolve config dir: %v\n", err)
		os.Exit(1)
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("animasola doctor: failed to resolve executable path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Version: %s\n", version.Current)
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Executable: %s\n", execPath)
	fmt.Printf("Config root: %s\n", configRoot)

	appConfigDir := filepath.Join(configRoot, "animasola")
	fmt.Printf("App config dir: %s\n", appConfigDir)

	torBinary, torErr := tor.EnsureTorBinary(appConfigDir)
	if torErr != nil {
		fmt.Printf("Tor: missing (%v)\n", torErr)
		fmt.Println("Fix: install tor, bundle it with the release, or set ANIMASOLA_TOR_BIN")
	} else {
		fmt.Printf("Tor: ok (%s)\n", torBinary)
	}

	profiles := 0
	if entries, err := os.ReadDir(appConfigDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				profiles++
			}
		}
	}
	fmt.Printf("Profiles: %d\n", profiles)

	if envTor := os.Getenv("ANIMASOLA_TOR_BIN"); envTor != "" {
		fmt.Printf("ANIMASOLA_TOR_BIN: %s\n", envTor)
	}
	if envProxy := os.Getenv("ALL_PROXY"); envProxy != "" {
		fmt.Printf("ALL_PROXY: %s\n", envProxy)
	}
	if envConfig := os.Getenv("XDG_CONFIG_HOME"); envConfig != "" {
		fmt.Printf("XDG_CONFIG_HOME: %s\n", envConfig)
	}

	fmt.Printf("Key storage: %s/<profile>/id_ed25519\n", filepath.Join(configRoot, "animasola"))

	if torErr != nil {
		os.Exit(1)
	}
}
