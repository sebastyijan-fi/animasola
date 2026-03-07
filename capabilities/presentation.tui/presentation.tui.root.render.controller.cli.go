package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	version "github.com/sebastyijan/animasola/capabilities/core.version"
	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	telemetry "github.com/sebastyijan/animasola/capabilities/network.telemetry"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type AppBootstrappedMsg struct {
	Store        *sqlite.Store
	User         *sqlite.User
	Keys         *keys.Keys
	TorConfig    *tor.Config
	TorRunner    *tor.Runner
	ProgressCh   <-chan string
	TorStartTime time.Time
	Version      string
	Registry     *registry.Client
}

type BootstrapErrMsg struct {
	Err error
}

type RootModel struct {
	width  int
	height int

	ctx context.Context

	state          string // "disclaimer", "profile", "bootstrapping", "app"
	disclaimerView *DisclaimerModel
	profileView    *ProfileModel
	appView        *AppModel

	torRunner *tor.Runner
	store     *sqlite.Store
	err       error
}

func NewRootModel(ctx context.Context) *RootModel {
	m := &RootModel{
		ctx:            ctx,
		disclaimerView: NewDisclaimerModel(),
		profileView:    NewProfileModel(),
	}

	// Logic to skip disclaimer if profiles already exist
	configRoot, _ := os.UserConfigDir()
	configDir := filepath.Join(configRoot, "animasola")
	entries, _ := os.ReadDir(configDir)

	hasProfiles := false
	for _, e := range entries {
		if e.IsDir() {
			hasProfiles = true
			break
		}
	}

	if hasProfiles {
		m.state = "profile"
	} else {
		m.state = "disclaimer"
	}

	return m
}

func (m *RootModel) Init() tea.Cmd {
	if m.state == "disclaimer" {
		return m.disclaimerView.Init()
	}
	return m.profileView.Init()
}

func (m *RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.torRunner != nil {
				m.torRunner.Stop()
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.disclaimerView.SetSize(msg.Width, msg.Height)
		m.profileView.SetSize(msg.Width, msg.Height)
		if m.appView != nil {
			m.appView.SetSize(msg.Width, msg.Height)
		}

	case DisclaimerDeclinedMsg:
		return m, tea.Quit

	case DisclaimerAcceptedMsg:
		m.state = "profile"
		return m, m.profileView.Init()

	case ProfileSelectedMsg:
		m.state = "bootstrapping"
		return m, m.bootstrapAppCmd(msg.Username)

	case AppBootstrappedMsg:
		m.store = msg.Store
		m.torRunner = msg.TorRunner
		m.appView = NewAppModel(msg.Store, msg.User, msg.Keys, msg.TorConfig, msg.TorRunner, msg.TorStartTime, msg.ProgressCh, msg.Version, msg.Registry)

		// Pass the existing dimensions so AppModel can cascade them
		if m.width > 0 && m.height > 0 {
			m.appView.SetSize(m.width, m.height)
		}

		m.state = "app"
		return m, m.appView.Init()

	case BootstrapErrMsg:
		m.profileView.SetError(msg.Err)
		m.state = "profile"
		m.err = nil
		return m, m.profileView.Init()
	}

	switch m.state {
	case "disclaimer":
		model, cmd := m.disclaimerView.Update(msg)
		m.disclaimerView = model.(*DisclaimerModel)
		cmds = append(cmds, cmd)
	case "profile":
		model, cmd := m.profileView.Update(msg)
		m.profileView = model.(*ProfileModel)
		cmds = append(cmds, cmd)
	case "app":
		model, cmd := m.appView.Update(msg)
		m.appView = model.(*AppModel)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *RootModel) View() string {
	if m.err != nil {
		errorContent := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Fatal Bootstrap Error:\n%v", m.err))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, errorContent)
	}

	switch m.state {
	case "disclaimer":
		return m.disclaimerView.View()
	case "profile":
		return m.profileView.View()
	case "bootstrapping":
		bootContent := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render("Provisioning Secure Local Environment...")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, bootContent)
	case "app":
		return m.appView.View()
	}

	return ""
}

