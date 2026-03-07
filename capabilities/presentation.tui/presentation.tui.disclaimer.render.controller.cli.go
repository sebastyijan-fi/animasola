package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DisclaimerAcceptedMsg struct{}
type DisclaimerDeclinedMsg struct{}

type DisclaimerModel struct {
	width  int
	height int
	cursor int // 0 for Accept, 1 for Decline
}

func NewDisclaimerModel() *DisclaimerModel {
	return &DisclaimerModel{cursor: 1} // Default to Decline for safety
}

func (m *DisclaimerModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *DisclaimerModel) Init() tea.Cmd {
	return nil
}

func (m *DisclaimerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "left", "h":
			m.cursor = 0
		case "right", "l":
			m.cursor = 1
		case "enter":
			if m.cursor == 0 {
				return m, func() tea.Msg { return DisclaimerAcceptedMsg{} }
			}
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *DisclaimerModel) View() string {
	styleHeader := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")).MarginBottom(1)
	styleText := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Width(60).Align(lipgloss.Center).MarginBottom(2)
	styleBtn := lipgloss.NewStyle().Padding(0, 2).Border(lipgloss.NormalBorder())
	styleBtnActive := styleBtn.Copy().BorderForeground(lipgloss.Color("205")).Foreground(lipgloss.Color("205")).Bold(true)

	header := styleHeader.Render("Private connection")
	text := styleText.Render("Animasola includes private routing and creates your secure profile on this device.\n\nContinue to start Animasola.")

	var btnAccept, btnDecline string
	if m.cursor == 0 {
		btnAccept = styleBtnActive.Render("[ ACCEPT ]")
		btnDecline = styleBtn.Render("[ DECLINE ]")
	} else {
		btnAccept = styleBtn.Render("[ ACCEPT ]")
		btnDecline = styleBtnActive.Render("[ DECLINE ]")
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Center, btnAccept, "    ", btnDecline)

	ui := lipgloss.JoinVertical(lipgloss.Center, header, text, buttons)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, ui)
}
