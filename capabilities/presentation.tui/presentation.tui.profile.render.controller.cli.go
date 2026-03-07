package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
)

type ProfileSelectedMsg struct {
	Username string
}

type ProfileModel struct {
	width  int
	height int

	profiles []string
	index    int

	creatingNew      bool
	confirmingDelete bool
	nameInput        textinput.Model
	errText          string
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
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return
	}
	configDir := filepath.Join(configRoot, "animasola")
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
				name = strings.ReplaceAll(name, ".", "")
				if name != "" {
					m.errText = ""
					return m, func() tea.Msg { return ProfileSelectedMsg{Username: name} }
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				m.errText = ""
				var cmd tea.Cmd
				m.nameInput, cmd = m.nameInput.Update(msg)
				cmds = append(cmds, cmd)
			}
		} else if m.confirmingDelete {
			switch msg.String() {
			case "y", "Y":
				if len(m.profiles) > 0 && m.index < len(m.profiles) {
					username := m.profiles[m.index]
					identity, err := keys.LoadKey(username)
					if err == nil {
						peerID, err := identity.PeerID()
						if err == nil {
							err = registry.NewClient().ReleaseProfile(context.Background(), identity, username, peerID)
						}
					}
					if err != nil && !errors.Is(err, registry.ErrProfileNotRegistered) {
						m.confirmingDelete = false
						m.errText = err.Error()
						return m, nil
					}

					configRoot, err := os.UserConfigDir()
					if err == nil {
						targetProfile := filepath.Join(configRoot, "animasola", username)
						_ = os.RemoveAll(targetProfile)
					}
					m.index = 0
					m.confirmingDelete = false
					m.errText = ""
					m.loadProfiles()
				}
			case "n", "N", "esc":
				m.confirmingDelete = false
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
			case "x":
				if len(m.profiles) > 0 {
					m.confirmingDelete = true
				}
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
	styleError := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	s.WriteString(styleHeader.Render("🔮 Select Cryptographic Identity"))
	s.WriteString("\n\n")

	if m.creatingNew {
		s.WriteString("Create New Profile:\n\n")
		if m.errText != "" {
			s.WriteString(styleError.Render(m.errText) + "\n\n")
		}
		s.WriteString(m.nameInput.View() + "\n\n")
		s.WriteString(styleNormal.Render("(enter to submit, esc to cancel)"))
	} else if m.confirmingDelete {
		for i, p := range m.profiles {
			if i == m.index {
				s.WriteString(fmt.Sprintf("> %s\n", styleSelected.Render(p)))
			} else {
				s.WriteString(fmt.Sprintf("  %s\n", styleNormal.Render(p)))
			}
		}
		s.WriteString("\n")
		warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
		s.WriteString(warningStyle.Render(fmt.Sprintf("Delete profile '%s'?\nAll keys and history will be permanently lost. (y/n)", m.profiles[m.index])))
	} else {
		if m.errText != "" {
			s.WriteString(styleError.Render(m.errText) + "\n\n")
		}
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
		s.WriteString(styleNormal.Render("up/down: navigate • enter: select • c: new identity • x: delete • q: quit"))
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s.String())
}

func (m *ProfileModel) SetError(err error) {
	if err == nil {
		m.errText = ""
		return
	}
	m.creatingNew = true
	m.errText = err.Error()
}
