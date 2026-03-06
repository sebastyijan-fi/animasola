package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	// ... existing imports ...
	version "github.com/sebastyijan/animasola/capabilities/core.version"
	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	http "github.com/sebastyijan/animasola/capabilities/network.http"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type AppModel struct {
	sqlite    *sqlite.Store
	user      *sqlite.User
	keys      *keys.Keys
	torConfig *tor.Config
	node      *p2p.Node

	width  int
	height int

	currentView string // "splash", "setup", "home", or "room"
	splashView  *TorSplashModel
	setupView   *SetupModel
	homeView    *HomeModel
	roomView    *RoomModel
}

func NewAppModel(s *sqlite.Store, u *sqlite.User, keys *keys.Keys, torConfig *tor.Config, torProgressCh <-chan string) *AppModel {
	m := &AppModel{
		sqlite:      s,
		user:        u,
		keys:        keys,
		torConfig:   torConfig,
		node:        nil,      // We don't have a node yet!
		currentView: "splash", // Boot into Tor waitscreen by default
		splashView:  NewTorSplashModel(torProgressCh),
		setupView:   NewSetupModel(),
		homeView:    NewHomeModel(s, u, nil), // Passed as nil initially
		roomView:    NewRoomModel(s, u, nil), // Passed as nil initially
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

func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.splashView.Init(), // Start ripping through Tor logs
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
		// 1. Extract the hidden service .onion address that was just written to disk
		onionAddr, err := m.torConfig.GetOnionAddress()
		if err != nil {
			return m, func() tea.Msg { return TorErrorMsg(fmt.Errorf("error parsing onion address: %w", err)) }
		}

		// 2. Initialize the P2P Network Node dynamically
		node, err := p2p.NewNode(m.keys, onionAddr)
		if err != nil {
			return m, func() tea.Msg { return TorErrorMsg(fmt.Errorf("error starting p2p node: %w", err)) }
		}
		m.node = node

		// 3. Inject the active node into the UI panels
		m.homeView.SetNode(node)
		m.roomView.SetNode(node)

		// 4. Start Global Discovery over the Kademlia DHT
		// We'll pass a channel to receive discovered rooms async
		discoveryCh := make(chan sqlite.Room)
		if err := node.StartGlobalDiscovery(m.sqlite, discoveryCh); err != nil {
			// Non-fatal, just log or ignore
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
		cmds = append(cmds, m.roomView.OpenRoom(msg.RoomID, msg.RoomName, msg.IsPrivate, msg.RoomKey))
		return m, tea.Batch(cmds...)
	case RoomDiscoveredMsg:
		// Received from the async global discovery channel
		// Pass it down to the HomeView so it can update the search list
		m.homeView.Update(msg)
		// We MUST re-queue the listener to block for the next discovery hit!
		if msg.Ch != nil {
			cmds = append(cmds, m.listenForDiscovery(msg.Ch))
		}
		return m, tea.Batch(cmds...)

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

	switch m.currentView {
	case "splash":
		return m.splashView.View()
	case "setup":
		return m.setupView.View()
	case "home":
		return m.homeView.View()
	case "room":
		return m.roomView.View()
	}

	return fmt.Sprintf("Unknown view: %s", m.currentView)
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
