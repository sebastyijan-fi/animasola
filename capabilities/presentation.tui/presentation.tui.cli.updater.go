package tui

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	version "github.com/sebastyijan/animasola/capabilities/core.version"
	httpcap "github.com/sebastyijan/animasola/capabilities/network.http"
)

// RunAutoUpdater handles the `animasola update` CLI execution.
// It bypasses the TUI to perform a system-level atomic binary swap.
func RunAutoUpdater(ctx context.Context) {
	_ = ctx
	fmt.Println("🚀 Initializing Animasola Secure Auto-Updater...")

	// 1. Resolve Active Executable
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Fatal: Could not resolve active binary path: %v\n", err)
		os.Exit(1)
	}
	execDir := filepath.Dir(execPath)

	// 2. Validate Write Permissions for the bundled user-local install root.
	testFile := filepath.Join(execDir, ".animasola.write.test")
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		fmt.Printf("\n❌ Error: Permission Denied to modify '%s'\n", execDir)
		fmt.Println("   This install is not writable by the current user.")
		fmt.Println("   Reinstall Animasola using the user-local installer:")
		fmt.Println("   curl -fsSL https://animasola.org/install.sh | bash")
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
	bundleAssetName := assetName + ".tar.gz"
	checksumsAssetName := "checksums.txt"
	signatureAssetName := "checksums.txt.sig"

	if !httpcap.ReleaseVerificationConfigured() {
		fmt.Println("❌ Verified updates are not configured in this build.")
		fmt.Println("   Refusing to install an unverified release payload.")
		os.Exit(1)
	}

	fmt.Printf("✅ Pre-flight checks passed! Target platform: %s\n", assetName)
	fmt.Println("\n🌐 Querying latest version...")
	release, err := httpcap.FetchLatestRelease()
	if err != nil {
		fmt.Printf("❌ Failed to reach GitHub: %v\n", err)
		os.Exit(1)
	}

	// 6. Compare Version
	if release.TagName == version.Current {
		fmt.Printf("✓ You are already on the latest version (%s). No update required.\n", version.Current)
		os.Exit(0)
	}

	downloadURL := httpcap.ReleaseAssetURL(release.TagName, bundleAssetName)
	checksumsURL := httpcap.ReleaseAssetURL(release.TagName, checksumsAssetName)
	signatureURL := httpcap.ReleaseAssetURL(release.TagName, signatureAssetName)
	fmt.Printf("📦 Downloading encrypted payload: %s\n", release.TagName)

	tmpDir, err := os.MkdirTemp("", "animasola-updater-*")
	if err != nil {
		fmt.Printf("❌ Failed to allocate temporary update directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	tmpDownloadedBundle := filepath.Join(tmpDir, bundleAssetName)
	err = httpcap.DownloadReleaseAsset(downloadURL, tmpDownloadedBundle)
	if err != nil {
		fmt.Printf("❌ Downloading binary failed: %v\n", err)
		os.Exit(1)
	}

	tmpChecksums := filepath.Join(tmpDir, checksumsAssetName)
	if err := httpcap.DownloadReleaseAsset(checksumsURL, tmpChecksums); err != nil {
		fmt.Printf("❌ Downloading release manifest failed: %v\n", err)
		os.Exit(1)
	}

	tmpSignature := filepath.Join(tmpDir, signatureAssetName)
	if err := httpcap.DownloadReleaseAsset(signatureURL, tmpSignature); err != nil {
		fmt.Printf("❌ Downloading release signature failed: %v\n", err)
		os.Exit(1)
	}

	if err := httpcap.VerifyReleaseBundle(tmpDownloadedBundle, tmpChecksums, tmpSignature); err != nil {
		fmt.Printf("❌ Release verification failed: %v\n", err)
		os.Exit(1)
	}

	bundleDir, err := extractBundle(tmpDownloadedBundle, tmpDir)
	if err != nil {
		fmt.Printf("❌ Failed to extract release bundle: %v\n", err)
		os.Exit(1)
	}

	if isBundledInstall(execDir) {
		if err := replaceInstallRoot(execDir, bundleDir); err != nil {
			fmt.Printf("❌ Failed to replace bundled install: %v\n", err)
			os.Exit(1)
		}
	} else {
		tmpDownloadedBinary := filepath.Join(execDir, "animasola.new")
		if err := copyFile(filepath.Join(bundleDir, "animasola"), tmpDownloadedBinary, 0755); err != nil {
			fmt.Printf("❌ Failed to stage updated binary: %v\n", err)
			os.Exit(1)
		}
		if err := os.Rename(tmpDownloadedBinary, execPath); err != nil {
			fmt.Printf("❌ Atomic OS Swap failed. The old binary is untouched. Err: %v\n", err)
			_ = os.Remove(tmpDownloadedBinary)
			os.Exit(1)
		}
	}

	fmt.Printf("\n✨ Successfully upgraded payload to %s!\n", release.TagName)
	fmt.Println("Type 'animasola' to launch the secure application.")
	os.Exit(0)
}

func isBundledInstall(execDir string) bool {
	if _, err := os.Stat(filepath.Join(execDir, "tor")); err == nil {
		return true
	}
	if strings.Contains(execDir, string(filepath.Separator)+"animasola") {
		return true
	}
	return false
}

func replaceInstallRoot(targetDir, bundleDir string) error {
	parentDir := filepath.Dir(targetDir)
	stagingDir := filepath.Join(parentDir, ".animasola-update")
	backupDir := filepath.Join(parentDir, ".animasola-backup")

	_ = os.RemoveAll(stagingDir)
	_ = os.RemoveAll(backupDir)

	if err := copyDir(bundleDir, stagingDir); err != nil {
		return err
	}
	if err := os.Rename(targetDir, backupDir); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, targetDir); err != nil {
		_ = os.Rename(backupDir, targetDir)
		return err
	}
	_ = os.RemoveAll(backupDir)
	return nil
}

func extractBundle(archivePath, workDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var rootDir string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		targetPath := filepath.Join(workDir, header.Name)
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, filepath.Clean(workDir)+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing to extract outside temp dir: %s", header.Name)
		}

		if rootDir == "" {
			parts := strings.Split(header.Name, string(filepath.Separator))
			if len(parts) > 0 {
				rootDir = filepath.Join(workDir, parts[0])
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTarget, 0755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0755); err != nil {
				return "", err
			}
			mode := os.FileMode(header.Mode)
			if mode == 0 {
				mode = 0644
			}
			out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		}
	}

	if rootDir == "" {
		return "", fmt.Errorf("release bundle was empty")
	}
	return rootDir, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		return copyFile(path, targetPath, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
