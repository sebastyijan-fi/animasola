package tui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	// ... existing imports ...
	version "github.com/sebastyijan/animasola/capabilities/core.version"
	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	discovery "github.com/sebastyijan/animasola/capabilities/network.discovery"
	http "github.com/sebastyijan/animasola/capabilities/network.http"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	telemetry "github.com/sebastyijan/animasola/capabilities/network.telemetry"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type AppModel struct {
	sqlite     *sqlite.Store
	user       *sqlite.User
	keys       *keys.Keys
	torConfig  *tor.Config
	torProcess *tor.Runner
	registry   *registry.Client
	node       *p2p.Node
	discovery  *discovery.Service

	width  int
	height int

	currentView string // "splash", "setup", "home", or "room"
	splashView  *TorSplashModel
	setupView   *SetupModel
	homeView    *HomeModel
	roomView    *RoomModel

	torStartTime time.Time // Tracks when Tor began bootstrapping
	appVersion   string

	// Phase 21: Real-Time Performance HUD state
	showDebugHUD    bool
	latestTelemetry []telemetry.TelemetryEvent
	telemetryFeed   chan telemetry.TelemetryEvent
}

func NewAppModel(s *sqlite.Store, u *sqlite.User, keys *keys.Keys, torConfig *tor.Config, torProcess *tor.Runner, torStartTime time.Time, torProgressCh <-chan string, appVersion string, registryClient *registry.Client) *AppModel {
	m := &AppModel{
		sqlite:       s,
		user:         u,
		keys:         keys,
		torConfig:    torConfig,
		torProcess:   torProcess,
		registry:     registryClient,
		node:         nil,      // We don't have a node yet!
		currentView:  "splash", // Boot into Tor waitscreen by default
		splashView:   NewTorSplashModel(torProgressCh),
		setupView:    NewSetupModel(),
		homeView:     NewHomeModel(s, u, keys, nil, nil, registryClient), // Passed as nil initially
		roomView:     NewRoomModel(s, u, nil, nil), // Passed as nil initially
		appVersion:   appVersion,
		torStartTime: torStartTime,

		showDebugHUD:    false,
		latestTelemetry: make([]telemetry.TelemetryEvent, 0, 5),
		telemetryFeed:   make(chan telemetry.TelemetryEvent, 10),
	}
	return m
}

func (m *AppModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.splashView.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.setupView.SetSize(w, h)
	m.homeView.SetSize(w, h)
	m.roomView.SetSize(w, h)
}

func (m *AppModel) Shutdown() {
	if m.discovery != nil {
		_ = m.discovery.Close()
	}
	if m.node != nil {
		m.node.Close()
	}
	if m.torProcess != nil {
		m.torProcess.Stop()
	}
}

func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.splashView.Init(), // Start ripping through Tor logs
		m.listenForTelemetry(),
		textinput.Blink,
	)
}

func (m *AppModel) checkIdentityCmd() tea.Cmd {
	return func() tea.Msg {
		// Try to fetch the identity key. If we fail because it doesn't exist, we need setup.
		// Since GetOrGenerateKey() auto-generates if missing, we need a lighter check.
		// Let's rely on the Update loop to handle the "KeyExistsMsg" vs "NeedsSetupMsg".
		return checkKeyMsg{}
	}
}

type checkKeyMsg struct{}

// checkUpdateCmd fires a background goroutine over Tor to see if a newer binary exists
func (m *AppModel) checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		release, err := http.FetchLatestRelease()
		if err != nil {
			// Fail silently, we don't want to spam users if GitHub/Tor acts up
			return nil
		}
		if release.TagName != "" && release.TagName != version.Current {
			return UpdateAvailableMsg{Version: release.TagName}
		}
		return nil
	}
}

