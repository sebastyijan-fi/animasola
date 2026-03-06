package tor

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnsureTorBinary checks if the Tor executable exists in the config directory.
// If it does not, it downloads the official pre-compiled binary for the host OS.
func EnsureTorBinary(configDir string) (string, error) {
	torDir := filepath.Join(configDir, "tor")
	if err := os.MkdirAll(torDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create tor directory: %w", err)
	}

	binaryName := "tor"
	if runtime.GOOS == "windows" {
		binaryName = "tor.exe"
	}
	binaryPath := filepath.Join(torDir, binaryName)

	// Check if already downloaded
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	fmt.Printf("\n[Tor Manager] Downloading official Tor binary for %s/%s. This may take a minute...\n", runtime.GOOS, runtime.GOARCH)

	url, err := getDownloadURL()
	if err != nil {
		return "", err
	}

	if err := downloadAndExtract(url, torDir, binaryPath); err != nil {
		return "", fmt.Errorf("failed to download Tor: %w", err)
	}

	// Ensure the binary is executable
	if runtime.GOOS != "windows" {
		if err := os.Chmod(binaryPath, 0700); err != nil { // #nosec G302 -- Tor requires exactly 0700 execution permissions
			return "", fmt.Errorf("failed to make Tor executable: %w", err)
		}
	}

	fmt.Println("[Tor Manager] Tor downloaded successfully!")
	return binaryPath, nil
}

func getDownloadURL() (string, error) {
	// Hardcoding standard Tor Expert Bundle URLs for brevity in this prototype.
	// In production, these should be dynamically scraped from torproject.org/download/tor/
	// or hosted on a reliable CDN mirror to prevent direct IP blocks.
	version := "14.0.6" // Latest Tor Browser stable release engine
	basePath := "https://archive.torproject.org/tor-package-archive/torbrowser"

	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH == "amd64" {
			return fmt.Sprintf("%s/%s/tor-expert-bundle-linux-x86_64-%s.tar.gz", basePath, version, version), nil
		}
		if runtime.GOARCH == "arm64" {
			return fmt.Sprintf("%s/%s/tor-expert-bundle-linux-aarch64-%s.tar.gz", basePath, version, version), nil
		}
	case "darwin":
		if runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64" {
			return fmt.Sprintf("%s/%s/tor-expert-bundle-macos-%s.tar.gz", basePath, version, version), nil
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			return fmt.Sprintf("%s/%s/tor-expert-bundle-windows-x86_64-%s.tar.gz", basePath, version, version), nil
		}
	}

	return "", fmt.Errorf("unsupported OS/Arch combination for automated Tor download: %s/%s", runtime.GOOS, runtime.GOARCH)
}

func downloadAndExtract(url, destDir, targetBinaryPath string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) // #nosec G107 - URL is strictly hardcoded in the architecture map
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP status: %s", resp.Status)
	}

	// The Expert Bundle is a .tar.gz
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// The expert bundle structure: `tor/tor`
		// Extract the main executable binary, avoiding `debug/tor`
		if header.Name == "tor/tor" || header.Name == "tor/tor.exe" || header.Name == "tor.exe" {
			if err := extractFile(tr, targetBinaryPath, 0700); err != nil {
				return err
			}
			continue
		}

		// Extract any necessary shared libraries (.so / .dll)
		if strings.HasPrefix(header.Name, "tor/") && (strings.HasSuffix(header.Name, ".so") || strings.HasSuffix(header.Name, ".so.3") || strings.HasSuffix(header.Name, ".so.6") || strings.HasSuffix(header.Name, ".so.7") || strings.HasSuffix(header.Name, ".dll")) {
			libPath := filepath.Join(destDir, filepath.Base(header.Name))
			if err := extractFile(tr, libPath, 0644); err != nil {
				return err
			}
		}
	}

	// Double check we actually found the binary
	if _, err := os.Stat(targetBinaryPath); err != nil {
		return fmt.Errorf("could not find 'tor' executable inside the downloaded archive")
	}

	return nil
}

func extractFile(r io.Reader, destPath string, mode os.FileMode) error {
	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode) // #nosec G304 -- destPath is statically constrained to the Tor AppData dir
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, r); err != nil {
		return err
	}
	return nil
}
