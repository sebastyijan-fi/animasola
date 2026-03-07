package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	discovery "github.com/sebastyijan/animasola/capabilities/network.discovery"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type runtimeNode struct {
	profile      string
	listenPort   int
	onionAddress string
	store        *sqlite.Store
	user         *sqlite.User
	node         *p2p.Node
	discovery    *discovery.Service
	registry     *registry.Client
	torRunner    *tor.Runner
	rooms        map[string]*p2p.Room
	registered   bool
}

type lab struct {
	ctx            context.Context
	cancel         context.CancelFunc
	transport      string
	nextListenPort int
	nextSocksPort  int
	bootstrap      *runtimeNode
	bootstrapAddr  string
	nodes          []*runtimeNode
}

type discoveryEnvelope struct {
	MessageType string    `json:"message_type"`
	RoomID      string    `json:"room_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatorID   string    `json:"creator_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
	Signature   string    `json:"signature,omitempty"`
}

type scenarioResult struct {
	Name    string
	Status  string
	Details string
	Err     error
}

func main() {
	cleanupEnv, err := isolateAttackLabEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to isolate attack lab environment: %v\n", err)
		os.Exit(1)
	}
	defer cleanupEnv()

	var scenarioCSV string
	var transport string
	flag.StringVar(&scenarioCSV, "scenarios", "spoofed-discovery,snapshot-flood,spoofed-chat,message-flood,multi-sender-flood,metadata-churn-flood,duplicate-display-name,username-rewrite,public-room-spam,private-room-spam,sybil-room-flood", "Comma-separated scenarios to run")
	flag.StringVar(&transport, "transport", "local", "Transport mode: local or tor")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l, err := newLab(ctx, transport)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize attack lab: %v\n", err)
		os.Exit(1)
	}
	defer l.Close()

	scenarios := parseScenarios(scenarioCSV)
	results := make([]scenarioResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		result := l.runScenario(scenario)
		results = append(results, result)
		status := result.Status
		if result.Err != nil {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s: %s\n", status, result.Name, result.Details)
		if result.Err != nil {
			fmt.Printf("  error: %v\n", result.Err)
		}
	}

	failed := false
	for _, result := range results {
		if result.Err != nil {
			failed = true
			break
		}
	}
	if failed {
		os.Exit(1)
	}
}

func isolateAttackLabEnvironment() (func(), error) {
	root, err := os.MkdirTemp("", "animasola-attack-lab-")
	if err != nil {
		return nil, err
	}

	configRoot := filepath.Join(root, "config")
	dataRoot := filepath.Join(root, "data")
	cacheRoot := filepath.Join(root, "cache")
	homeRoot := filepath.Join(root, "home")
	for _, dir := range []string{configRoot, dataRoot, cacheRoot, homeRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}

	_ = os.Setenv("HOME", homeRoot)
	_ = os.Setenv("XDG_CONFIG_HOME", configRoot)
	_ = os.Setenv("XDG_DATA_HOME", dataRoot)
	_ = os.Setenv("XDG_CACHE_HOME", cacheRoot)

	return func() {
		_ = os.RemoveAll(root)
	}, nil
}

func newLab(parent context.Context, transport string) (*lab, error) {
	ctx, cancel := context.WithCancel(parent)
	l := &lab{
		ctx:            ctx,
		cancel:         cancel,
		transport:      transport,
		nextListenPort: 4001,
		nextSocksPort:  45000,
	}

	if transport == "local" {
		return l, nil
	}
	if transport != "tor" {
		cancel()
		return nil, fmt.Errorf("unsupported transport %q", transport)
	}

	bootstrap, err := l.newRuntimeNode("attack-bootstrap", false, true)
	if err != nil {
		cancel()
		return nil, err
	}
	l.bootstrap = bootstrap
	l.bootstrapAddr = fmt.Sprintf("/onion3/%s:%d/p2p/%s", bootstrap.onionAddress, bootstrap.listenPort, bootstrap.node.Host.ID().String())
	return l, nil
}

func (l *lab) Close() {
	l.cancel()
	for i := len(l.nodes) - 1; i >= 0; i-- {
		l.nodes[i].Close()
	}
	if l.bootstrap != nil {
		l.bootstrap.Close()
	}
}

