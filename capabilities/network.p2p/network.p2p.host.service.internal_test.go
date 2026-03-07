package p2p

import (
	"strings"
	"testing"
)

func TestLoadBootstrapPeerInfosRejectsMissingPeersByDefault(t *testing.T) {
	_, err := ParseBootstrapPeerInfos(nil, false)
	if err == nil {
		t.Fatalf("expected missing bootstrap peers to fail")
	}
	if !strings.Contains(err.Error(), "/onion3/.../p2p/...") {
		t.Fatalf("expected onion bootstrap guidance, got %v", err)
	}
}

func TestLoadBootstrapPeerInfosAllowsEmptyPeersForBootstrapOperators(t *testing.T) {
	peers, err := ParseBootstrapPeerInfos(nil, true)
	if err != nil {
		t.Fatalf("expected empty bootstrap peers to be allowed, got %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected zero peers, got %d", len(peers))
	}
}

func TestLoadBootstrapPeerInfosRejectsNonOnionPeer(t *testing.T) {
	_, err := ParseBootstrapPeerInfos([]string{"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWBoK4"}, false)
	if err == nil {
		t.Fatalf("expected non-onion bootstrap peer to fail")
	}
}

func TestLoadBootstrapPeerInfosAcceptsOnionPeers(t *testing.T) {
	peers, err := ParseBootstrapPeerInfos(SplitBootstrapPeers(strings.Join([]string{
		"/onion3/vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd:4001/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
		"/onion3/vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd:4002/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	}, ",")), false)
	if err != nil {
		t.Fatalf("expected onion peers to parse, got %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
}

func TestSplitBootstrapPeersIgnoresCommentsAndBlankLines(t *testing.T) {
	peers := SplitBootstrapPeers(`
# comment
/onion3/vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd:4001/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN

/onion3/vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd:4002/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa
`)
	if len(peers) != 2 {
		t.Fatalf("expected 2 parsed peers, got %d", len(peers))
	}
}
