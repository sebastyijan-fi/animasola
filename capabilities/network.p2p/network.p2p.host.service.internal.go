package p2p

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	transport "github.com/libp2p/go-libp2p/core/transport"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	telemetry "github.com/sebastyijan/animasola/capabilities/network.telemetry"
)

type Config struct {
	Identity            *keys.Keys
	OnionAddress        string
	ListenPort          int
	SocksProxy          string
	BootstrapPeers      []string
	AllowEmptyBootstrap bool
}

type Node struct {
	Host      host.Host
	PubSub    *pubsub.PubSub
	Telemetry *telemetry.Service
	Identity  *keys.Keys
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	status    NodeStatus
}

type NodeStatus struct {
	StartedAt             time.Time
	FirstConnectionAt     time.Time
	FirstBootstrapPeerAt  time.Time
	LastBootstrapAttempt  time.Time
	LastBootstrapSuccess  time.Time
	ConnectedPeers        int
	BootstrapPeerCount    int
	AutoRelayEnabled      bool
	StaticRelayCandidates int
}

// NewNode initializes a libp2p host using the provided isolated Identity Key,
// and strictly advertises its Tor Onion v3 routing address to the DHT.
func NewNode(cfg Config) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if cfg.Identity == nil {
		cancel()
		return nil, fmt.Errorf("missing p2p identity")
	}
	if strings.TrimSpace(cfg.OnionAddress) == "" {
		cancel()
		return nil, fmt.Errorf("missing onion address")
	}
	if cfg.ListenPort <= 0 {
		cancel()
		return nil, fmt.Errorf("invalid listen port: %d", cfg.ListenPort)
	}
	if strings.TrimSpace(cfg.SocksProxy) == "" {
		cancel()
		return nil, fmt.Errorf("missing Tor SOCKS proxy for outbound onion dialing")
	}

	bootstrapPeers, err := ParseBootstrapPeerInfos(cfg.BootstrapPeers, cfg.AllowEmptyBootstrap)
	if err != nil {
		cancel()
		return nil, err
	}

	// Convert standard crypto/ed25519 to libp2p crypto
	p2pPrivKey, err := crypto.UnmarshalEd25519PrivateKey(cfg.Identity.PrivateKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to convert identity key to libp2p key: %w", err)
	}

	// Tell libp2p to advertise the Tor Hidden Service onion address so global peers can find us
	onionMultiaddr := fmt.Sprintf("/onion3/%s:%d", cfg.OnionAddress, cfg.ListenPort)
	advertiseAddr, err := multiaddr.NewMultiaddr(onionMultiaddr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to parse onion multiaddr: %w", err)
	}

	// Set up a connection manager to prevent File Descriptor (FD) exhaustion limits
	cm, err := connmgr.NewConnManager(
		10,  // Low water mark
		100, // High water mark (prune back to 10 when we reach 100)
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create connection manager: %w", err)
	}

	// Create a new libp2p Host that listens on a random TCP port locally,
	// but routes all outgoing dials via the Tor SOCKS5 proxy,
	// and ONLY tells the world its .onion address.
	h, err := libp2p.New(
		libp2p.Identity(p2pPrivKey),
		libp2p.NoTransports,                     // Disable all default transports (including QUIC)
		libp2p.Transport(tcp.NewTCPTransport),   // Local loopback listener for the Tor hidden-service forward
		libp2p.Transport(func(upgrader transport.Upgrader, rcmgr network.ResourceManager) (transport.Transport, error) {
			return NewTorOnionTransport(upgrader, rcmgr, cfg.SocksProxy)
		}),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", cfg.ListenPort)), // Bind specifically to what torrc expects
		libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
			// Strip all local IP leakage and only present the Tor endpoint
			return []multiaddr.Multiaddr{advertiseAddr}
		}),
		libp2p.ConnectionManager(cm),
		libp2p.ForceReachabilityPrivate(),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Initialize Kademlia DHT
	// Enforce ModeServer so Animasola nodes actively cache Tor routing records
	// instead of defaulting to ModeClient due to the SOCKS5 proxy NAT detection.
	kDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	// Bootstrap the DHT
	if err = kDHT.Bootstrap(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	routingDiscovery := routing.NewRoutingDiscovery(kDHT)

	// Upgrade to GossipSub for scalable global routing.
	// We bind the Kademlia DHT routing discovery so it knows how to find peers for topics.
	ps, err := pubsub.NewGossipSub(ctx, h, pubsub.WithDiscovery(routingDiscovery))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create pubsub router: %w", err)
	}

	// Phase 20: Boot the Anonymous Distributed Telemetry Engine
	// This sits firmly inside the Libp2p/Tor SOCKS5 circuit logic.
	telSvc, err := telemetry.NewService(ps)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to init telemetry: %w", err)
	}

	n := &Node{
		Host:      h,
		PubSub:    ps,
		Telemetry: telSvc,
		Identity:  cfg.Identity,
		ctx:       ctx,
		cancel:    cancel,
		status: NodeStatus{
			StartedAt:             time.Now().UTC(),
			BootstrapPeerCount:    len(bootstrapPeers),
			AutoRelayEnabled:      false,
			StaticRelayCandidates: 0,
		},
	}
	h.Network().Notify(&nodeNetworkNotifiee{node: n})

	go n.maintainBootstrapConnections(bootstrapPeers)
	go n.waitForBootstrapPeer(ctx, bootstrapPeers)

	return n, nil
}

