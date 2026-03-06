package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TorSplashModel struct {
	spinner    spinner.Model
	progressCh <-chan string
	lastMsg    string
	width      int
	height     int
	err        error

	tips      []string
	activeTip int
}

type TorProgressMsg string
type TorDoneMsg struct{}
type TorErrorMsg error
type TipRotateMsg struct{}

func NewTorSplashModel(ch <-chan string) *TorSplashModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	tips := []string{
		"Starting Animasola's private network runtime...",
		"Tip: Animasola includes Tor internally, so you do not need to manage it yourself.",
		"Fact: Animasola enforces Argon2id to protect Private Rooms from dictionary attacks.",
		"Fact: Because Animasola is decentralized, your IP address is not sent to a central chat server.",
		"Tip: Public rooms appear as the network refreshes around you.",
		"Connecting private network peers...",
	}

	return &TorSplashModel{
		spinner:    s,
		progressCh: ch,
		lastMsg:    "Spawning Tor Background Process...",
		tips:       tips,
		activeTip:  0,
	}
}

func (m *TorSplashModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.listenForTor(),
		m.tickTip(),
	)
}

func (m *TorSplashModel) tickTip() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return TipRotateMsg{}
	})
}

func (m *TorSplashModel) listenForTor() tea.Cmd {
	return func() tea.Msg {
		if m.progressCh == nil {
			return TorDoneMsg{}
		}

		// Block and wait for the next string on the channel, or timeout if frozen
		select {
		case msg, ok := <-m.progressCh:
			if !ok {
				// Channel closed unexpectedly BEFORE we received the 100% signal!
				// This mathematically means the C binary panicked or was killed.
				return TorErrorMsg(fmt.Errorf("tor background process crashed unexpectedly before completing bootstrap"))
			}
			if string(msg) == "SUCCESS_100" {
				return TorDoneMsg{}
			}
			return TorProgressMsg(msg)
		case <-time.After(60 * time.Second):
			return TorErrorMsg(fmt.Errorf("private network startup timed out after 60 seconds. Exit and try again."))
		}
	}
}

func (m *TorSplashModel) Update(msg tea.Msg) (*TorSplashModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case TorErrorMsg:
		m.err = error(msg)
		return m, nil

	case TorProgressMsg:
		m.lastMsg = string(msg)
		// We received a message, we must queue the listener back up
		// to block until the NEXT message comes through!
		return m, m.listenForTor()

	case TipRotateMsg:
		m.activeTip = (m.activeTip + 1) % len(m.tips)
		return m, m.tickTip()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *TorSplashModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("\nPrivate Network Error: %v\nPlease report this issue.", m.err)
	}

	// Dynamic, colorful tip text to mitigate perceived latency abandonment
	tipText := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true).Render(m.tips[m.activeTip])
	torLog := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(m.lastMsg)

	content := fmt.Sprintf("\n\n  %s Starting Private Network...\n\n    %s\n\n    %s\n", m.spinner.View(), torLog, tipText)

	// Center on screen
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}