func (l *lab) newRuntimeNode(profile string, withDiscovery bool, allowEmptyBootstrap bool) (*runtimeNode, error) {
	listenPort := l.nextListenPort
	socksPort := l.nextSocksPort
	l.nextListenPort++
	l.nextSocksPort++

	fmt.Printf("[LAB] starting node %s (listen=%d socks=%d discovery=%t allow_empty_bootstrap=%t)\n", profile, listenPort, socksPort, withDiscovery, allowEmptyBootstrap)

	configRoot, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	configDir := filepath.Join(configRoot, "animasola", profile)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, err
	}

	store, err := sqlite.Open(filepath.Join(configDir, "animasola.db"))
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(l.ctx); err != nil {
		store.Close()
		return nil, err
	}

	identity, err := keys.GetOrGenerateKey(profile)
	if err != nil {
		store.Close()
		return nil, err
	}
	peerID, err := identity.PeerID()
	if err != nil {
		store.Close()
		return nil, err
	}
	user, err := store.GetOrCreateUser(l.ctx, profile, peerID)
	if err != nil {
		store.Close()
		return nil, err
	}

	if l.transport == "local" {
		return l.newLocalRuntimeNode(profile, store, user, identity, withDiscovery, listenPort)
	}

	torBinary, err := tor.EnsureTorBinary(configDir)
	if err != nil {
		store.Close()
		return nil, err
	}
	torConfig, err := tor.GenerateConfig(configDir, listenPort, socksPort)
	if err != nil {
		store.Close()
		return nil, err
	}
	runner, progressCh, err := tor.Start(l.ctx, torBinary, torConfig)
	if err != nil {
		store.Close()
		return nil, err
	}
	if err := waitForTor(profile, progressCh, 90*time.Second); err != nil {
		runner.Stop()
		store.Close()
		return nil, err
	}

	onionAddress, err := torConfig.GetOnionAddress()
	if err != nil {
		runner.Stop()
		store.Close()
		return nil, err
	}

	cfg := p2p.Config{
		Identity:            identity,
		OnionAddress:        onionAddress,
		ListenPort:          listenPort,
		SocksProxy:          fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort),
		AllowEmptyBootstrap: allowEmptyBootstrap,
	}
	if !allowEmptyBootstrap {
		cfg.BootstrapPeers = []string{l.bootstrapAddr}
	}

	node, err := p2p.NewNode(cfg)
	if err != nil {
		runner.Stop()
		store.Close()
		return nil, err
	}

	rt := &runtimeNode{
		profile:      profile,
		listenPort:   listenPort,
		onionAddress: onionAddress,
		store:        store,
		user:         user,
		node:         node,
		registry:     registry.NewClient(),
		torRunner:    runner,
		rooms:        make(map[string]*p2p.Room),
	}
	if withDiscovery {
		disco := discovery.NewService(node, store, user, rt.registry)
		discoveryCh := make(chan sqlite.Room, 256)
		if err := disco.Start(discoveryCh); err != nil {
			rt.Close()
			return nil, err
		}
		rt.discovery = disco
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					return
				case <-discoveryCh:
				}
			}
		}()
	}

	fmt.Printf("[LAB] node ready %s peer=%s onion=%s\n", profile, node.Host.ID().String(), onionAddress)
	l.nodes = append(l.nodes, rt)
	return rt, nil
}

func (l *lab) newLocalRuntimeNode(profile string, store *sqlite.Store, user *sqlite.User, identity *keys.Keys, withDiscovery bool, listenPort int) (*runtimeNode, error) {
	p2pPrivKey, err := crypto.UnmarshalEd25519PrivateKey(identity.PrivateKey)
	if err != nil {
		store.Close()
		return nil, err
	}

	host, err := libp2p.New(
		libp2p.Identity(p2pPrivKey),
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
	)
	if err != nil {
		store.Close()
		return nil, err
	}

	ps, err := pubsub.NewGossipSub(l.ctx, host)
	if err != nil {
		_ = host.Close()
		store.Close()
		return nil, err
	}

	node := p2p.NewNodeFromParts(host, ps, identity)
	rt := &runtimeNode{
		profile:    profile,
		listenPort: listenPort,
		store:      store,
		user:       user,
		node:       node,
		registry:   registry.NewClient(),
		rooms:      make(map[string]*p2p.Room),
	}

	for _, existing := range l.nodes {
		if err := connectHosts(l.ctx, rt.node.Host, existing.node.Host); err != nil {
			rt.Close()
			return nil, err
		}
	}

	if withDiscovery {
		disco := discovery.NewService(node, store, user, rt.registry)
		discoveryCh := make(chan sqlite.Room, 256)
		if err := disco.Start(discoveryCh); err != nil {
			rt.Close()
			return nil, err
		}
		rt.discovery = disco
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					return
				case <-discoveryCh:
				}
			}
		}()
	}

	fmt.Printf("[LAB] node ready %s peer=%s transport=local\n", profile, node.Host.ID().String())
	l.nodes = append(l.nodes, rt)
	return rt, nil
}

func (r *runtimeNode) Close() {
	for _, room := range r.rooms {
		room.LeaveRoom()
	}
	if r.registered && r.node != nil && r.node.Identity != nil && r.user != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if r.registry != nil {
			_ = r.registry.ReleaseProfile(ctx, r.node.Identity, r.user.Username, r.user.ID)
		}
		cancel()
	}
	if r.discovery != nil {
		_ = r.discovery.Close()
	}
	if r.node != nil {
		_ = r.node.Close()
	}
	if r.torRunner != nil {
		r.torRunner.Stop()
	}
	if r.store != nil {
		r.store.Close()
	}
}