// Close strictly shuts down the node
func (n *Node) Close() error {
	n.cancel()
	return n.Host.Close()
}

func (n *Node) Context() context.Context {
	return n.ctx
}

// NewNodeFromParts assembles a Node from already-constructed host/pubsub parts.
// It is primarily useful for isolated tests that don't need the full Tor/DHT bootstrap path.
func NewNodeFromParts(h host.Host, ps *pubsub.PubSub, identity *keys.Keys) *Node {
	ctx, cancel := context.WithCancel(context.Background())
	n := &Node{
		Host:     h,
		PubSub:   ps,
		Identity: identity,
		ctx:      ctx,
		cancel:   cancel,
		status: NodeStatus{
			StartedAt: time.Now().UTC(),
		},
	}
	h.Network().Notify(&nodeNetworkNotifiee{node: n})
	return n
}

func (n *Node) Status() NodeStatus {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.status
}

func (n *Node) waitForBootstrapPeer(ctx context.Context, bootstrapPeers []peer.AddrInfo) bool {
	results := make(chan struct{}, 1)

	for _, peerInfo := range bootstrapPeers {
		n.setLastBootstrapAttempt()
		go func(pi peer.AddrInfo) {
			dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := n.Host.Connect(dialCtx, pi); err == nil {
				n.markBootstrapSuccess()
				select {
				case results <- struct{}{}:
				default:
				}
			}
		}(peerInfo)
	}

	select {
	case <-results:
		return true
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
	}
	return false
}

func (n *Node) maintainBootstrapConnections(bootstrapPeers []peer.AddrInfo) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.waitForBootstrapPeer(n.ctx, bootstrapPeers)
		}
	}
}

func (n *Node) setLastBootstrapAttempt() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status.LastBootstrapAttempt = time.Now().UTC()
}

func (n *Node) markBootstrapSuccess() {
	now := time.Now().UTC()
	var emitLatency bool
	var latencyMs int
	n.mu.Lock()
	n.status.LastBootstrapSuccess = now
	n.status.BootstrapPeerCount = len(n.Host.Network().Peers())
	if n.status.FirstBootstrapPeerAt.IsZero() {
		n.status.FirstBootstrapPeerAt = now
		emitLatency = true
		latencyMs = int(now.Sub(n.status.StartedAt).Milliseconds())
	}
	n.mu.Unlock()
	if emitLatency && n.Telemetry != nil {
		n.Telemetry.RecordEvent(context.Background(), "Bootstrap_First_Peer_Latency", latencyMs, map[string]string{
			"action": "node_startup",
		}, runtime.GOOS, runtime.GOARCH)
	}
}

func (n *Node) updateConnectedPeers() {
	now := time.Now().UTC()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status.ConnectedPeers = len(n.Host.Network().Peers())
	if n.status.ConnectedPeers > 0 && n.status.FirstConnectionAt.IsZero() {
		n.status.FirstConnectionAt = now
	}
}

func parseBootstrapPeerInfos(addrs []string) []peer.AddrInfo {
	peers := make([]peer.AddrInfo, 0, len(addrs))
	for _, peerAddr := range addrs {
		addr, err := multiaddr.NewMultiaddr(peerAddr)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			continue
		}
		peers = append(peers, *info)
	}
	return peers
}

func SplitBootstrapPeers(raw string) []string {
	lines := strings.Split(raw, "\n")
	addrs := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, field := range strings.Split(line, ",") {
			addr := strings.TrimSpace(field)
			if addr == "" || strings.HasPrefix(addr, "#") {
				continue
			}
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func ParseBootstrapPeerInfos(addrs []string, allowEmpty bool) ([]peer.AddrInfo, error) {
	if len(addrs) == 0 {
		if allowEmpty {
			return nil, nil
		}
		return nil, fmt.Errorf("tor-only networking requires at least one /onion3/.../p2p/... bootstrap peer")
	}

	for _, addr := range addrs {
		if !strings.HasPrefix(addr, "/onion3/") {
			return nil, fmt.Errorf("bootstrap peer %q is not onion-only; bootstrap peers must contain only /onion3/.../p2p/... addresses", addr)
		}
	}

	peers := parseBootstrapPeerInfos(addrs)
	if len(peers) != len(addrs) {
		return nil, fmt.Errorf("failed to parse one or more onion bootstrap peers")
	}
	return peers, nil
}

type nodeNetworkNotifiee struct {
	node *Node
}

func (n *nodeNetworkNotifiee) Listen(network.Network, multiaddr.Multiaddr)      {}
func (n *nodeNetworkNotifiee) ListenClose(network.Network, multiaddr.Multiaddr) {}

func (n *nodeNetworkNotifiee) Connected(_ network.Network, _ network.Conn) {
	n.node.updateConnectedPeers()
}

func (n *nodeNetworkNotifiee) Disconnected(_ network.Network, _ network.Conn) {
	n.node.updateConnectedPeers()
}
