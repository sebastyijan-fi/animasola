package p2p

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
)

var DefaultBootstrapPeers = []string{
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
}

type Node struct {
	Host           host.Host
	PubSub         *pubsub.PubSub
	DiscoveryTopic *pubsub.Topic
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewNode initializes a libp2p host using the provided isolated Identity Key,
// and strictly advertises its Tor Onion v3 routing address to the DHT.
func NewNode(keys *keys.Keys, onionURL string) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Convert standard crypto/ed25519 to libp2p crypto
	p2pPrivKey, err := crypto.UnmarshalEd25519PrivateKey(keys.PrivateKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to convert identity key to libp2p key: %w", err)
	}

	// Tell libp2p to advertise the Tor Hidden Service onion address so global peers can find us
	onionMultiaddr := fmt.Sprintf("/onion3/%s:4001", onionURL)
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
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4001"), // Bind specifically to what torrc expects
		libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
			// Strip all local IP leakage and only present the Tor endpoint
			return []multiaddr.Multiaddr{advertiseAddr}
		}),
		libp2p.ConnectionManager(cm),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Initialize Kademlia DHT
	kDHT, err := dht.New(ctx, h)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	// Bootstrap the DHT
	if err = kDHT.Bootstrap(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Connect to default bootstrap peers
	for _, peerAddr := range DefaultBootstrapPeers {
		addr, _ := multiaddr.NewMultiaddr(peerAddr)
		peerinfo, _ := peer.AddrInfoFromP2pAddr(addr)
		go func(pi peer.AddrInfo) {
			_ = h.Connect(ctx, pi)
		}(*peerinfo)
	}

	// Wait briefly so connections resolve before GossipSub init
	time.Sleep(1 * time.Second)

	routingDiscovery := routing.NewRoutingDiscovery(kDHT)

	// Upgrade to GossipSub for scalable global routing.
	// We bind the Kademlia DHT routing discovery so it knows how to find peers for topics.
	ps, err := pubsub.NewGossipSub(ctx, h, pubsub.WithDiscovery(routingDiscovery))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create pubsub router: %w", err)
	}

	n := &Node{
		Host:   h,
		PubSub: ps,
		ctx:    ctx,
		cancel: cancel,
	}

	// Setup mDNS discovery to find local peers automatically
	if err := n.setupDiscovery(); err != nil {
		// mDNS failure shouldn't kill the node
		fmt.Printf("Warning: failed to setup mDNS: %s\n", err)
	}

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
