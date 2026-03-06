package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func main() {
	var listenPort int
	var socksPort int
	flag.IntVar(&listenPort, "listen-port", 0, "Local libp2p/Tor hidden-service target port")
	flag.IntVar(&socksPort, "socks-port", 0, "Local Tor SOCKS port")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Printf("Usage: %s [-listen-port N] [-socks-port N] <username>\n", os.Args[0])
		os.Exit(1)
	}
	username := flag.Arg(0)

	if listenPort == 0 {
		listenPort = derivePort(username, 41000, 5000)
	}
	if socksPort == 0 {
		socksPort = derivePort(username, 46000, 5000)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Init Database
	configRoot, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("Error getting config dir: %v\n", err)
		os.Exit(1)
	}
	configDir := filepath.Join(configRoot, "animasola", username)
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
	keys, err := keys.GetOrGenerateKey(username)
	if err != nil {
		fmt.Printf("Error getting keys: %v\n", err)
		os.Exit(1)
	}

	hostID, err := keys.PeerID()
	if err != nil {
		fmt.Printf("Error generating peer ID: %v\n", err)
		os.Exit(1)
	}

	user, err := st.GetOrCreateUser(ctx, username, hostID)
	if err != nil {
		fmt.Printf("Error getting user: %v\n", err)
		os.Exit(1)
	}

	// 3. Init Tor Engine
	torBinary, err := tor.EnsureTorBinary(configDir)
	if err != nil {
		fmt.Printf("Error finding tor binary: %v\n", err)
		os.Exit(1)
	}

	torConfig, err := tor.GenerateConfig(configDir, listenPort, socksPort)
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

	// Phase 22.5: CRITICAL PROXY ROUTING FIX
	// Without this, Libp2p and the Kademlia DHT will drop packets locally instead of routing
	// over the Tor SOCKS circuit, which prevents the bots from actually finding each other!
	socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
	os.Setenv("ALL_PROXY", socksAddr)
	os.Setenv("HTTP_PROXY", socksAddr)
	os.Setenv("HTTPS_PROXY", socksAddr)

	onionAddress, err := torConfig.GetOnionAddress()
	if err != nil {
		fmt.Printf("Failed to read onion address: %v\n", err)
		os.Exit(1)
	}

	node, err := p2p.NewNode(keys, onionAddress, listenPort)
	if err != nil {
		fmt.Printf("Failed to create libp2p node: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[HEADLESS:%s] Node Ready: %s\n", username, node.Host.ID().String())

	// Telemetry mesh sync is useful for benchmarking, but it should not block the bot's command loop.
	if node.Telemetry != nil {
		go func() {
			fmt.Printf("[HEADLESS:%s] Waiting for DHT Telemetry synchronization...\n", username)
			err := node.Telemetry.WaitForMesh(ctx, 20*time.Second)
			if err == nil {
				fmt.Printf("[HEADLESS:%s] [TELEMETRY_SYNCED]\n", username)
			} else {
				fmt.Printf("[HEADLESS:%s] Telemetry Sync Timeout: %v\n", username, err)
			}
		}()
	}

	// Phase 23: Aggressive Data Compaction for Chaos Engine
	// High-velocity simulations can generate massive SQLite footprints locally per bot.
	// Natively loop every 5 minutes and tear down everything older than 5 minutes.
	go func() {
		compactionTicker := time.NewTicker(5 * time.Minute)
		defer compactionTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-compactionTicker.C:
				err := st.PruneAndCompact(ctx, 5*time.Minute)
				if err != nil {
					fmt.Printf("[HEADLESS:%s] Vacuum failed: %v\n", username, err)
				} else {
					fmt.Printf("[HEADLESS:%s] Aggressive SQLite Compaction Complete\n", username)
				}
			}
		}
	}()

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
		case "CREATE_PUBLIC":
			if len(parts) < 2 {
				continue
			}
			roomName := parts[1]
			r, err := st.CreateRoom(ctx, roomName, "", user.ID, false, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to create db room %s: %v\n", username, roomName, err)
				continue
			}

			// Start pubsub
			room, err := node.JoinRoom(r.ID, false, "", false)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, r.ID, err)
			} else {
				activeRooms[r.ID] = room
				fmt.Printf("[HEADLESS:%s] Created & Joined Public Room\n", username)
				// VERY IMPORTANT: Emit exactly this format so the Orchestrator regex can capture it
				fmt.Printf("[ROOM_ID: %s]\n", r.ID)
				fmt.Printf("[ROOM_META: %s public]\n", r.ID)
				go room.Listen(st, node.Host.ID().String(), nil)
			}

		case "CREATE_PRIVATE":
			if len(parts) < 3 {
				continue
			}
			roomName := parts[1]
			password := parts[2]

			start := time.Now()
			r, err := st.CreateRoom(ctx, roomName, "", user.ID, true, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to create db room %s: %v\n", username, roomName, err)
				continue
			}
			r.RoomKey = sqlite.EncodePrivateRoomKey(r.ID, password)
			if err := st.UpdateRoomSecret(ctx, r.ID, r.RoomKey); err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to persist room secret for %s: %v\n", username, roomName, err)
				continue
			}

			if node.Telemetry != nil {
				node.Telemetry.RecordEvent(ctx, "Argon2id_Derivation_Latency", int(time.Since(start).Milliseconds()), map[string]string{
					"action": "room_create_headless",
				}, runtime.GOOS, runtime.GOARCH)
			}

			// Start pubsub with Argon2id lock
			room, err := node.JoinRoom(r.ID, true, password, false)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, r.ID, err)
			} else {
				activeRooms[r.ID] = room
				fmt.Printf("[HEADLESS:%s] Created & Joined Private Room\n", username)
				fmt.Printf("[ROOM_ID: %s]\n", r.ID)
				fmt.Printf("[ROOM_META: %s private]\n", r.ID)
				go room.Listen(st, node.Host.ID().String(), nil)
			}

		case "JOIN_PUBLIC":
			if len(parts) < 2 {
				fmt.Println("Usage: JOIN_PUBLIC <room_id>")
				continue
			}
			roomID := parts[1]
			_, err = st.JoinExternalRoom(ctx, roomID, "Remote Room "+roomID[len(roomID)-8:], user.ID, false, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join db room %s: %v\n", username, roomID, err)
				continue
			}
			room, err := node.JoinRoom(roomID, false, "", false)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, roomID, err)
			} else {
				activeRooms[roomID] = room
				fmt.Printf("[HEADLESS:%s] Joined Public Room %s\n", username, roomID)
				go room.Listen(st, node.Host.ID().String(), nil)
			}

		case "JOIN":
			if len(parts) < 2 {
				continue
			}
			roomID := parts[1]
			_, err := st.JoinExternalRoom(ctx, roomID, "Stress Test Room", user.ID, false, "")
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join db room %s: %v\n", username, roomID, err)
			}

			room, err := node.JoinRoom(roomID, false, "", false)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, roomID, err)
			} else {
				activeRooms[roomID] = room
				fmt.Printf("[HEADLESS:%s] Joined Public Room %s\n", username, roomID)
				go room.Listen(st, node.Host.ID().String(), nil)
			}

		case "JOIN_PRIVATE":
			if len(parts) < 3 {
				fmt.Println("Usage: JOIN_PRIVATE <room_id> <password>")
				continue
			}
			roomID := parts[1]
			password := parts[2]

			roomKey := sqlite.EncodePrivateRoomKey(roomID, password)

			_, err = st.JoinExternalRoom(ctx, roomID, "Remote Room "+roomID[len(roomID)-8:], user.ID, true, roomKey)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join db room %s: %v\n", username, roomID, err)
				continue
			}
			room, err := node.JoinRoom(roomID, true, password, false)
			if err != nil {
				fmt.Printf("[HEADLESS:%s] Failed to join %s: %v\n", username, roomID, err)
			} else {
				activeRooms[roomID] = room
				fmt.Printf("[HEADLESS:%s] Joined Private Room %s\n", username, roomID)
				go room.Listen(st, node.Host.ID().String(), nil)
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

		case "LEAVE":
			if len(parts) < 2 {
				continue
			}
			roomID := parts[1]
			room, exists := activeRooms[roomID]
			if exists {
				room.LeaveRoom()
				delete(activeRooms, roomID)
				fmt.Printf("[HEADLESS:%s] Left Room %s\n", username, roomID)
			}

		case "DISCONNECT":
			fmt.Printf("[HEADLESS:%s] Shutting down...\n", username)
			torRunner.Stop()
			os.Exit(0)
		}
	}
}

func derivePort(seed string, base int, span int) int {
	hashOffset := 0
	for _, char := range seed {
		hashOffset += int(char)
	}
	return base + (hashOffset % span)
}
