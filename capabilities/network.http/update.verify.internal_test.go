package http

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyReleaseBundleWithKeyAcceptsValidManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "animasola-linux-amd64.tar.gz")
	if err := os.WriteFile(bundlePath, []byte("bundle-bytes"), 0600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	hash, err := sha256File(bundlePath)
	if err != nil {
		t.Fatalf("hash bundle: %v", err)
	}

	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	checksumsData := []byte(fmt.Sprintf("%s  ./%s\n", hash, filepath.Base(bundlePath)))
	if err := os.WriteFile(checksumsPath, checksumsData, 0600); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	signaturePath := filepath.Join(tmpDir, "checksums.txt.sig")
	sig := ed25519.Sign(priv, checksumsData)
	if err := os.WriteFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0600); err != nil {
		t.Fatalf("write signature: %v", err)
	}

	if err := verifyReleaseBundleWithKey(pub, bundlePath, checksumsPath, signaturePath, filepath.Base(bundlePath)); err != nil {
		t.Fatalf("expected valid manifest to verify, got %v", err)
	}
}

func TestVerifyReleaseBundleWithKeyRejectsTamperedBundle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "animasola-linux-amd64.tar.gz")
	if err := os.WriteFile(bundlePath, []byte("bundle-bytes"), 0600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	hash, err := sha256File(bundlePath)
	if err != nil {
		t.Fatalf("hash bundle: %v", err)
	}

	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	checksumsData := []byte(fmt.Sprintf("%s  ./%s\n", hash, filepath.Base(bundlePath)))
	if err := os.WriteFile(checksumsPath, checksumsData, 0600); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	signaturePath := filepath.Join(tmpDir, "checksums.txt.sig")
	sig := ed25519.Sign(priv, checksumsData)
	if err := os.WriteFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0600); err != nil {
		t.Fatalf("write signature: %v", err)
	}

	if err := os.WriteFile(bundlePath, []byte("tampered-bundle-bytes"), 0600); err != nil {
		t.Fatalf("tamper bundle: %v", err)
	}

	if err := verifyReleaseBundleWithKey(pub, bundlePath, checksumsPath, signaturePath, filepath.Base(bundlePath)); err == nil {
		t.Fatalf("expected tampered bundle verification to fail")
	}
}

func TestVerifyExtractedBundleWithKeyAcceptsValidBundle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "animasola"), []byte("binary"), 0600); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bundleDir, "tor"), 0755); err != nil {
		t.Fatalf("mkdir tor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "tor", "tor"), []byte("torbin"), 0600); err != nil {
		t.Fatalf("write tor: %v", err)
	}

	hashes, err := bundleFileHashes(bundleDir)
	if err != nil {
		t.Fatalf("hash bundle: %v", err)
	}
	manifest := fmt.Sprintf("%s  animasola\n%s  tor/tor\n", hashes["animasola"], hashes["tor/tor"])
	if err := os.WriteFile(filepath.Join(bundleDir, BundleManifestName), []byte(manifest), 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	sig := ed25519.Sign(priv, []byte(manifest))
	if err := os.WriteFile(filepath.Join(bundleDir, BundleManifestSigName), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0600); err != nil {
		t.Fatalf("write signature: %v", err)
	}

	if err := verifyExtractedBundleWithKey(pub, bundleDir); err != nil {
		t.Fatalf("expected valid bundle manifest to verify, got %v", err)
	}
}
