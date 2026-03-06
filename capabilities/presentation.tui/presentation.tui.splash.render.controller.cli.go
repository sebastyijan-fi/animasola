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
}

type TorProgressMsg string
type TorDoneMsg struct{}
type TorErrorMsg error

func NewTorSplashModel(ch <-chan string) *TorSplashModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return &TorSplashModel{
		spinner:    s,
		progressCh: ch,
		lastMsg:    "Spawning Tor Background Process...",
	}
}

func (m *TorSplashModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.listenForTor(),
	)
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
			return TorErrorMsg(fmt.Errorf("tor bootstrap timed out (frozen for 60s, possible SOCKS port collision). Exit and try again."))
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

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *TorSplashModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("\nFatal Tor Error: %v\nPlease report this issue.", m.err)
	}

	content := fmt.Sprintf("\n\n  %s Bootstrapping Tor Anonymity Engine...\n\n    %s\n", m.spinner.View(), lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.lastMsg))

	// Center on screen
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}
