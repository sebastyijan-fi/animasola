package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	tor "github.com/sebastyijan/animasola/capabilities/network.tor"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type AppBootstrappedMsg struct {
	Store      *sqlite.Store
	User       *sqlite.User
	Keys       *keys.Keys
	TorConfig  *tor.Config
	TorRunner  *tor.Runner
	ProgressCh <-chan string
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
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".config", "animasola")
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
		m.appView = NewAppModel(msg.Store, msg.User, msg.Keys, msg.TorConfig, msg.ProgressCh)

		// Pass the existing dimensions so AppModel can cascade them
		if m.width > 0 && m.height > 0 {
			m.appView.SetSize(m.width, m.height)
		}

		m.state = "app"
		return m, m.appView.Init()

	case BootstrapErrMsg:
		m.err = msg.Err
		return m, nil
	}

	if m.state == "disclaimer" {
		model, cmd := m.disclaimerView.Update(msg)
		m.disclaimerView = model.(*DisclaimerModel)
		cmds = append(cmds, cmd)
	} else if m.state == "profile" {
		model, cmd := m.profileView.Update(msg)
		m.profileView = model.(*ProfileModel)
		cmds = append(cmds, cmd)
	} else if m.state == "app" {
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

	if m.state == "disclaimer" {
		return m.disclaimerView.View()
	} else if m.state == "profile" {
		return m.profileView.View()
	} else if m.state == "bootstrapping" {
		bootContent := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render("Provisioning Secure Local Environment...")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, bootContent)
	} else if m.state == "app" {
		return m.appView.View()
	}

	return ""
}

func (m *RootModel) bootstrapAppCmd(username string) tea.Cmd {
	return func() tea.Msg {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error getting home directory: %w", err)}
		}
		configDir := filepath.Join(homeDir, ".config", "animasola", username)
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

		user, err := st.GetOrCreateUser(m.ctx, username)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error getting/creating user: %w", err)}
		}

		keys, err := keys.GetOrGenerateKey(username)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error loading identity: %w", err)}
		}

		torBinary, err := tor.EnsureTorBinary(configDir)
		if err != nil {
			return BootstrapErrMsg{fmt.Errorf("error gathering Tor executable: %w", err)}
		}

		socksPort := 45000 + int(time.Now().UnixMilli()%10000)
		torConfig, err := tor.GenerateConfig(configDir, 4001, socksPort)
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
			Store:      st,
			User:       user,
			Keys:       keys,
			TorConfig:  torConfig,
			TorRunner:  torRunner,
			ProgressCh: progressCh,
		}
	}
}

func Start(ctx context.Context) *tea.Program {
	return tea.NewProgram(NewRootModel(ctx), tea.WithAltScreen())
}
