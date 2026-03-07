package http

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed release_public_key.txt
var embeddedReleasePublicKeyRaw string

const (
	BundleManifestName    = "bundle-manifest.txt"
	BundleManifestSigName = "bundle-manifest.txt.sig"
)

func ReleaseVerificationConfigured() bool {
	_, err := releasePublicKey()
	return err == nil
}

func VerifyReleaseBundle(bundlePath, checksumsPath, signaturePath string) error {
	pub, err := releasePublicKey()
	if err != nil {
		return fmt.Errorf("release verification unavailable: %w", err)
	}
	return verifyReleaseBundleWithKey(pub, bundlePath, checksumsPath, signaturePath, filepath.Base(bundlePath))
}

func VerifyExtractedBundle(bundleDir string) error {
	pub, err := releasePublicKey()
	if err != nil {
		return fmt.Errorf("bundle verification unavailable: %w", err)
	}
	return verifyExtractedBundleWithKey(pub, bundleDir)
}

func verifyReleaseBundleWithKey(pub ed25519.PublicKey, bundlePath, checksumsPath, signaturePath, assetName string) error {
	checksumsData, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("read checksums manifest: %w", err)
	}

	signature, err := parseBase64PayloadFile(signaturePath, "release signature")
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, checksumsData, signature) {
		return fmt.Errorf("release manifest signature verification failed")
	}

	expectedHash, err := expectedAssetHash(checksumsData, assetName)
	if err != nil {
		return err
	}

	actualHash, err := sha256File(bundlePath)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return fmt.Errorf("release bundle checksum mismatch for %s", assetName)
	}
	return nil
}

func releasePublicKey() (ed25519.PublicKey, error) {
	keyBytes, err := parseBase64Payload(embeddedReleasePublicKeyRaw, "release public key")
	if err != nil {
		return nil, err
	}
	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release public key has invalid length %d", len(keyBytes))
	}
	return ed25519.PublicKey(keyBytes), nil
}

func parseBase64PayloadFile(path, label string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return parseBase64Payload(string(data), label)
}

func parseBase64Payload(raw, label string) ([]byte, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", label, err)
		}
		return decoded, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", label, err)
	}
	return nil, fmt.Errorf("%s not configured", label)
}

func expectedAssetHash(checksumsData []byte, assetName string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(checksumsData)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "./")
		if filepath.Base(name) == assetName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan checksums manifest: %w", err)
	}
	return "", fmt.Errorf("release manifest missing checksum for %s", assetName)
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bundle for checksum: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func verifyExtractedBundleWithKey(pub ed25519.PublicKey, bundleDir string) error {
	manifestPath := filepath.Join(bundleDir, BundleManifestName)
	signaturePath := filepath.Join(bundleDir, BundleManifestSigName)

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read bundle manifest: %w", err)
	}

	signature, err := parseBase64PayloadFile(signaturePath, "bundle manifest signature")
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, manifestData, signature) {
		return fmt.Errorf("bundle manifest signature verification failed")
	}

	expected, err := parseManifestEntries(manifestData)
	if err != nil {
		return err
	}
	actual, err := bundleFileHashes(bundleDir)
	if err != nil {
		return err
	}

	if len(expected) != len(actual) {
		return fmt.Errorf("bundle manifest file count mismatch")
	}

	for rel, expectedHash := range expected {
		actualHash, ok := actual[rel]
		if !ok {
			return fmt.Errorf("bundle manifest missing extracted file %s", rel)
		}
		if actualHash != expectedHash {
			return fmt.Errorf("bundle file checksum mismatch for %s", rel)
		}
	}

	return nil
}

func parseManifestEntries(data []byte) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid manifest line %q", line)
		}
		rel := filepath.ToSlash(strings.TrimPrefix(fields[len(fields)-1], "./"))
		result[rel] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan manifest: %w", err)
	}
	return result, nil
}

func bundleFileHashes(bundleDir string) (map[string]string, error) {
	type entry struct {
		rel string
		abs string
	}
	var files []entry

	err := filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == BundleManifestName || rel == BundleManifestSigName {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported symlink in bundle: %s", rel)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in bundle: %s", rel)
		}
		files = append(files, entry{rel: rel, abs: path})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	result := make(map[string]string, len(files))
	for _, file := range files {
		hash, err := sha256File(file.abs)
		if err != nil {
			return nil, err
		}
		result[file.rel] = hash
	}
	return result, nil
}
