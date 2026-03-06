package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ProfileSelectedMsg struct {
	Username string
}

type ProfileModel struct {
	width  int
	height int

	profiles []string
	index    int

	creatingNew bool
	nameInput   textinput.Model
}

func NewProfileModel() *ProfileModel {
	ti := textinput.New()
	ti.Placeholder = "Enter new identity name..."
	ti.CharLimit = 32

	m := &ProfileModel{
		nameInput: ti,
	}
	m.loadProfiles()
	if len(m.profiles) == 0 {
		m.creatingNew = true
	}
	return m
}

func (m *ProfileModel) loadProfiles() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configDir := filepath.Join(homeDir, ".config", "animasola")
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return
	}

	var profiles []string
	for _, e := range entries {
		if e.IsDir() {
			profiles = append(profiles, e.Name())
		}
	}
	m.profiles = profiles
}

func (m *ProfileModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *ProfileModel) Init() tea.Cmd {
	if m.creatingNew {
		m.nameInput.Focus()
		return textinput.Blink
	}
	return nil
}

func (m *ProfileModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.creatingNew {
			switch msg.String() {
			case "esc":
				if len(m.profiles) > 0 {
					m.creatingNew = false
				} else {
					return m, tea.Quit
				}
			case "enter":
				name := strings.TrimSpace(m.nameInput.Value())
				// basic sanitization to prevent path injection
				name = strings.ReplaceAll(name, "/", "")
				name = strings.ReplaceAll(name, "\\", "")
				if name != "" {
					return m, func() tea.Msg { return ProfileSelectedMsg{Username: name} }
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.nameInput, cmd = m.nameInput.Update(msg)
				cmds = append(cmds, cmd)
			}
		} else {
			switch msg.String() {
			case "up", "k":
				if m.index > 0 {
					m.index--
				}
			case "down", "j":
				if m.index < len(m.profiles)-1 {
					m.index++
				}
			case "c":
				m.creatingNew = true
				m.nameInput.Focus()
				cmds = append(cmds, textinput.Blink)
			case "enter":
				if len(m.profiles) > 0 && m.index < len(m.profiles) {
					return m, func() tea.Msg { return ProfileSelectedMsg{Username: m.profiles[m.index]} }
				}
			case "q", "esc", "ctrl+c":
				return m, tea.Quit
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *ProfileModel) View() string {
	var s strings.Builder

	styleHeader := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).MarginBottom(2)
	styleSelected := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	styleNormal := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	s.WriteString(styleHeader.Render("🔮 Select Cryptographic Identity"))
	s.WriteString("\n\n")

	if m.creatingNew {
		s.WriteString("Create New Profile:\n\n")
		s.WriteString(m.nameInput.View() + "\n\n")
		s.WriteString(styleNormal.Render("(enter to submit, esc to cancel)"))
	} else {
		for i, p := range m.profiles {
			cursor := " "
			style := styleNormal
			if i == m.index {
				cursor = ">"
				style = styleSelected
			}
			s.WriteString(fmt.Sprintf("%s %s\n", cursor, style.Render(p)))
		}
		s.WriteString("\n")
		s.WriteString(styleNormal.Render("up/down: navigate • enter: select • c: new identity • q: quit"))
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s.String())
}
