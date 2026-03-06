package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	version "github.com/sebastyijan/animasola/capabilities/core.version"
	httpcap "github.com/sebastyijan/animasola/capabilities/network.http"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
)

// RunAutoUpdater handles the `animasola update` CLI execution.
// It bypasses the TUI to perform a system-level atomic binary swap over Tor.
func RunAutoUpdater(ctx context.Context) {
	fmt.Println("🚀 Initializing Animasola Secure Auto-Updater...")

	// 1. Resolve Active Executable
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Fatal: Could not resolve active binary path: %v\n", err)
		os.Exit(1)
	}
	execDir := filepath.Dir(execPath)

	// 2. Validate Write Permissions (Crucial if binary is in /usr/local/bin)
	testFile := filepath.Join(execDir, ".animasola.write.test")
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		fmt.Printf("\n❌ Error: Permission Denied to modify '%s'\n", execDir)
		fmt.Println("   You must run the updater with elevated privileges.")
		fmt.Println("   Try again using: sudo animasola update")
		os.Exit(1)
	}
	_ = os.Remove(testFile) // Clean up test

	// 3. Map OS Architecture to GitHub Release Asset
	var assetName string
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			assetName = "animasola-linux-amd64"
		case "arm64":
			assetName = "animasola-linux-arm64"
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			assetName = "animasola-darwin-amd64"
		case "arm64":
			assetName = "animasola-darwin-arm64"
		}
	case "windows":
		fmt.Println("❌ Windows Auto-Update is not supported natively yet.")
		os.Exit(1)
	}

	if assetName == "" {
		fmt.Printf("❌ Unsupported architecture: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		os.Exit(1)
	}

	// 4. Trigger Ephemeral Tor daemon
	fmt.Printf("✅ Pre-flight checks passed! Target platform: %s\n", assetName)
	fmt.Println("\n⏳ Spawning Ephemeral Tor Sandbox for Anonymity...")

	ephemeralDir := filepath.Join(os.TempDir(), "animasola-tmp-updater")
	if err := os.MkdirAll(ephemeralDir, 0700); err != nil {
		fmt.Printf("❌ Failed to create temporary Tor sandbox: %v\n", err)
		os.Exit(1)
	}

	torBinary, err := tor.EnsureTorBinary(ephemeralDir)
	if err != nil {
		fmt.Printf("❌ Failed to extract Tor executable: %v\n", err)
		os.Exit(1)
	}

	torConfig, err := tor.GenerateConfig(ephemeralDir, 4001, 48099)
	if err != nil {
		fmt.Printf("❌ Failed to configure Tor daemon: %v\n", err)
		os.Exit(1)
	}

	torRunner, progressCh, err := tor.Start(ctx, torBinary, torConfig)
	if err != nil {
		fmt.Printf("❌ Failed to spawn Tor engine: %v\n", err)
		os.Exit(1)
	}

	// Wait for Tor to hit 100% Bootstrap
	for msg := range progressCh {
		if msg == "SUCCESS_100" {
			break
		}
	}

	// 5. Inject Ephemeral SOCKS proxy for HTTP Client
	socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
	os.Setenv("ALL_PROXY", socksAddr)

	fmt.Println("🌐 Querying latest version over Tor circuit...")
	release, err := httpcap.FetchLatestRelease()
	if err != nil {
		fmt.Printf("❌ Failed to reach GitHub via Tor: %v\n", err)
		torRunner.Stop()
		os.Exit(1)
	}

	// 6. Compare Version
	if release.TagName == version.Current {
		fmt.Printf("✓ You are already on the latest version (%s). No update required.\n", version.Current)
		torRunner.Stop()
		_ = os.RemoveAll(ephemeralDir)
		os.Exit(0)
	}

	downloadURL := fmt.Sprintf("https://github.com/sebastyijan-fi/animasola/releases/download/%s/%s", release.TagName, assetName)
	fmt.Printf("📦 Downloading encrypted payload: %s\n", release.TagName)

	tmpDownloadedBinary := filepath.Join(execDir, "animasola.new")
	err = httpcap.DownloadReleaseAsset(downloadURL, tmpDownloadedBinary)
	if err != nil {
		fmt.Printf("❌ Downloading binary failed: %v\n", err)
		_ = os.Remove(tmpDownloadedBinary)
		torRunner.Stop()
		os.Exit(1)
	}

	// 7. Make the new binary executable safely before swapping
	if err := os.Chmod(tmpDownloadedBinary, 0755); err != nil {
		fmt.Printf("❌ Failed to set execution permissions: %v\n", err)
		_ = os.Remove(tmpDownloadedBinary)
		torRunner.Stop()
		os.Exit(1)
	}

	// 8. Atomic Swap
	// Using os.Rename guarantees that the `animasola` command will never
	// briefly cease to exist while we write bytes from the internet.
	err = os.Rename(tmpDownloadedBinary, execPath)
	if err != nil {
		fmt.Printf("❌ Atomic OS Swap failed. The old binary is untouched. Err: %v\n", err)
		_ = os.Remove(tmpDownloadedBinary)
		torRunner.Stop()
		os.Exit(1)
	}

	// 9. Graceful Teardown
	torRunner.Stop()
	_ = os.RemoveAll(ephemeralDir) // Wipe Tor's temporary sandbox keys and lockfiles

	fmt.Printf("\n✨ Successfully upgraded payload to %s!\n", release.TagName)
	fmt.Println("Type 'animasola' to launch the secure application.")
	os.Exit(0)
}
