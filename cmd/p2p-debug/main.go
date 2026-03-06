package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sebastyijan/animasola/capabilities/identity.keys"
	"github.com/sebastyijan/animasola/capabilities/network.p2p"
	"github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func main() {
	fmt.Println("Starting P2P Network Test")

	// 1. Setup Snipa (Sender)
	snipaDB, _ := sqlite.Open("/tmp/snipa_test.db")
	snipaDB.Migrate(context.Background())
	snipaUser, _ := snipaDB.GetOrCreateUser(context.Background(), "snipa")
	snipaKeys, _ := keys.GetOrGenerateKey("snipa")
	snipaNode, err := p2p.NewNode(snipaKeys, "snipajkl3456789123456789123456789123456789123456789")
	if err != nil {
		log.Fatal("Snipa Node Error:", err)
	}
	defer snipaNode.Close()
	fmt.Println("Snipa Peer ID:", snipaNode.Host.ID().String())
	r, err := snipaDB.CreateRoom(context.Background(), "Test Room", "", snipaUser.ID, false, "")
	if err != nil {
		log.Fatal("CreateRoom Error:", err)
	}
	roomID := r.ID
	snipaDB.JoinRoom(context.Background(), snipaUser.ID, roomID)

	// 2. Setup Bob (Receiver)
	bobDB, _ := sqlite.Open("/tmp/bob_test.db")
	bobDB.Migrate(context.Background())
	bobUser, _ := bobDB.GetOrCreateUser(context.Background(), "bob")
	bobKeys, _ := keys.GetOrGenerateKey("bob")
	bobNode, err := p2p.NewNode(bobKeys, "bobjkl3456789123456789123456789123456789123456789")
	if err != nil {
		log.Fatal("Bob Node Error:", err)
	}
	defer bobNode.Close()
	_, err = bobDB.JoinExternalRoom(context.Background(), roomID, "Test Room", bobUser.ID, false, "")
	if err != nil {
		log.Fatal("Bob JoinExternalRoom Error:", err)
	}

	// Give mDNS a few seconds to discover each other
	fmt.Println("Waiting for mDNS Discovery (5s)...")
	time.Sleep(5 * time.Second)

	// 3. Both Join Topic
	snipaRoom, _ := snipaNode.JoinRoom(roomID, false, "")
	bobRoom, _ := bobNode.JoinRoom(roomID, false, "")

	// 4. Bob Starts Listening
	fmt.Println("Bob listening on topic...")
	go bobRoom.Listen(bobDB, bobNode.Host.ID().String(), func(msg sqlite.FeedMessage) {
		fmt.Printf("[BOB UI CALLBACK] Received Sync! ID: %s | Author: %s (%s)\n", msg.ID, msg.AuthorUsername, msg.AuthorID)
	})

	// Wait 1s for subscription to settle
	time.Sleep(1 * time.Second)

	// 5. Snipa Publishes
	msg, err := snipaDB.CreateMessage(context.Background(), roomID, snipaUser.ID, "Hello Bob!")
	if err != nil {
		log.Fatal("CreateMessage Error:", err)
	}
	fmt.Println("Snipa Publishing Message...")
	err = snipaRoom.Publish(context.Background(), msg, "Test Room", snipaUser.Username)
	if err != nil {
		log.Fatal("Publish Error:", err)
	}

	// 6. Wait for Bob to receive
	fmt.Println("Waiting for Bob to receive and DB to sync (3s)...")
	time.Sleep(3 * time.Second)

	// 7. Verify Bob's SQLite DB
	dbMsgs, _ := bobDB.ListMessages(context.Background(), roomID, 100)
	found := false
	for _, m := range dbMsgs {
		if m.ID == msg.ID {
			found = true
			fmt.Printf("[DB VERIFY] Bob's SQLite successfully saved message: '%s' from AuthorID: %s\n", m.Content, m.AuthorID)
		}
	}
	if !found {
		fmt.Println("[FAIL] Message not found in Bob's DB!")
	}
}
