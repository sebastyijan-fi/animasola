package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TryGenerateKeyMsg struct{}
type KeyGeneratedMsg struct{}
type SetupErrorMsg struct{ Err error }

type SetupModel struct {
	width  int
	height int

	status string
	err    error
}

func NewSetupModel() *SetupModel {
	return &SetupModel{
		status: "awaiting_consent",
	}
}

func (m *SetupModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *SetupModel) Init() tea.Cmd {
	return nil
}

func (m *SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case KeyGeneratedMsg:
		m.status = "done" // AppModel will intercept this
		return m, nil

	case SetupErrorMsg:
		m.err = msg.Err
		m.status = "error"
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if m.status == "awaiting_consent" {
				m.status = "generating"
				// The actual generation should happen via a command to avoid blocking the UI thread
				return m, generateKeyCmd()
			}
		}
	}

	return m, nil
}

// generateKeyCmd acts as a placeholder here, AppModel usually injects the real one
// or handles the routing, but for encapsulation we dispatch a message
func generateKeyCmd() tea.Cmd {
	return func() tea.Msg {
		return TryGenerateKeyMsg{}
	}
}

func (m *SetupModel) View() string {
	var s strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).MarginBottom(1)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(m.width - 4)
	highlightStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(1, 2).
		Margin(2, 2)

	switch m.status {
	case "awaiting_consent":
		s.WriteString(titleStyle.Render("Welcome to Animasola"))
		s.WriteString("\n")

		desc := `To join the peer-to-peer network, you need a cryptographic keys.`
		s.WriteString(textStyle.Render(desc) + "\n\n")

		s.WriteString(warningStyle.Render("We will never touch or read your system SSH keys (~/.ssh).") + "\n\n")

		safeDesc := fmt.Sprintf("Animasola will generate an isolated Ed25519 keypair and securely store it in your config directory:\n%s", highlightStyle.Render("$XDG_CONFIG_HOME/animasola/<profile>/id_ed25519"))
		s.WriteString(textStyle.Render(safeDesc) + "\n\n")

		s.WriteString(textStyle.Render("Do you consent to generating this application-specific key?"))
		s.WriteString("\n\n")
		s.WriteString(lipgloss.NewStyle().Bold(true).Render("[Press ENTER to Generate] • [Press 'q' to Abort]"))

	case "generating":
		s.WriteString(titleStyle.Render("Generating Identity..."))
		s.WriteString("\n")
		s.WriteString(textStyle.Render("Please wait while your cryptographic keys are forged."))

	case "error":
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("Error Generating Key"))
		s.WriteString("\n\n")
		s.WriteString(textStyle.Render(m.err.Error()))
		s.WriteString("\n\n(Press ctrl+c to exit)")
	}

	return boxStyle.Render(s.String())
}
