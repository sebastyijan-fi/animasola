package tor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the dynamically generated Tor variables
type Config struct {
	SocksPort         int
	HiddenServiceDir  string
	HiddenServicePort int
	TorrcPath         string
}

// GenerateConfig creates a sandboxed torrc file in the user's config directory.
func GenerateConfig(configDir string, listenPort int, socksPort int) (*Config, error) {
	torDir := filepath.Join(configDir, "tor")
	hsDir := filepath.Join(torDir, "hidden_service")

	// Ensure directories exist with strict permissions
	if err := os.MkdirAll(hsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create hidden service directory: %w", err)
	}

	cfg := &Config{
		SocksPort:         socksPort,
		HiddenServiceDir:  hsDir,
		HiddenServicePort: listenPort,
		TorrcPath:         filepath.Join(torDir, "torrc"),
	}

	// Generate minimal torrc
	torrcContent := fmt.Sprintf(`
# Animasola Auto-Generated Tor Config
SocksPort 127.0.0.1:%d

# Hidden Service matching libp2p incoming traffic
HiddenServiceDir %s
HiddenServicePort %d 127.0.0.1:%d

# Security and isolation
RunAsDaemon 0
DataDirectory %s/data
Log notice stdout
`, cfg.SocksPort, cfg.HiddenServiceDir, cfg.HiddenServicePort, listenPort, torDir)

	if err := os.WriteFile(cfg.TorrcPath, []byte(torrcContent), 0600); err != nil {
		return nil, fmt.Errorf("failed to write torrc: %w", err)
	}

	return cfg, nil
}

// GetOnionAddress reads the generated `.onion` URL from the Tor HiddenServiceDir
// This file is created dynamically by Tor once it successfully bootstraps and publishes the descriptor.
func (c *Config) GetOnionAddress() (string, error) {
	hostnamePath := filepath.Join(c.HiddenServiceDir, "hostname")

	data, err := os.ReadFile(hostnamePath) // #nosec G304 -- Path is strictly confined to ~/.config/animasola/<user>/tor/
	if err != nil {
		return "", fmt.Errorf("hidden service hostname file not found: %w", err)
	}

	// The file contains the address and a trailing newline
	address := string(data)
	// Strip any whitespace or newlines
	for len(address) > 0 && (address[len(address)-1] == '\n' || address[len(address)-1] == '\r' || address[len(address)-1] == ' ') {
		address = address[:len(address)-1]
	}

	address = strings.TrimSuffix(address, ".onion")

	return address, nil
}
