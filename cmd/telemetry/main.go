package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
)

func main() {
	var listenPort int
	var socksPort int
	flag.IntVar(&listenPort, "listen-port", 0, "Local libp2p/Tor hidden-service target port")
	flag.IntVar(&socksPort, "socks-port", 0, "Local Tor SOCKS port")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Printf("Usage: %s [-listen-port N] [-socks-port N] <admin-telemetry-username>\n", os.Args[0])
		fmt.Printf("Example: %s admin_collector_node_1\n", os.Args[0])
		os.Exit(1)
	}
	username := flag.Arg(0)

	if listenPort == 0 {
		listenPort = derivePort(username, 43000, 5000)
	}
	if socksPort == 0 {
		socksPort = derivePort(username, 48000, 5000)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Storage & Config Init
	configRoot, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("Error getting config dir: %v", err)
	}
	configDir := filepath.Join(configRoot, "animasola_telemetry", username)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		log.Fatalf("Failed to create telemetry config datastore: %v", err)
	}

	// 2. Identity Generation (The Telemetry Node also needs an untraceable Ed25519 identity)
	identityKeys, err := keys.GetOrGenerateKey("telemetry_" + username)
	if err != nil {
		log.Fatalf("Failed to derive telemetry node identity: %v", err)
	}

	// 3. Tor Engine Boot
	log.Printf("[*] Locating system Tor daemon...")
	torBinary, err := tor.EnsureTorBinary(configDir)
	if err != nil {
		log.Fatalf("Error finding tor binary: %v", err)
	}

	torConfig, err := tor.GenerateConfig(configDir, listenPort, socksPort)
	if err != nil {
		log.Fatalf("Error generating torrc: %v", err)
	}

	log.Printf("[*] Starting Tor SOCKS5 Network Node...")
	torRunner, progressCh, err := tor.Start(ctx, torBinary, torConfig)
	if err != nil {
		log.Fatalf("Error starting tor: %v", err)
	}
	defer torRunner.Stop()

	for msg := range progressCh {
		if strings.Contains(msg, "100% (done)") {
			break
		}
	}
	log.Printf("[+] Tor Fully Bootstrapped!")

	socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
	os.Setenv("ALL_PROXY", socksAddr)
	os.Setenv("HTTP_PROXY", socksAddr)
	os.Setenv("HTTPS_PROXY", socksAddr)

	onionAddress, err := torConfig.GetOnionAddress()
	if err != nil {
		log.Fatalf("Failed to read onion address: %v", err)
	}

	// 4. Kademlia Libp2p Network Integration
	node, err := p2p.NewNode(identityKeys, onionAddress, listenPort)
	if err != nil {
		log.Fatalf("Failed to create libp2p node: %v", err)
	}
	defer node.Close()

	log.Printf("[+] Tor GossipSub Mesh Linked via %s", node.Host.ID().String())

	// 5. Activate Headless Telemetry Listener
	if node.Telemetry == nil {
		log.Fatalf("[-] Critical Error: Global Telemetry Engine not available on network node")
	}

	// Will block and dump JSON telemetry records to stdout until SIGTERM
	go func() {
		if err := node.Telemetry.StartHeadlessListener(ctx); err != nil {
			log.Fatalf("Telemetry feed crashed: %v", err)
		}
	}()

	// Wait for explicit teardown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Printf("\n[*] SIGTERM received. Shutting down Tor relays safely...")
}

func derivePort(seed string, base int, span int) int {
	hashOffset := 0
	for _, char := range seed {
		hashOffset += int(char)
	}
	return base + (hashOffset % span)
}
