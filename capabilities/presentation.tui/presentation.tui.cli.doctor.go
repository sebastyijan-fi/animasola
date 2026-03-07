package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	version "github.com/sebastyijan/animasola/capabilities/core.version"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
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

	hasFailure := false

	torBinary, torErr := tor.EnsureTorBinary(appConfigDir)
	if torErr != nil {
		fmt.Printf("Tor: missing (%v)\n", torErr)
		fmt.Println("Fix: install tor, bundle it with the release, or set ANIMASOLA_TOR_BIN")
		hasFailure = true
	} else {
		fmt.Printf("Tor: ok (%s)\n", torBinary)
	}

	allProxy := strings.TrimSpace(os.Getenv("ALL_PROXY"))
	if allProxy == "" {
		fmt.Println("Tor SOCKS proxy: missing (set ALL_PROXY to the local Tor SOCKS endpoint, for example socks5://127.0.0.1:45000)")
		hasFailure = true
	} else if proxyURL, err := url.Parse(allProxy); err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		fmt.Printf("Tor SOCKS proxy: invalid (%s)\n", allProxy)
		hasFailure = true
	} else if proxyURL.Scheme != "socks5" && proxyURL.Scheme != "socks5h" {
		fmt.Printf("Tor SOCKS proxy: invalid scheme (%s)\n", proxyURL.Scheme)
		hasFailure = true
	} else {
		fmt.Printf("Tor SOCKS proxy: ok (%s)\n", allProxy)
	}

	rawBootstrap := strings.TrimSpace(os.Getenv("ANIMASOLA_BOOTSTRAP_PEERS"))
	bootstrapSource := p2p.BootstrapPeerSource(rawBootstrap)
	bootstrapCount := 0
	switch {
	case bootstrapSource == "missing":
		fmt.Println("Bootstrap peers: missing (release bootstrap peers have not been embedded and ANIMASOLA_BOOTSTRAP_PEERS is unset)")
		hasFailure = true
	default:
		peers, err := p2p.ParseBootstrapPeerInfos(p2p.ResolveBootstrapPeers(rawBootstrap), false)
		if err != nil {
			fmt.Printf("Bootstrap peers: invalid (%v)\n", err)
			hasFailure = true
		} else {
			bootstrapCount = len(peers)
			fmt.Printf("Bootstrap peers: ok (%d onion peer(s), source=%s)\n", bootstrapCount, bootstrapSource)
		}
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
	if allProxy != "" {
		fmt.Printf("ALL_PROXY: %s\n", allProxy)
	}
	if envBootstrap := os.Getenv("ANIMASOLA_BOOTSTRAP_PEERS"); envBootstrap != "" {
		fmt.Printf("ANIMASOLA_BOOTSTRAP_PEERS: %s\n", envBootstrap)
	} else if bootstrapSource == "embedded" {
		fmt.Println("ANIMASOLA_BOOTSTRAP_PEERS: using embedded release bootstrap list")
	}
	if envConfig := os.Getenv("XDG_CONFIG_HOME"); envConfig != "" {
		fmt.Printf("XDG_CONFIG_HOME: %s\n", envConfig)
	}

	fmt.Printf("Key storage: %s/<profile>/id_ed25519\n", filepath.Join(configRoot, "animasola"))

	if hasFailure {
		os.Exit(1)
	}
}