func (l *lab) runScenario(name string) scenarioResult {
	start := len(l.nodes)
	defer func() {
		for i := len(l.nodes) - 1; i >= start; i-- {
			l.nodes[i].Close()
		}
		l.nodes = l.nodes[:start]
	}()

	fmt.Printf("[LAB] running scenario %s\n", name)
	switch name {
	case "spoofed-discovery":
		return l.runSpoofedDiscovery()
	case "snapshot-flood":
		return l.runSnapshotFlood()
	case "spoofed-chat":
		return l.runSpoofedChat()
	case "message-flood":
		return l.runMessageFlood()
	case "multi-sender-flood":
		return l.runMultiSenderFlood()
	case "metadata-churn-flood":
		return l.runMetadataChurnFlood()
	case "duplicate-display-name":
		return l.runDuplicateDisplayName()
	case "username-rewrite":
		return l.runUsernameRewrite()
	case "public-room-spam":
		return l.runPublicRoomSpam()
	case "private-room-spam":
		return l.runPrivateRoomSpam()
	case "sybil-room-flood":
		return l.runSybilRoomFlood()
	default:
		return scenarioResult{
			Name:    name,
			Status:  "FAIL",
			Details: "unknown scenario",
			Err:     fmt.Errorf("unknown scenario %q", name),
		}
	}
}

func (l *lab) registerRuntimeNode(rt *runtimeNode) error {
	if rt == nil || rt.node == nil || rt.node.Identity == nil || rt.user == nil {
		return fmt.Errorf("runtime node is incomplete")
	}
	ctx, cancel := context.WithTimeout(l.ctx, 10*time.Second)
	defer cancel()
	if rt.registry == nil {
		rt.registry = registry.NewClient()
	}
	if err := rt.registry.RegisterProfile(ctx, rt.node.Identity, rt.user.Username, rt.user.ID); err != nil {
		return err
	}
	rt.registered = true
	return nil
}

func (l *lab) runSpoofedDiscovery() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-discovery", true, false)
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-attacker-discovery", false, false)
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}

	topic, err := attacker.node.PubSub.Join(discovery.GlobalDiscoveryTopic)
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "attacker failed to join discovery topic", Err: err}
	}
	defer topic.Close()
	sub, err := topic.Subscribe()
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "attacker failed to subscribe to discovery topic", Err: err}
	}
	defer sub.Cancel()

	if err := waitFor(20*time.Second, func() bool {
		return victim.discovery.Status().KnownPeerCount > 0 && len(topic.ListPeers()) > 0
	}); err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "discovery mesh did not settle", Err: err}
	}

	now := time.Now().UTC()
	payload, err := json.Marshal(discoveryEnvelope{
		MessageType: "announce",
		RoomID:      "public-forged-discovery-room",
		Name:        "forged-room",
		Description: "forged",
		CreatorID:   victim.node.Host.ID().String(),
		CreatedAt:   now,
		UpdatedAt:   now,
		Version:     1,
		Signature:   "invalid-signature",
	})
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "marshal failed", Err: err}
	}
	if err := topic.Publish(l.ctx, payload); err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "publish failed", Err: err}
	}

	time.Sleep(2 * time.Second)

	rooms, err := victim.store.SearchAllRooms(l.ctx)
	if err != nil {
		return scenarioResult{Name: "spoofed-discovery", Status: "FAIL", Details: "victim search failed", Err: err}
	}
	for _, room := range rooms {
		if room.ID == "public-forged-discovery-room" {
			return scenarioResult{
				Name:    "spoofed-discovery",
				Status:  "FAIL",
				Details: "victim accepted forged discovery metadata",
				Err:     errors.New("forged discovery room became visible"),
			}
		}
	}

	return scenarioResult{
		Name:    "spoofed-discovery",
		Status:  "PASS",
		Details: "victim ignored forged discovery announce with invalid creator/signature",
	}
}

