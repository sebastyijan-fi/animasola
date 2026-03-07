package tui

import (
	"context"
	"fmt"
	"os"

	httpcap "github.com/sebastyijan/animasola/capabilities/network.http"
)

// RunBundleVerifier handles the hidden `animasola verify-bundle <dir>` CLI execution.
func RunBundleVerifier(ctx context.Context, bundleDir string) {
	_ = ctx

	if bundleDir == "" {
		fmt.Println("animasola verify-bundle: missing bundle directory")
		os.Exit(1)
	}

	if err := httpcap.VerifyExtractedBundle(bundleDir); err != nil {
		fmt.Printf("animasola verify-bundle: verification failed: %v\n", err)
		os.Exit(1)
	}
}
