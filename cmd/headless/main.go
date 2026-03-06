package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <username>\n", os.Args[0])
		os.Exit(1)
	}
	username := os.Args[1]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Init Database
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home dir: %v\n", err)
		os.Exit(1)
	}
	configDir := filepath.Join(homeDir, ".config", "animasola", username)
	os.MkdirAll(configDir, 0700) // #nosec G703 -- ConfigDir is statically scoped to UserHomeDir

	dbPath := filepath.Join(configDir, "animasola.db")
	st, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Printf("Error opening db: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	st.Migrate(ctx)

	// 2. Init User / Identity
	user, err := st.GetOrCreateUser(ctx, username)
	if err != nil {
		fmt.Printf("Error getting user: %v\n", err)
		os.Exit(1)
	}

	keys, err := keys.GetOrGenerateKey(username)
	if err != nil {
		fmt.Printf("Error getting keys: %v\n", err)
		os.Exit(1)
	}

	// 3. Init Tor Engine
	torBinary, err := tor.EnsureTorBinary(configDir)
	if err != nil {
		fmt.Printf("Error finding tor binary: %v\n", err)
		os.Exit(1)
	}

	// Dynamic offset based on username hash to guarantee no collisions
	hashOffset := 0
	for _, char := range username {
		hashOffset += int(char)
	}
	socksPort := 45000 + (hashOffset % 10000)
	torConfig, err := tor.GenerateConfig(configDir, 4001, socksPort)
	if err != nil {
		fmt.Printf("Error generating torrc: %v\n", err)
		os.Exit(1)
	}

	torRunner, progressCh, err := tor.Start(ctx, torBinary, torConfig)
	if err != nil {
		fmt.Printf("Error starting tor: %v\n", err)
		os.Exit(1)
	}
	defer torRunner.Stop()

	// Wait for Tor bootstrap to complete
	fmt.Printf("[HEADLESS:%s] Waiting for Tor to Bootstrap on port %d...\n", username, socksPort)
	for msg := range progressCh {
		if strings.Contains(msg, "100% (done)") {
			break
		}
	}
	fmt.Printf("[HEADLESS:%s] Tor Bootstrapped!\n", username)

	socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
	os.Setenv("ALL_PROXY", socksAddr)
	os.Setenv("HTTP_PROXY", socksAddr)
	os.Setenv("HTTPS_PROXY", socksAddr)

	onionAddress, err := torConfig.GetOnionAddress()
	if err != nil {
		fmt.Printf("Failed to read onion address: %v\n", err)
		os.Exit(1)
	}

	node, err := p2p.NewNode(keys, onionAddress)
	if err != nil {
		fmt.Printf("Failed to create libp2p node: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[HEADLESS:%s] Node Ready: %s\n", username, node.Host.ID().String())
	fmt.Printf("[HEADLESS:%s] Awaiting commands via stdin...\n", username)

	// Keep active topics mapped
	activeRooms := make(map[string]*p2p.Room)

	// 4. Accept Stdin Commands loop
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 3)
		cmd := parts[0]

		switch cmd {
		case "JOIN":
			if len(parts) < 2 {
				continue
			}
			roomID := parts[1]
			_, err := st.JoinExternalRoom(ctx, roomID, "Stress Test Room", user.ID, false, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join db room %s: %v\n", username, roomID, err)
			}

			// Start pubsub
			room, err := node.JoinRoom(roomID, false, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, roomID, err)
			} else {
				activeRooms[roomID] = room
				fmt.Printf("[HEADLESS:%s] Joined Room %s\n", username, roomID)
				go room.Listen(st, "Stress Test Room", nil)
			}

		case "CHAT":
			if len(parts) < 3 {
				continue
			}
			roomID := parts[1]
			content := parts[2]

			room, exists := activeRooms[roomID]
			if !exists {
				fmt.Printf("[HEADLESS:%s] Not in room %s\n", username, roomID)
				continue
			}

			// Insert locally
			msg, err := st.CreateMessage(ctx, roomID, user.ID, content)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to insert msg: %v\n", username, err)
				continue
			}

			// Broadcast
			go room.Publish(ctx, msg, "Stress Test Room", username)
			fmt.Printf("[HEADLESS:%s] Sent: %s\n", username, content)

		case "DISCONNECT":
			fmt.Printf("[HEADLESS:%s] Shutting down...\n", username)
			torRunner.Stop()
			os.Exit(0)
		}
	}
}