func (l *lab) runSnapshotFlood() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-snapshot", true, false)
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-attacker-snapshot", true, false)
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	observer, err := l.newRuntimeNode("attack-observer-snapshot", false, false)
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "observer startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}

	room, err := victim.createPublicRoom("snapshot-target-room")
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}

	obsTopic, err := observer.node.PubSub.Join(discovery.GlobalDiscoveryTopic)
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "observer join failed", Err: err}
	}
	defer obsTopic.Close()
	obsSub, err := obsTopic.Subscribe()
	if err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "observer subscribe failed", Err: err}
	}
	defer obsSub.Cancel()

	if err := waitFor(25*time.Second, func() bool {
		return victim.discovery.Status().KnownPeerCount > 0 && attacker.discovery.Status().KnownPeerCount > 0 && len(obsTopic.ListPeers()) > 0
	}); err != nil {
		return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "discovery mesh did not settle", Err: err}
	}

	var mu sync.Mutex
	announceCount := 0
	observeCtx, cancel := context.WithCancel(l.ctx)
	defer cancel()
	go func() {
		for {
			msg, err := obsSub.Next(observeCtx)
			if err != nil {
				return
			}
			var envelope discoveryEnvelope
			if err := json.Unmarshal(msg.Data, &envelope); err != nil {
				continue
			}
			if msg.ReceivedFrom.String() == victim.node.Host.ID().String() && envelope.MessageType == "announce" && envelope.RoomID == room.ID {
				mu.Lock()
				announceCount++
				mu.Unlock()
			}
		}
	}()

	for i := 0; i < 20; i++ {
		if err := attacker.discovery.RequestSnapshot(l.ctx); err != nil {
			return scenarioResult{Name: "snapshot-flood", Status: "FAIL", Details: "snapshot request failed", Err: err}
		}
		time.Sleep(200 * time.Millisecond)
	}

	time.Sleep(3 * time.Second)

	mu.Lock()
	count := announceCount
	mu.Unlock()

	if count > 2 {
		return scenarioResult{
			Name:    "snapshot-flood",
			Status:  "FAIL",
			Details: fmt.Sprintf("victim rebroadcasted %d times during snapshot flood", count),
			Err:     fmt.Errorf("snapshot flood bypassed cooldown"),
		}
	}

	return scenarioResult{
		Name:    "snapshot-flood",
		Status:  "PASS",
		Details: fmt.Sprintf("victim announce count stayed bounded at %d during 20 rapid snapshot requests", count),
	}
}

func (l *lab) runSpoofedChat() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-chat", false, false)
	if err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-attacker-chat", false, false)
	if err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}

	room, err := victim.createPublicRoom("spoof-target-room")
	if err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}
	if _, err := attacker.joinPublicRoom(room.ID, room.Name); err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "attacker join failed", Err: err}
	}

	victimRoom := victim.rooms[room.ID]
	attackerRoom := attacker.rooms[room.ID]
	if err := waitFor(20*time.Second, func() bool {
		return len(victimRoom.Topic.ListPeers()) > 0 && len(attackerRoom.Topic.ListPeers()) > 0
	}); err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "room mesh did not settle", Err: err}
	}

	forged := p2p.NetworkMessage{
		ID:             "forged-author-message",
		RoomID:         room.ID,
		RoomName:       room.Name,
		AuthorID:       victim.node.Host.ID().String(),
		AuthorUsername: "mallory",
		Content:        "forged payload",
		CreatedAt:      time.Now().UTC().Format(sqlite.SortableTimeFormat),
	}
	payload, err := json.Marshal(forged)
	if err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "marshal failed", Err: err}
	}
	if err := attackerRoom.Topic.Publish(l.ctx, payload); err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "publish failed", Err: err}
	}

	time.Sleep(2 * time.Second)

	msgs, err := victim.store.ListMessages(l.ctx, room.ID, 100)
	if err != nil {
		return scenarioResult{Name: "spoofed-chat", Status: "FAIL", Details: "victim message load failed", Err: err}
	}
	for _, msg := range msgs {
		if msg.ID == forged.ID {
			return scenarioResult{
				Name:    "spoofed-chat",
				Status:  "FAIL",
				Details: "victim accepted forged author payload",
				Err:     errors.New("forged chat message stored"),
			}
		}
	}

	return scenarioResult{
		Name:    "spoofed-chat",
		Status:  "PASS",
		Details: "victim rejected forged room message with mismatched author identity",
	}
}

func (l *lab) runMessageFlood() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-message-flood", false, false)
	if err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-attacker-message-flood", false, false)
	if err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}
	if err := l.registerRuntimeNode(attacker); err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "attacker profile registration failed", Err: err}
	}

	room, err := victim.createPublicRoom("message-flood-room")
	if err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}
	if _, err := attacker.joinPublicRoom(room.ID, room.Name); err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "attacker join failed", Err: err}
	}

	if err := waitFor(20*time.Second, func() bool {
		return len(victim.rooms[room.ID].Topic.ListPeers()) > 0 && len(attacker.rooms[room.ID].Topic.ListPeers()) > 0
	}); err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "room mesh did not settle", Err: err}
	}

	totalMessages := 50
	for i := 0; i < totalMessages; i++ {
		if err := attacker.publishRawPublicMessage(room.ID, room.Name, attacker.user.Username, fmt.Sprintf("flood-%02d", i+1)); err != nil {
			return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "flood publish failed", Err: err}
		}
	}

	time.Sleep(3 * time.Second)

	msgs, err := victim.store.ListMessages(l.ctx, room.ID, 200)
	if err != nil {
		return scenarioResult{Name: "message-flood", Status: "FAIL", Details: "victim message load failed", Err: err}
	}

	if len(msgs) > 20 {
		return scenarioResult{
			Name:    "message-flood",
			Status:  "FAIL",
			Details: fmt.Sprintf("victim stored %d/%d burst messages from one sender; expected per-author burst cap to hold", len(msgs), totalMessages),
			Err:     fmt.Errorf("message flood bypassed listener burst limit"),
		}
	}
	if len(msgs) == 0 {
		return scenarioResult{
			Name:    "message-flood",
			Status:  "FAIL",
			Details: "victim stored zero messages during flood run",
			Err:     fmt.Errorf("message flood scenario did not propagate"),
		}
	}

	return scenarioResult{
		Name:    "message-flood",
		Status:  "PASS",
		Details: fmt.Sprintf("victim stored %d/%d rapid messages from one sender; listener burst cap held", len(msgs), totalMessages),
	}
}

