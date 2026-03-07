package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
)

func main() {
	var profile string
	var listenPort int
	var socksPort int

	flag.StringVar(&profile, "profile", "bootstrap", "Bootstrap node profile name under ~/.config/animasola/")
	flag.IntVar(&listenPort, "listen-port", 4001, "Local loopback listen port forwarded by the hidden service")
	flag.IntVar(&socksPort, "socks-port", 45000, "Local Tor SOCKS port")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	configRoot, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("resolve config dir: %v", err)
	}

	configDir := filepath.Join(configRoot, "animasola", profile)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		log.Fatalf("create config dir: %v", err)
	}

	identity, err := keys.GetOrGenerateKey(profile)
	if err != nil {
		log.Fatalf("load identity: %v", err)
	}

	torBinary, err := tor.EnsureTorBinary(configDir)
	if err != nil {
		log.Fatalf("resolve Tor binary: %v", err)
	}

	torConfig, err := tor.GenerateConfig(configDir, listenPort, socksPort)
	if err != nil {
		log.Fatalf("generate tor config: %v", err)
	}

	runner, progressCh, err := tor.Start(ctx, torBinary, torConfig)
	if err != nil {
		log.Fatalf("start Tor: %v", err)
	}
	defer runner.Stop()

	for progress := range progressCh {
		fmt.Println(progress)
		if progress == "SUCCESS_100" {
			break
		}
	}

	onionAddress, err := torConfig.GetOnionAddress()
	if err != nil {
		log.Fatalf("read onion address: %v", err)
	}

	socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
	os.Setenv("ALL_PROXY", socksAddr)
	os.Setenv("HTTP_PROXY", socksAddr)
	os.Setenv("HTTPS_PROXY", socksAddr)

	node, err := p2p.NewNode(p2p.Config{
		Identity:            identity,
		OnionAddress:        onionAddress,
		ListenPort:          listenPort,
		SocksProxy:          socksAddr,
		BootstrapPeers:      p2p.ResolveBootstrapPeers(os.Getenv("ANIMASOLA_BOOTSTRAP_PEERS")),
		AllowEmptyBootstrap: true,
	})
	if err != nil {
		log.Fatalf("create bootstrap node: %v", err)
	}
	defer node.Close()

	fmt.Printf("Bootstrap node ready\n")
	fmt.Printf("Profile: %s\n", profile)
	fmt.Printf("Peer ID: %s\n", node.Host.ID().String())
	fmt.Printf("Onion bootstrap address: /onion3/%s:%d/p2p/%s\n", onionAddress, listenPort, node.Host.ID().String())

	<-ctx.Done()
}
