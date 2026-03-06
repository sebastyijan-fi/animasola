package discovery_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/multiformats/go-multiaddr"
	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	discovery "github.com/sebastyijan/animasola/capabilities/network.discovery"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
	"golang.org/x/crypto/ssh"
)

func TestDiscoveryPropagatesNewestPublicRoomMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()

	keysA := mustGenerateKeys(t)
	keysB := mustGenerateKeys(t)

	hostA := mustAddPeer(t, mn, keysA, 10001)
	hostB := mustAddPeer(t, mn, keysB, 10002)

	if err := mn.LinkAll(); err != nil {
		t.Fatalf("link mocknet: %v", err)
	}
	if err := mn.ConnectAllButSelf(); err != nil {
		t.Fatalf("connect mocknet: %v", err)
	}

	pubsubA, err := pubsub.NewGossipSub(ctx, hostA)
	if err != nil {
		t.Fatalf("create pubsub A: %v", err)
	}
	pubsubB, err := pubsub.NewGossipSub(ctx, hostB)
	if err != nil {
		t.Fatalf("create pubsub B: %v", err)
	}

	nodeA := p2p.NewNodeFromParts(hostA, pubsubA, keysA)
	nodeB := p2p.NewNodeFromParts(hostB, pubsubB, keysB)
	t.Cleanup(func() { _ = nodeA.Close() })
	t.Cleanup(func() { _ = nodeB.Close() })

	storeA, userA := mustOpenStoreAndUser(t, "a.db", hostA.ID().String())
	storeB, userB := mustOpenStoreAndUser(t, "b.db", hostB.ID().String())
	defer storeA.Close()
	defer storeB.Close()
	_ = userB

	serviceA := discovery.NewService(nodeA, storeA, userA)
	serviceB := discovery.NewService(nodeB, storeB, userB)
	if err := serviceA.Start(make(chan sqlite.Room, 8)); err != nil {
		t.Fatalf("start discovery A: %v", err)
	}
	if err := serviceB.Start(make(chan sqlite.Room, 8)); err != nil {
		t.Fatalf("start discovery B: %v", err)
	}
	t.Cleanup(func() { _ = serviceA.Close() })
	t.Cleanup(func() { _ = serviceB.Close() })

	room, err := storeA.CreateRoom(ctx, "alpha", "first", userA.ID, false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room.CreatorID = hostA.ID().String()

	if err := serviceA.PublishRoomSync(ctx, room); err != nil {
		t.Fatalf("publish room v1: %v", err)
	}

	waitForIndexedRoom(t, ctx, storeB, room.ID, "alpha", 1)

	if _, err := serviceA.UpdatePublicRoomMetadata(ctx, room.ID, userA.ID, "beta", "second"); err != nil {
		t.Fatalf("update public room metadata: %v", err)
	}

	stale := *room
	stale.CreatorID = hostA.ID().String()
	if err := serviceA.PublishRoomSync(ctx, &stale); err != nil {
		t.Fatalf("publish stale room metadata: %v", err)
	}

	waitForIndexedRoom(t, ctx, storeB, room.ID, "beta", 2)
}

func mustGenerateKeys(t *testing.T) *keys.Keys {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("create ssh public key: %v", err)
	}
	return &keys.Keys{
		PrivateKey: priv,
		PublicKey:  sshPub,
	}
}

func mustAddPeer(t *testing.T, mn mocknet.Mocknet, identity *keys.Keys, port int) host.Host {
	t.Helper()

	priv, err := crypto.UnmarshalEd25519PrivateKey(identity.PrivateKey)
	if err != nil {
		t.Fatalf("unmarshal libp2p private key: %v", err)
	}
	addr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port))
	if err != nil {
		t.Fatalf("create multiaddr: %v", err)
	}
	host, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatalf("add mock peer: %v", err)
	}
	return host
}

func mustOpenStoreAndUser(t *testing.T, dbName, explicitID string) (*sqlite.Store, *sqlite.User) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), dbName)
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	user, err := store.GetOrCreateUser(ctx, dbName, explicitID)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return store, user
}

func waitForIndexedRoom(t *testing.T, ctx context.Context, store *sqlite.Store, roomID, wantName string, wantVersion int) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		rooms, err := store.SearchAllRooms(ctx)
		if err == nil {
			for _, room := range rooms {
				if room.ID == roomID && room.Name == wantName && room.Version == wantVersion {
					return
				}
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for room %s version %d name %q", roomID, wantVersion, wantName)
		case <-ticker.C:
		}
	}
}