func (l *lab) runMultiSenderFlood() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-multi-flood", false, false)
	if err != nil {
		return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "victim startup failed", Err: err}
	}

	attackers := make([]*runtimeNode, 0, 2)
	for i := 0; i < 2; i++ {
		attacker, err := l.newRuntimeNode(fmt.Sprintf("attack-multi-flood-%d", i+1), false, false)
		if err != nil {
			return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "attacker startup failed", Err: err}
		}
		attackers = append(attackers, attacker)
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}
	for _, attacker := range attackers {
		if err := l.registerRuntimeNode(attacker); err != nil {
			return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "attacker profile registration failed", Err: err}
		}
	}

	room, err := victim.createPublicRoom("multi-sender-flood-room")
	if err != nil {
		return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}
	for _, attacker := range attackers {
		if _, err := attacker.joinPublicRoom(room.ID, room.Name); err != nil {
			return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "attacker join failed", Err: err}
		}
	}

	if err := waitFor(20*time.Second, func() bool {
		return len(victim.rooms[room.ID].Topic.ListPeers()) >= len(attackers)
	}); err != nil {
		return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "room mesh did not settle", Err: err}
	}

	messagesPerAttacker := 20
	for attackerIndex, attacker := range attackers {
		for i := 0; i < messagesPerAttacker; i++ {
			if err := attacker.publishRawPublicMessage(room.ID, room.Name, attacker.user.Username, fmt.Sprintf("multi-%d-%02d", attackerIndex+1, i+1)); err != nil {
				return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "flood publish failed", Err: err}
			}
		}
	}

	time.Sleep(3 * time.Second)

	msgs, err := victim.store.ListMessages(l.ctx, room.ID, 300)
	if err != nil {
		return scenarioResult{Name: "multi-sender-flood", Status: "FAIL", Details: "victim message load failed", Err: err}
	}

	totalExpected := len(attackers) * messagesPerAttacker
	if len(msgs) > p2p.InboundRoomBurstLimit() {
		return scenarioResult{
			Name:    "multi-sender-flood",
			Status:  "EXPOSED",
			Details: fmt.Sprintf("victim stored %d/%d rapid messages from %d coordinated senders; room-level burst control is insufficient", len(msgs), totalExpected, len(attackers)),
		}
	}

	return scenarioResult{
		Name:    "multi-sender-flood",
		Status:  "PASS",
		Details: fmt.Sprintf("victim stored %d/%d rapid coordinated messages; room-level burst cap held", len(msgs), totalExpected),
	}
}

func (l *lab) runMetadataChurnFlood() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-metadata-churn", true, false)
	if err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	owner, err := l.newRuntimeNode("attack-owner-metadata-churn", true, false)
	if err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "owner startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(owner); err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "owner profile registration failed", Err: err}
	}

	if err := waitFor(25*time.Second, func() bool {
		return victim.discovery.Status().KnownPeerCount > 0 && owner.discovery.Status().KnownPeerCount > 0
	}); err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "discovery mesh did not settle", Err: err}
	}

	room, err := owner.createPublicRoom("metadata-churn-room")
	if err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "owner room creation failed", Err: err}
	}

	successes := 0
	attempts := 8
	for i := 0; i < attempts; i++ {
		_, err := owner.discovery.UpdatePublicRoomMetadata(l.ctx, room.ID, owner.user.ID, fmt.Sprintf("metadata-churn-%d", i+1), fmt.Sprintf("desc-%d", i+1))
		if err == nil {
			successes++
		}
		time.Sleep(150 * time.Millisecond)
	}

	time.Sleep(2 * time.Second)

	rooms, err := victim.store.SearchAllRooms(l.ctx)
	if err != nil {
		return scenarioResult{Name: "metadata-churn-flood", Status: "FAIL", Details: "victim search failed", Err: err}
	}

	finalName := ""
	for _, indexed := range rooms {
		if indexed.ID == room.ID {
			finalName = indexed.Name
			break
		}
	}

	if successes > 2 {
		return scenarioResult{
			Name:    "metadata-churn-flood",
			Status:  "EXPOSED",
			Details: fmt.Sprintf("owner pushed %d/%d metadata updates rapidly; final victim name=%q", successes, attempts, finalName),
		}
	}

	return scenarioResult{
		Name:    "metadata-churn-flood",
		Status:  "PASS",
		Details: fmt.Sprintf("owner metadata churn was bounded to %d/%d rapid updates; final victim name=%q", successes, attempts, finalName),
	}
}

