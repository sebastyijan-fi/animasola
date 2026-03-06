package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// Keys represent an loaded Ed25519 keypair for the application.
type Keys struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ssh.PublicKey
}

// Fingerprint returns the standard SSH SHA256 fingerprint for the public key.
func (k *Keys) Fingerprint() string {
	return ssh.FingerprintSHA256(k.PublicKey)
}

// HasKey returns true if the application-specific Ed25519 key exists.
func HasKey(username string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	keyPath := filepath.Join(homeDir, ".config", "animasola", username, "id_ed25519")
	_, err = os.Stat(keyPath)
	return err == nil
}

// GetOrGenerateKey ensures an application-specific Ed25519 keypair exists
// in ~/.config/animasola/<username>/id_ed25519. It will NOT read or touch ~/.ssh.
func GetOrGenerateKey(username string) (*Keys, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	configDir := filepath.Join(homeDir, ".config", "animasola", username)
	keyPath := filepath.Join(configDir, "id_ed25519")

	// Ensure the config directory exists
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create config dir: %w", err)
	}

	// Try to read existing key
	if _, err := os.Stat(keyPath); err == nil {
		return loadKey(keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to access key file: %w", err)
	}

	// Key does not exist; generate one.
	return generateAndSaveKey(keyPath)
}

func generateAndSaveKey(path string) (*Keys, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	// Convert to OpenSSH Private Key PEM format
	privKeyData, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	pemBlock := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: privKeyData.Bytes,
	}

	// Save private key with strict permissions
	// O_EXCL ensures we don't accidentally overwrite an existing file
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- Path is strictly confined to ~/.config/animasola/
	if err != nil {
		return nil, fmt.Errorf("failed to create private key file: %w", err)
	}
	defer file.Close()

	if err := pem.Encode(file, pemBlock); err != nil {
		return nil, fmt.Errorf("failed to write pem block: %w", err)
	}

	// Convert public key to ssh.PublicKey interface
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	// Save the public key alongside it (like id_ed25519.pub)
	pubPath := path + ".pub"
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)
	if err := os.WriteFile(pubPath, pubBytes, 0600); err != nil { // #nosec G304 -- Path is safe. G306 -- Reduced to 0600.
		return nil, fmt.Errorf("failed to save public key file: %w", err)
	}

	return &Keys{
		PrivateKey: priv,
		PublicKey:  sshPub,
	}, nil
}

func loadKey(path string) (*Keys, error) {
	keyBytes, err := os.ReadFile(path) // #nosec G304 -- Path is derived securely upstream.
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	privateKey, err := ssh.ParseRawPrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	ed25519Priv, ok := privateKey.(*ed25519.PrivateKey)
	if !ok {
		// Just in case we expand supported key types later
		return nil, errors.New("loaded key is not an ed25519 key")
	}

	// Derive the public key natively
	pub := ed25519Priv.Public().(ed25519.PublicKey)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("failed to parse derived public key: %w", err)
	}

	return &Keys{
		PrivateKey: *ed25519Priv,
		PublicKey:  sshPub,
	}, nil
}