func (m *RootModel) bootstrapAppCmd(username string) tea.Cmd {
	return func() tea.Msg {
		registryClient := registry.NewClient()
		configRoot, err := os.UserConfigDir()
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error getting config directory: %w", err)}
		}
		configDir := filepath.Join(configRoot, "animasola", username)
		hadLocalKey := keys.HasKey(username)

		keys, err := keys.GetOrGenerateKey(username)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error loading identity: %w", err)}
		}

		hostID, err := keys.PeerID()
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error generating peer ID for database mapping: %w", err)}
		}

		if err := registryClient.RegisterProfile(m.ctx, keys, username, hostID); err != nil {
			if !hadLocalKey {
				_ = os.RemoveAll(configDir)
			}
			return BootstrapErrMsg{err}
		}

		if err := os.MkdirAll(configDir, 0700); err != nil {
			return BootstrapErrMsg{fmt.Errorf("error creating config directory: %w", err)}
		}

		dbPath := filepath.Join(configDir, "animasola.db")
		st, err := sqlite.Open(dbPath)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error opening db: %w", err)}
		}

		if err := st.Migrate(m.ctx); err != nil {
			return BootstrapErrMsg{fmt.Errorf("error migrating db: %w", err)}
		}

		st.StartDataPruning(m.ctx, 30*24*time.Hour)

		user, err := st.GetOrCreateUser(m.ctx, username, hostID)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error getting/creating user: %w", err)}
		}

		torBinary, err := tor.EnsureTorBinary(configDir)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error gathering Tor executable: %w", err)}
		}

		listenPort := 4001 + deterministicOffset(username, 2000)
		socksPort := 45000 + deterministicOffset(username, 10000)
		torConfig, err := tor.GenerateConfig(configDir, listenPort, socksPort)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error configuring Tor wrapper: %w", err)}
		}

		torRunner, progressCh, err := tor.Start(m.ctx, torBinary, torConfig)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error booting Tor engine: %w", err)}
		}

		socksAddr := fmt.Sprintf("socks5://127.0.0.1:%d", torConfig.SocksPort)
		os.Setenv("ALL_PROXY", socksAddr)
		os.Setenv("HTTP_PROXY", socksAddr)
		os.Setenv("HTTPS_PROXY", socksAddr)

		return AppBootstrappedMsg{
			Store:        st,
			User:         user,
			Keys:         keys,
			TorConfig:    torConfig,
			TorRunner:    torRunner,
			ProgressCh:   progressCh,
			TorStartTime: time.Now(), // Fallback if `torStartTime` from RootModel isn't accessible here
			Version:      version.Current,
			Registry:     registryClient,
		}
	}
}

func deterministicOffset(seed string, span int) int {
	offset := 0
	for _, r := range seed {
		offset += int(r)
	}
	return offset % span
}

func (m *RootModel) Shutdown() {
	if m.appView != nil {
		m.appView.Shutdown()
	}
	if m.torRunner != nil {
		m.torRunner.Stop()
	}
	if m.store != nil {
		m.store.Close()
	}
}

func Start(ctx context.Context) *tea.Program {
	return tea.NewProgram(NewRootModel(ctx), tea.WithAltScreen())
}

// GetTelemetry allows the outer main() wrapper to safely extract the Tor telemetry
// engine in the event of an unexpected Bubbletea UI crash (recover block).
func (m *RootModel) GetTelemetry() *telemetry.Service {
	if m.appView != nil && m.appView.node != nil {
		return m.appView.node.Telemetry
	}
	return nil
}

// GetCurrentView returns a debugging string to identify exactly what UI component panicked.
func (m *RootModel) GetCurrentView() string {
	if m.state == "app" && m.appView != nil {
		return fmt.Sprintf("app:%s", m.appView.currentView)
	}
	return m.state
}