func (l *lab) runDuplicateDisplayName() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-duplicate-name", false, false)
	if err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attackerA, err := l.newRuntimeNode("attack-name-a", false, false)
	if err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "first attacker startup failed", Err: err}
	}
	attackerB, err := l.newRuntimeNode("attack-name-b", false, false)
	if err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "second attacker startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}
	if err := l.registerRuntimeNode(attackerA); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "first attacker profile registration failed", Err: err}
	}
	if err := l.registerRuntimeNode(attackerB); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "second attacker profile registration failed", Err: err}
	}

	room, err := victim.createPublicRoom("duplicate-name-room")
	if err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}
	if _, err := attackerA.joinPublicRoom(room.ID, room.Name); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "first attacker join failed", Err: err}
	}
	if _, err := attackerB.joinPublicRoom(room.ID, room.Name); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "second attacker join failed", Err: err}
	}
	if err := waitFor(20*time.Second, func() bool {
		return len(victim.rooms[room.ID].Topic.ListPeers()) >= 2
	}); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "room mesh did not settle", Err: err}
	}

	if err := attackerA.publishRawPublicMessage(room.ID, room.Name, "shared-name", "first message"); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "first publish failed", Err: err}
	}
	if err := attackerB.publishRawPublicMessage(room.ID, room.Name, "shared-name", "second message"); err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "second publish failed", Err: err}
	}

	time.Sleep(2 * time.Second)

	msgs, err := victim.store.ListMessages(l.ctx, room.ID, 100)
	if err != nil {
		return scenarioResult{Name: "duplicate-display-name", Status: "FAIL", Details: "victim message load failed", Err: err}
	}
	if len(msgs) != 0 {
		return scenarioResult{
			Name:    "duplicate-display-name",
			Status:  "EXPOSED",
			Details: fmt.Sprintf("victim accepted %d spoofed message(s) under an unbound shared display name", len(msgs)),
		}
	}

	return scenarioResult{
		Name:    "duplicate-display-name",
		Status:  "PASS",
		Details: "spoofed duplicate display-name claims were rejected because the claimed name was not bound to the sender peer",
	}
}

func (l *lab) runUsernameRewrite() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-username-rewrite", false, false)
	if err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-username-rewrite", false, false)
	if err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(victim); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "victim profile registration failed", Err: err}
	}
	if err := l.registerRuntimeNode(attacker); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "attacker profile registration failed", Err: err}
	}

	room, err := victim.createPublicRoom("username-rewrite-room")
	if err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "victim room creation failed", Err: err}
	}
	if _, err := attacker.joinPublicRoom(room.ID, room.Name); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "attacker join failed", Err: err}
	}

	if err := waitFor(20*time.Second, func() bool {
		return len(victim.rooms[room.ID].Topic.ListPeers()) > 0
	}); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "room mesh did not settle", Err: err}
	}

	if err := attacker.publishRawPublicMessage(room.ID, room.Name, attacker.user.Username, "first identity"); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "first publish failed", Err: err}
	}
	time.Sleep(500 * time.Millisecond)
	if err := attacker.publishRawPublicMessage(room.ID, room.Name, "moderator", "renamed identity"); err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "second publish failed", Err: err}
	}

	time.Sleep(2 * time.Second)

	msgs, err := victim.store.ListMessages(l.ctx, room.ID, 100)
	if err != nil {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "victim message load failed", Err: err}
	}
	if len(msgs) != 1 {
		return scenarioResult{Name: "username-rewrite", Status: "FAIL", Details: "expected one accepted message after spoofed rename test", Err: fmt.Errorf("got %d messages", len(msgs))}
	}
	if msgs[0].AuthorUsername != attacker.user.Username {
		return scenarioResult{
			Name:    "username-rewrite",
			Status:  "EXPOSED",
			Details: "a peer was able to change its displayed username away from its registered profile identity",
		}
	}

	return scenarioResult{
		Name:    "username-rewrite",
		Status:  "PASS",
		Details: "spoofed rename attempts were rejected and the accepted message kept the registered profile name",
	}
}

