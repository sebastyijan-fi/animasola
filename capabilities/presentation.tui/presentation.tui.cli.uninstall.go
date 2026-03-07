package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func RunUninstall(_ context.Context, args []string) {
	if len(args) > 0 {
		fmt.Printf("❌ Unknown uninstall option: %s\n", args[0])
		os.Exit(1)
	}

	if os.Geteuid() == 0 {
		fmt.Println("❌ Please do not run 'animasola uninstall' as root.")
		fmt.Println("   Animasola uses a user-local install and user-local data path.")
		os.Exit(1)
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Could not resolve active binary path: %v\n", err)
		os.Exit(1)
	}
	execDir := filepath.Dir(execPath)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("❌ Could not resolve home directory: %v\n", err)
		os.Exit(1)
	}

	installBase := filepath.Join(homeDir, ".local", "share", "animasola")
	installRoot := filepath.Clean(execDir)
	expectedInstallRoot := filepath.Join(installBase, "current")
	if installRoot != expectedInstallRoot {
		fmt.Println("❌ This does not look like a user-local Animasola install.")
		fmt.Println("   Reinstall with: curl -fsSL https://animasola.org/install.sh | bash")
		os.Exit(1)
	}

	binTarget := filepath.Join(homeDir, ".local", "bin", "animasola")
	_ = os.Remove(binTarget)
	_ = os.RemoveAll(filepath.Join(homeDir, ".config", "animasola"))
	_ = os.RemoveAll(installBase)

	fmt.Println("Animasola uninstalled.")
	fmt.Println("Local config and profile data were removed.")
}
