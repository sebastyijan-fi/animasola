package p2p

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	telemetry "github.com/sebastyijan/animasola/capabilities/network.telemetry"
)

var DefaultBootstrapPeers = []string{
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
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
func NewNode(keys *keys.Keys, onionURL string, listenPort int) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())
	relayPeers := parseBootstrapPeerInfos(DefaultBootstrapPeers)

	// Convert standard crypto/ed25519 to libp2p crypto
	p2pPrivKey, err := crypto.UnmarshalEd25519PrivateKey(keys.PrivateKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to convert identity key to libp2p key: %w", err)
	}

	// Tell libp2p to advertise the Tor Hidden Service onion address so global peers can find us
	onionMultiaddr := fmt.Sprintf("/onion3/%s:%d", onionURL, listenPort)
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
		libp2p.NoTransports,                   // Disable all default transports (including QUIC)
		libp2p.Transport(tcp.NewTCPTransport), // Explicitly only enable TCP for Tor routing
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", listenPort)), // Bind specifically to what torrc expects
		libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
			// Strip all local IP leakage and only present the Tor endpoint
			return []multiaddr.Multiaddr{advertiseAddr}
		}),
		libp2p.ConnectionManager(cm),
		libp2p.ForceReachabilityPrivate(),
		libp2p.EnableAutoRelayWithStaticRelays(relayPeers),
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
		Identity:  keys,
		ctx:       ctx,
		cancel:    cancel,
		status: NodeStatus{
			StartedAt:             time.Now().UTC(),
			AutoRelayEnabled:      len(relayPeers) > 0,
			StaticRelayCandidates: len(relayPeers),
		},
	}
	h.Network().Notify(&nodeNetworkNotifiee{node: n})

	// Setup mDNS discovery to find local peers automatically
	if err := n.setupDiscovery(); err != nil {
		// mDNS failure shouldn't kill the node
		fmt.Printf("Warning: failed to setup mDNS: %s\n", err)
	}
	go n.maintainBootstrapConnections()
	go n.waitForBootstrapPeer(ctx)

	return n, nil
}

// Close strictly shuts down the node
func (n *Node) Close() error {
	n.cancel()
	return n.Host.Close()
}

// ----- Local Peer Discovery (mDNS) -----

type discoveryNotifee struct {
	h host.Host
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	// Automatically connect to local peers found via mDNS
	if pi.ID == n.h.ID() {
		return // Ignore self
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err := n.h.Connect(ctx, pi)
	if err == nil {
		// Connected
	}
}

func (n *Node) setupDiscovery() error {
	ser := mdns.NewMdnsService(n.Host, "animasola-alpha", &discoveryNotifee{h: n.Host})
	return ser.Start()
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

func (n *Node) waitForBootstrapPeer(ctx context.Context) bool {
	results := make(chan struct{}, 1)

	for _, peerAddr := range DefaultBootstrapPeers {
		addr, err := multiaddr.NewMultiaddr(peerAddr)
		if err != nil {
			continue
		}
		peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			continue
		}
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
		}(*peerInfo)
	}

	select {
	case <-results:
		return true
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
	}
	return false
}

func (n *Node) maintainBootstrapConnections() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.waitForBootstrapPeer(n.ctx)
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