func (l *lab) runPublicRoomSpam() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-public-spam", true, false)
	if err != nil {
		return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "victim startup failed", Err: err}
	}
	attacker, err := l.newRuntimeNode("attack-public-spam", true, false)
	if err != nil {
		return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	if err := l.registerRuntimeNode(attacker); err != nil {
		return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "attacker profile registration failed", Err: err}
	}

	if err := waitFor(25*time.Second, func() bool {
		return victim.discovery.Status().KnownPeerCount > 0
	}); err != nil {
		return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "discovery mesh did not settle", Err: err}
	}

	totalAttempts := 12
	successes := 0
	for i := 0; i < totalAttempts; i++ {
		if _, err := attacker.createPublicRoom(fmt.Sprintf("public-spam-%02d", i+1)); err != nil {
			break
		}
		successes++
	}

	if err := waitFor(20*time.Second, func() bool {
		rooms, err := victim.store.SearchAllRooms(l.ctx)
		if err != nil {
			return false
		}
		return len(rooms) >= successes
	}); err != nil {
		rooms, loadErr := victim.store.SearchAllRooms(l.ctx)
		if loadErr != nil {
			return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "victim search failed", Err: loadErr}
		}
		return scenarioResult{
			Name:    "public-room-spam",
			Status:  "FAIL",
			Details: fmt.Sprintf("victim saw %d/%d allowed public rooms", len(rooms), successes),
			Err:     err,
		}
	}

	rooms, err := victim.store.SearchAllRooms(l.ctx)
	if err != nil {
		return scenarioResult{Name: "public-room-spam", Status: "FAIL", Details: "victim search failed", Err: err}
	}
	if successes > 3 || len(rooms) > 3 {
		return scenarioResult{
			Name:    "public-room-spam",
			Status:  "EXPOSED",
			Details: fmt.Sprintf("a single peer created %d public rooms and exposed %d of them", successes, len(rooms)),
		}
	}
	return scenarioResult{
		Name:    "public-room-spam",
		Status:  "PASS",
		Details: fmt.Sprintf("a single peer was limited to %d public rooms and victim saw %d", successes, len(rooms)),
	}
}

func (l *lab) runPrivateRoomSpam() scenarioResult {
	attacker, err := l.newRuntimeNode("attack-private-spam", true, false)
	if err != nil {
		return scenarioResult{Name: "private-room-spam", Status: "FAIL", Details: "attacker startup failed", Err: err}
	}
	victim, err := l.newRuntimeNode("attack-victim-private-spam", true, false)
	if err != nil {
		return scenarioResult{Name: "private-room-spam", Status: "FAIL", Details: "victim startup failed", Err: err}
	}

	totalRooms := 12
	for i := 0; i < totalRooms; i++ {
		if _, err := attacker.createPrivateRoom(fmt.Sprintf("private-spam-%02d", i+1), "secret"); err != nil {
			return scenarioResult{Name: "private-room-spam", Status: "FAIL", Details: "private room creation failed", Err: err}
		}
	}

	time.Sleep(2 * time.Second)

	attackerRooms, err := attacker.store.ListRooms(l.ctx, attacker.user.ID)
	if err != nil {
		return scenarioResult{Name: "private-room-spam", Status: "FAIL", Details: "attacker local room list failed", Err: err}
	}
	victimPublicRooms, err := victim.store.SearchAllRooms(l.ctx)
	if err != nil {
		return scenarioResult{Name: "private-room-spam", Status: "FAIL", Details: "victim search failed", Err: err}
	}

	privateCount := 0
	for _, room := range attackerRooms {
		if room.IsPrivate {
			privateCount++
		}
	}

	if len(victimPublicRooms) != 0 {
		return scenarioResult{
			Name:    "private-room-spam",
			Status:  "FAIL",
			Details: fmt.Sprintf("victim saw %d rooms from private-room spam", len(victimPublicRooms)),
			Err:     fmt.Errorf("private rooms leaked into public discovery"),
		}
	}

	return scenarioResult{
		Name:    "private-room-spam",
		Status:  "PASS",
		Details: fmt.Sprintf("attacker created %d private rooms locally, but none leaked into victim public discovery", privateCount),
	}
}

func (l *lab) runSybilRoomFlood() scenarioResult {
	victim, err := l.newRuntimeNode("attack-victim-sybil", true, false)
	if err != nil {
		return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "victim startup failed", Err: err}
	}

	attackers := make([]*runtimeNode, 0, 2)
	for i := 0; i < 2; i++ {
		attacker, err := l.newRuntimeNode(fmt.Sprintf("attack-sybil-%d", i+1), true, false)
		if err != nil {
			return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "attacker startup failed", Err: err}
		}
		attackers = append(attackers, attacker)
	}
	for _, attacker := range attackers {
		if err := l.registerRuntimeNode(attacker); err != nil {
			return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "attacker profile registration failed", Err: err}
		}
	}

	if err := waitFor(25*time.Second, func() bool {
		return victim.discovery.Status().KnownPeerCount > 0
	}); err != nil {
		return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "victim discovery mesh did not settle", Err: err}
	}

	totalRooms := 0
	for i, attacker := range attackers {
		_, err := attacker.createPublicRoom(fmt.Sprintf("sybil-%d-room-1", i+1))
		if err != nil {
			return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "attacker room creation failed", Err: err}
		}
		totalRooms++
	}

	if err := waitFor(20*time.Second, func() bool {
		rooms, err := victim.store.SearchAllRooms(l.ctx)
		if err != nil {
			return false
		}
		return len(rooms) >= totalRooms
	}); err != nil {
		rooms, loadErr := victim.store.SearchAllRooms(l.ctx)
		if loadErr != nil {
			return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "victim room search failed", Err: loadErr}
		}
		return scenarioResult{
			Name:    "sybil-room-flood",
			Status:  "EXPOSED",
			Details: fmt.Sprintf("victim saw %d/%d attacker rooms during open public flood window", len(rooms), totalRooms),
		}
	}

	rooms, err := victim.store.SearchAllRooms(l.ctx)
	if err != nil {
		return scenarioResult{Name: "sybil-room-flood", Status: "FAIL", Details: "victim room search failed", Err: err}
	}
	return scenarioResult{
		Name:    "sybil-room-flood",
		Status:  "EXPOSED",
		Details: fmt.Sprintf("victim discovered %d attacker rooms; open public discovery remains Sybil-exposed by design", len(rooms)),
	}
}

