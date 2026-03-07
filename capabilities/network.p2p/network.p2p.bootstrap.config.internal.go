package p2p

import (
	_ "embed"
	"strings"
)

//go:embed bootstrap_peers.txt
var embeddedBootstrapPeersRaw string

func DefaultBootstrapPeers() []string {
	return SplitBootstrapPeers(embeddedBootstrapPeersRaw)
}

func ResolveBootstrapPeers(envOverride string) []string {
	if override := strings.TrimSpace(envOverride); override != "" {
		return SplitBootstrapPeers(override)
	}
	return DefaultBootstrapPeers()
}

func BootstrapPeerSource(envOverride string) string {
	if strings.TrimSpace(envOverride) != "" {
		return "env"
	}
	if len(DefaultBootstrapPeers()) > 0 {
		return "embedded"
	}
	return "missing"
}