type UpdateAvailableMsg struct {
	Version string
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case TorDoneMsg:
		// Tor has successfully bootstrapped to 100%!
		torBootDuration := int(time.Since(m.torStartTime).Milliseconds())

		// 1. Extract the hidden service .onion address that was just written to disk
		onionAddr, err := m.torConfig.GetOnionAddress()
		if err != nil {
			return m, func() tea.Msg { return TorErrorMsg(fmt.Errorf("error parsing onion address: %w", err)) }
		}

		// 2. Initialize the P2P Network Node dynamically
		node, err := p2p.NewNode(p2p.Config{
			Identity:       m.keys,
			OnionAddress:   onionAddr,
			ListenPort:     m.torConfig.HiddenServicePort,
			SocksProxy:     fmt.Sprintf("socks5://127.0.0.1:%d", m.torConfig.SocksPort),
			BootstrapPeers: p2p.ResolveBootstrapPeers(os.Getenv("ANIMASOLA_BOOTSTRAP_PEERS")),
		})
		if err != nil {
			return m, func() tea.Msg { return TorErrorMsg(fmt.Errorf("error starting p2p node: %w", err)) }
		}
		m.node = node

		// 3. Inject the active node into the UI panels
		m.discovery = discovery.NewService(node, m.sqlite, m.user, m.registry)
		m.homeView.SetNode(node)
		m.homeView.SetDiscovery(m.discovery)
		m.roomView.SetNode(node)
		m.roomView.SetDiscovery(m.discovery)

		// 4. Start Global Discovery over the Kademlia DHT
		// We'll pass a buffered channel to receive discovered rooms async
		// A buffer of 100 ensures the background Tor Tor daemon never drops messages
		// via its non-blocking select even if the Bubbletea UI is busy rendering.
		discoveryCh := make(chan sqlite.Room, 100)
		if err := m.discovery.Start(discoveryCh); err != nil {
			// Non-fatal, just log or ignore
		}

		// Phase 20: Emit the asynchronous Tor Boot Telemetry.
		if m.node.Telemetry != nil {
			// Wire the global Telemetry engine to dual-route metrics to our local HUD buffer!
			m.node.Telemetry.SetLocalInterceptor(m.telemetryFeed)

			m.node.Telemetry.RecordEvent(context.Background(), "Tor_Bootstrap_Latency", torBootDuration, map[string]string{
				"action": "app_startup",
			}, runtime.GOOS, runtime.GOARCH)
		}

		// Fall through to checking the user's cryptographic identity layer
		return m, tea.Batch(
			m.checkIdentityCmd(),
			m.listenForDiscovery(discoveryCh),
			m.checkUpdateCmd(),
		)

	case checkKeyMsg:
		if keys.HasKey(m.user.Username) {
			m.currentView = "home"
			return m, m.homeView.Init()
		}

		// Show setup explicitly to await consent before generating.
		m.currentView = "setup"
		return m, m.setupView.Init()

	case error:
		// UX HARDENING: If the RoomModel's subscribeToTopicCmd throws a DHT timeout error
		// (e.g. joining a ghost room), we must catch it here, obliterate the SQLite record
		// so it doesn't stay pinned, and kick the user back to the Home view with the error string.
		if m.currentView == "room" {
			failedRoomID := m.roomView.roomID
			if failedRoomID != "" {
				_ = m.sqlite.DeleteRoom(context.Background(), failedRoomID)
			}
			m.currentView = "home"
			m.homeView.err = msg
			cmds = append(cmds, m.homeView.Init())
		}
		return m, tea.Batch(cmds...)

	case TryGenerateKeyMsg:
		// The Setup view asked us to generate the key in the background
		return m, func() tea.Msg {
			_, err := keys.GetOrGenerateKey(m.user.Username)
			if err != nil {
				return SetupErrorMsg{Err: err}
			}
			return KeyGeneratedMsg{}
		}

	case KeyGeneratedMsg:
		// Key was successfully generated! Move to Home view.
		m.currentView = "home"
		// Pass it along so setupView can update its status internally if it wants
		m.setupView.Update(msg)
		return m, m.homeView.Init()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if (msg.String() == "ctrl+e" || msg.String() == "f12") && m.currentView != "splash" {
			m.showDebugHUD = !m.showDebugHUD
			return m, nil
		}
		if msg.String() == "esc" && m.currentView == "room" {
			// Explicitly tear down the libp2p pubsub subscription so it doesn't
			// continue draining Tor bandwidth or CPU in the background!
			m.roomView.Leave()

			m.currentView = "home"
			cmds = append(cmds, m.homeView.Init()) // Refresh home
			return m, tea.Batch(cmds...)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.splashView.Update(msg)
		m.setupView.SetSize(msg.Width, msg.Height)
		m.homeView.SetSize(msg.Width, msg.Height)
		m.roomView.SetSize(msg.Width, msg.Height)

	case OpenRoomMsg:
		m.currentView = "room"
		cmds = append(cmds, m.roomView.OpenRoom(msg.RoomID, msg.RoomName, msg.IsPrivate, msg.RoomKey, msg.RequireDHT))
		return m, tea.Batch(cmds...)
	case RoomDiscoveredMsg:
		// Received from the async global discovery channel
		// Route the update Idiomatically down to HomeView
		model, cmd := m.homeView.Update(msg)
		m.homeView = model.(*HomeModel)
		cmds = append(cmds, cmd)

		// We MUST re-queue the listener to block for the next discovery hit!
		if msg.Ch != nil {
			cmds = append(cmds, m.listenForDiscovery(msg.Ch))
		}
		return m, tea.Batch(cmds...)

	case telemetry.TelemetryEvent:
		// Push to a bounded 5-element ring buffer
		if len(m.latestTelemetry) >= 5 {
			m.latestTelemetry = m.latestTelemetry[1:]
		}
		m.latestTelemetry = append(m.latestTelemetry, msg)

		// Immediately queue the listener to catch the next fast-flowing metric!
		return m, m.listenForTelemetry()

	case UpdateAvailableMsg:
		// Route it directly to HomeView so it can display the banner
		m.homeView.Update(msg)
		return m, tea.Batch(cmds...)

	}

	// Route to active view
	switch m.currentView {
	case "splash":
		splashModel, cmd := m.splashView.Update(msg)
		m.splashView = splashModel
		cmds = append(cmds, cmd)
	case "setup":
		setupModel, cmd := m.setupView.Update(msg)
		m.setupView = setupModel.(*SetupModel)
		cmds = append(cmds, cmd)
	case "home":
		homeModel, cmd := m.homeView.Update(msg)
		m.homeView = homeModel.(*HomeModel)
		cmds = append(cmds, cmd)
	case "room":
		roomModel, cmd := m.roomView.Update(msg)
		m.roomView = roomModel.(*RoomModel)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *AppModel) View() string {
	if m.width == 0 {
		return "Initializing Display..."
	}

	var activeView string
	switch m.currentView {
	case "splash":
		return m.splashView.View() // Never overlay Tor Splash screens
	case "setup":
		activeView = m.setupView.View()
	case "home":
		activeView = m.homeView.View()
	case "room":
		activeView = m.roomView.View()
	}

	if !m.showDebugHUD {
		return activeView
	}

	// ---------------------------------------------------------
	// PHASE 21: Real-Time Performance Telemetry HUD Compiler
	// ---------------------------------------------------------
	hudTitle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("ANALYTICS: Global Telemetry Stream")
	var metrics []string
	if len(m.latestTelemetry) == 0 {
		metrics = append(metrics, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("No cryptographic or routing pings resolved yet..."))
	} else {
		for i := len(m.latestTelemetry) - 1; i >= 0; i-- { // Reverse chron
			evt := m.latestTelemetry[i]
			title := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render(evt.EventName)
			ms := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("%dms", evt.Duration))
			metrics = append(metrics, fmt.Sprintf("%-35s %s", title, ms))
		}
	}

	hudBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(1, 4).
		Render(lipgloss.JoinVertical(lipgloss.Left, hudTitle, "\n"+strings.Join(metrics, "\n")))

	// Composite the HUD float absolutely over the Top-Right of the active Bubbletea buffer
	return lipgloss.Place(m.width, m.height, lipgloss.Right, lipgloss.Top, hudBox) +
		"\n\033[" + fmt.Sprintf("%d", m.height) + "A\033[" + fmt.Sprintf("%d", m.width) + "D" + activeView
	// 	 ^ ANSI escape sequences forcibly rewind the cursor to draw the main UI UNDER the floating HUD.
}

// listenForTelemetry attaches the HUD buffer to the Global GossipSub Telemetry stream
func (m *AppModel) listenForTelemetry() tea.Cmd {
	return func() tea.Msg {
		if m.telemetryFeed == nil {
			return nil
		}
		evt, ok := <-m.telemetryFeed
		if !ok {
			return nil
		}
		return evt
	}
}

// listenForDiscovery converts Go channel messages into native Bubbletea Msgs
func (m *AppModel) listenForDiscovery(ch chan sqlite.Room) tea.Cmd {
	return func() tea.Msg {
		room, ok := <-ch
		if !ok {
			return nil
		}
		return RoomDiscoveredMsg{Room: room, Ch: ch}
	}
}