func (r *runtimeNode) createPublicRoom(name string) (*sqlite.Room, error) {
	if !r.registered {
		return nil, fmt.Errorf("profile must be registered before creating a public room")
	}
	room, err := r.store.CreateRoom(context.Background(), name, "", r.user.ID, false, "")
	if err != nil {
		return nil, err
	}
	if r.registry == nil {
		r.registry = registry.NewClient()
	}
	if err := r.registry.RegisterPublicRoom(context.Background(), r.node.Identity, r.user.Username, r.user.ID, room.ID, room.Name); err != nil {
		return nil, err
	}

	topicRoom, err := r.node.JoinRoom(room.ID, false, "", false)
	if err != nil {
		_ = r.registry.ReleasePublicRoom(context.Background(), r.node.Identity, r.user.ID, room.ID)
		return nil, err
	}
	r.rooms[room.ID] = topicRoom
	go topicRoom.Listen(r.store, r.node.Host.ID().String(), nil)

	if r.discovery != nil {
		if err := r.discovery.PublishRoomSync(context.Background(), room); err != nil {
			_ = r.registry.ReleasePublicRoom(context.Background(), r.node.Identity, r.user.ID, room.ID)
			return nil, err
		}
	}
	return room, nil
}

func (r *runtimeNode) createPrivateRoom(name, password string) (*sqlite.Room, error) {
	room, err := r.store.CreateRoom(context.Background(), name, "", r.user.ID, true, "")
	if err != nil {
		return nil, err
	}
	room.RoomKey = sqlite.EncodePrivateRoomKey(room.ID, password)
	if err := r.store.UpdateRoomSecret(context.Background(), room.ID, room.RoomKey); err != nil {
		return nil, err
	}
	topicRoom, err := r.node.JoinRoom(room.ID, true, password, false)
	if err != nil {
		return nil, err
	}
	r.rooms[room.ID] = topicRoom
	go topicRoom.Listen(r.store, r.node.Host.ID().String(), nil)
	return room, nil
}

func (r *runtimeNode) joinPublicRoom(roomID, roomName string) (*p2p.Room, error) {
	_, err := r.store.JoinExternalRoom(context.Background(), roomID, roomName, r.user.ID, "", false, "")
	if err != nil {
		return nil, err
	}
	topicRoom, err := r.node.JoinRoom(roomID, false, "", false)
	if err != nil {
		return nil, err
	}
	r.rooms[roomID] = topicRoom
	go topicRoom.Listen(r.store, r.node.Host.ID().String(), nil)
	return topicRoom, nil
}

func (r *runtimeNode) publishRawPublicMessage(roomID, roomName, authorUsername, content string) error {
	topicRoom, ok := r.rooms[roomID]
	if !ok {
		return fmt.Errorf("room %s not joined", roomID)
	}

	netMsg := p2p.NetworkMessage{
		ID:             fmt.Sprintf("%s-%d", r.node.Host.ID().String(), time.Now().UnixNano()),
		RoomID:         roomID,
		RoomName:       roomName,
		AuthorID:       r.node.Host.ID().String(),
		AuthorUsername: authorUsername,
		Content:        content,
		CreatedAt:      time.Now().UTC().Format(sqlite.SortableTimeFormat),
	}
	payload, err := json.Marshal(netMsg)
	if err != nil {
		return err
	}
	return topicRoom.Topic.Publish(context.Background(), payload)
}

func waitForTor(profile string, progressCh <-chan string, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("timed out waiting for Tor bootstrap")
		case progress, ok := <-progressCh:
			if !ok {
				return fmt.Errorf("tor exited before bootstrap completed")
			}
			if progress != "" {
				fmt.Printf("[LAB:%s] %s\n", profile, progress)
			}
			if progress == "SUCCESS_100" || strings.Contains(progress, "100% (done)") {
				return nil
			}
		}
	}
}

func waitFor(timeout time.Duration, condition func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("condition not satisfied within %s", timeout)
}

func connectHosts(ctx context.Context, a, b host.Host) error {
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		return err
	}
	if err := b.Connect(ctx, peer.AddrInfo{ID: a.ID(), Addrs: a.Addrs()}); err != nil {
		return err
	}
	return nil
}

func parseScenarios(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

var _ = pubsub.DefaultMsgIdFn
