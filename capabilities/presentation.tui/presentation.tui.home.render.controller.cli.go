package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type OpenRoomMsg struct {
	RoomID    string
	RoomName  string
	IsPrivate bool
	RoomKey   string
}

type RoomsLoadedMsg []sqlite.Room

// RoomDiscoveredMsg is sent when the background P2P node discovers a new public room
type RoomDiscoveredMsg struct {
	Room sqlite.Room
	Ch   chan sqlite.Room
}

type HomeModel struct {
	sqlite *sqlite.Store
	user   *sqlite.User
	node   *p2p.Node

	width  int
	height int

	mode           string // "pinned" or "search"
	rooms          []sqlite.Room
	allPublicRooms []sqlite.Room
	index          int

	creatingRoom      bool
	joiningRoom       bool
	processing        bool
	confirmingDelete  bool
	roomNameInput     textinput.Model
	roomPasswordInput textinput.Model
	joinIDInput       textinput.Model
	joinPasswordInput textinput.Model
	searchInput       textinput.Model
	err               error
}

func NewHomeModel(s *sqlite.Store, u *sqlite.User, n *p2p.Node) *HomeModel {
	ti := textinput.New()
	ti.Placeholder = "New room name..."
	ti.CharLimit = 32

	si := textinput.New()
	si.Placeholder = "Search or enter Room ID to join..."
	si.CharLimit = 64

	pi := textinput.New()
	pi.Placeholder = "Optional Password (leaves public if empty)"
	pi.CharLimit = 64
	pi.EchoMode = textinput.EchoPassword
	pi.EchoCharacter = '•'

	ji := textinput.New()
	ji.Placeholder = "Enter exact Room ID..."
	ji.CharLimit = 64

	jp := textinput.New()
	jp.Placeholder = "Password (if private)"
	jp.CharLimit = 64
	jp.EchoMode = textinput.EchoPassword
	jp.EchoCharacter = '•'

	return &HomeModel{
		sqlite:            s,
		user:              u,
		node:              n,
		mode:              "pinned",
		roomNameInput:     ti,
		roomPasswordInput: pi,
		joinIDInput:       ji,
		joinPasswordInput: jp,
		searchInput:       si,
	}
}

func (m *HomeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *HomeModel) SetNode(n *p2p.Node) {
	m.node = n
}

func (m *HomeModel) Init() tea.Cmd {
	m.processing = false
	m.err = nil
	return m.FetchRooms()
}

func (m *HomeModel) FetchRooms() tea.Cmd {
	return func() tea.Msg {
		var rooms []sqlite.Room
		var err error

		if m.mode == "search" {
			// Search fetches all public rooms and we filter locally
			rooms, err = m.sqlite.SearchAllRooms(context.Background())
		} else {
			rooms, err = m.sqlite.ListRooms(context.Background(), m.user.ID)
		}

		if err != nil {
			return err
		}
		return RoomsLoadedMsg(rooms)
	}
}

func (m *HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case error:
		m.err = msg
		m.processing = false
		return m, nil

	case RoomsLoadedMsg:
		if m.mode == "search" {
			m.allPublicRooms = []sqlite.Room(msg)

			// Re-apply filter immediately
			query := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
			var filtered []sqlite.Room
			for _, r := range m.allPublicRooms {
				if query == "" || strings.Contains(strings.ToLower(r.Name), query) {
					filtered = append(filtered, r)
				}
			}
			m.rooms = filtered
		} else {
			m.rooms = []sqlite.Room(msg)
		}

		if m.index >= len(m.rooms) {
			m.index = max(0, len(m.rooms)-1)
		}

	case RoomDiscoveredMsg:
		// ALWAYS insert the room into allPublicRooms to keep state fresh, regardless of mode
		inserted := false
		for i, r := range m.allPublicRooms {
			if msg.Room.Name < r.Name {
				m.allPublicRooms = append(m.allPublicRooms[:i], append([]sqlite.Room{msg.Room}, m.allPublicRooms[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			m.allPublicRooms = append(m.allPublicRooms, msg.Room)
		}

		if m.mode == "search" {
			// Re-apply filter immediately so it pops up visually for the user
			query := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
			var filtered []sqlite.Room
			for _, r := range m.allPublicRooms {
				if query == "" || strings.Contains(strings.ToLower(r.Name), query) {
					filtered = append(filtered, r)
				}
			}
			m.rooms = filtered
		}

	case tea.KeyMsg:
		if m.confirmingDelete {
			switch msg.String() {
			case "y", "Y", "enter":
				// Perform delete
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					r := m.rooms[m.index]
					isOwner, err := m.sqlite.IsRoomOwner(context.Background(), m.user.ID, r.ID)
					if err == nil {
						if isOwner {
							_ = m.sqlite.DeleteRoom(context.Background(), r.ID)
						} else {
							_ = m.sqlite.LeaveRoom(context.Background(), m.user.ID, r.ID)
						}
					}
					m.index = 0 // Reset index to avoid out of bounds
				}
				m.confirmingDelete = false
				return m, m.FetchRooms()
			case "n", "N", "esc":
				m.confirmingDelete = false
			}
			return m, nil
		} else if m.creatingRoom {
			switch msg.String() {
			case "esc":
				m.creatingRoom = false
				m.roomNameInput.Blur()
				m.roomPasswordInput.Blur()
				m.roomNameInput.SetValue("")
				m.roomPasswordInput.SetValue("")
			case "tab", "shift+tab", "up", "down":
				if m.roomNameInput.Focused() {
					m.roomNameInput.Blur()
					m.roomPasswordInput.Focus()
				} else {
					m.roomPasswordInput.Blur()
					m.roomNameInput.Focus()
				}
			case "enter":
				name := strings.TrimSpace(m.roomNameInput.Value())
				password := strings.TrimSpace(m.roomPasswordInput.Value())
				if name != "" {
					m.creatingRoom = false
					m.processing = true
					m.roomNameInput.SetValue("")
					m.roomPasswordInput.SetValue("")
					return m, m.createRoom(name, password)
				}
				// Optionally handle empty name gracefully
			default:
				var cmd tea.Cmd
				if m.roomNameInput.Focused() {
					m.roomNameInput, cmd = m.roomNameInput.Update(msg)
				} else {
					m.roomPasswordInput, cmd = m.roomPasswordInput.Update(msg)
				}
				cmds = append(cmds, cmd)
			}
		} else if m.joiningRoom {
			switch msg.String() {
			case "esc":
				m.joiningRoom = false
				m.joinIDInput.Blur()
				m.joinPasswordInput.Blur()
				m.joinIDInput.SetValue("")
				m.joinPasswordInput.SetValue("")
			case "tab", "shift+tab", "up", "down":
				if m.joinIDInput.Focused() {
					m.joinIDInput.Blur()
					m.joinPasswordInput.Focus()
				} else {
					m.joinPasswordInput.Blur()
					m.joinIDInput.Focus()
				}
			case "enter":
				id := strings.TrimSpace(m.joinIDInput.Value())
				password := strings.TrimSpace(m.joinPasswordInput.Value())
				if id != "" {
					m.joiningRoom = false
					m.processing = true
					m.joinIDInput.SetValue("")
					m.joinPasswordInput.SetValue("")
					return m, m.joinRoomByID(id, password)
				}
			default:
				var cmd tea.Cmd
				if m.joinIDInput.Focused() {
					m.joinIDInput, cmd = m.joinIDInput.Update(msg)
				} else {
					m.joinPasswordInput, cmd = m.joinPasswordInput.Update(msg)
				}
				cmds = append(cmds, cmd)
			}
		} else if m.mode == "search" {
			switch msg.String() {
			case "esc":
				m.mode = "pinned"
				m.searchInput.Blur()
				m.searchInput.SetValue("")
				return m, m.FetchRooms()
			case "up", "shift+tab":
				if m.index > 0 {
					m.index--
				}
			case "down", "tab":
				if m.index < len(m.rooms)-1 {
					m.index++
				}
			case "enter":
				// If we have a selected room, join it!
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					r := m.rooms[m.index]
					_ = m.sqlite.JoinRoom(context.Background(), m.user.ID, r.ID)
					_ = m.sqlite.MarkRoomAsRead(context.Background(), m.user.ID, r.ID)
					return m, func() tea.Msg {
						return OpenRoomMsg{
							RoomID:    r.ID,
							RoomName:  r.Name,
							IsPrivate: r.IsPrivate,
							RoomKey:   r.RoomKey,
						}
					}
				} else {
					// Fallback: manually try to join exact name if no rooms listed
					name := strings.TrimSpace(m.searchInput.Value())
					if name != "" {
						m.searchInput.Blur()
						return m, m.joinRoomByName(name)
					}
				}
			default:
				var cmd tea.Cmd
				oldQuery := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
				m.searchInput, cmd = m.searchInput.Update(msg)
				cmds = append(cmds, cmd)

				// Re-filter locally so we don't query DB on every keystroke
				query := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))

				// If the query changed, reset the cursor to the top of the suggestions!
				if query != oldQuery {
					m.index = 0
				}
				var filtered []sqlite.Room
				for _, r := range m.allPublicRooms {
					if query == "" || strings.Contains(strings.ToLower(r.Name), query) {
						filtered = append(filtered, r)
					}
				}
				m.rooms = filtered
				if m.index >= len(m.rooms) {
					m.index = max(0, len(m.rooms)-1)
				}
			}
		} else {
			switch msg.String() {
			case "up", "k":
				if m.index > 0 {
					m.index--
				}
			case "down", "j":
				if m.index < len(m.rooms)-1 {
					m.index++
				}
			case "enter":
				if len(m.rooms) > 0 {
					return m, func() tea.Msg {
						_ = m.sqlite.JoinRoom(context.Background(), m.user.ID, m.rooms[m.index].ID)
						_ = m.sqlite.MarkRoomAsRead(context.Background(), m.user.ID, m.rooms[m.index].ID)

						return OpenRoomMsg{
							RoomID:    m.rooms[m.index].ID,
							RoomName:  m.rooms[m.index].Name,
							IsPrivate: m.rooms[m.index].IsPrivate,
							RoomKey:   m.rooms[m.index].RoomKey,
						}
					}
				}
			case "c":
				m.creatingRoom = true
				m.roomNameInput.Focus()
				cmds = append(cmds, textinput.Blink)
			case "s":
				m.mode = "search"
				m.searchInput.Focus()
				cmds = append(cmds, textinput.Blink)
				cmds = append(cmds, m.FetchRooms()) // Refreshes list to show all for finding
			case "i":
				m.joiningRoom = true
				m.joinIDInput.Focus()
				cmds = append(cmds, textinput.Blink)
			case "x", "delete":
				if len(m.rooms) > 0 {
					m.confirmingDelete = true
				}
			case "esc":
				if m.mode == "search" {
					m.mode = "pinned"
					return m, m.FetchRooms()
				}
			case "q", "quit":
				return m, tea.Quit
			}
		}

	default:
		// Required to keep text inputs blinking and handling internal signals
		var cmd tea.Cmd
		if m.mode == "search" {
			m.searchInput, cmd = m.searchInput.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.creatingRoom {
			m.roomNameInput, cmd = m.roomNameInput.Update(msg)
			cmds = append(cmds, cmd)

			var cmd2 tea.Cmd
			m.roomPasswordInput, cmd2 = m.roomPasswordInput.Update(msg)
			cmds = append(cmds, cmd2)
		} else if m.joiningRoom {
			m.joinIDInput, cmd = m.joinIDInput.Update(msg)
			cmds = append(cmds, cmd)

			var cmd2 tea.Cmd
			m.joinPasswordInput, cmd2 = m.joinPasswordInput.Update(msg)
			cmds = append(cmds, cmd2)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *HomeModel) createRoom(name, password string) tea.Cmd {
	return func() tea.Msg {
		isPrivate := password != ""
		r, err := m.sqlite.CreateRoom(context.Background(), name, "", m.user.ID, isPrivate, password)
		if err != nil {
			return err
		}

		if !isPrivate && m.node != nil {
			_ = m.node.BroadcastRoomDiscovery(context.Background(), r)
		}

		return OpenRoomMsg{
			RoomID:    r.ID,
			RoomName:  r.Name,
			IsPrivate: isPrivate,
			RoomKey:   r.RoomKey,
		}
	}
}

func (m *HomeModel) joinRoomByID(id, password string) tea.Cmd {
	return func() tea.Msg {
		isPrivate := password != ""
		r, err := m.sqlite.JoinExternalRoom(context.Background(), id, "Remote Room", m.user.ID, isPrivate, password)
		if err != nil {
			return err
		}
		return OpenRoomMsg{
			RoomID:    r.ID,
			RoomName:  r.Name,
			IsPrivate: isPrivate,
			RoomKey:   password,
		}
	}
}

// joinRoomByName is a shortcut for the Search bar to force-join a room by exact name
func (m *HomeModel) joinRoomByName(name string) tea.Cmd {
	return func() tea.Msg {
		// First try to find it locally
		rooms, _ := m.sqlite.SearchAllRooms(context.Background())
		for _, r := range rooms {
			if r.Name == name {
				_ = m.sqlite.JoinRoom(context.Background(), m.user.ID, r.ID)
				_ = m.sqlite.MarkRoomAsRead(context.Background(), m.user.ID, r.ID)
				return OpenRoomMsg{
					RoomID:    r.ID,
					RoomName:  r.Name,
					IsPrivate: r.IsPrivate,
					RoomKey:   r.RoomKey,
				}
			}
		}

		// If the room wasn't found in discovery, we cannot blindly join it
		// Return nil so the UI doesn't do anything (or we could return an error message)
		return nil
	}
}

func (m *HomeModel) View() string {
	var s strings.Builder

	styleHeader := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).MarginBottom(1)
	styleSelected := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	styleNormal := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	if m.mode == "search" {
		title := "🔍 Search / Join Public Rooms"
		s.WriteString(styleHeader.Render(title))
		s.WriteString("\n\n")
		s.WriteString(m.searchInput.View())
		s.WriteString("\n\n")
	} else {
		title := fmt.Sprintf("Welcome, %s", m.user.Username)
		s.WriteString(styleHeader.Render(title))
		s.WriteString("\n\n")
	}

	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	if len(m.rooms) == 0 {
		if m.mode == "pinned" {
			s.WriteString("You haven't joined any rooms. Press 's' to search or 'c' to create.\n")
		} else {
			s.WriteString("No public rooms found matching your search. Press enter to force-create it.\n")
		}
	} else {
		for i, r := range m.rooms {
			cursor := " "
			style := styleNormal
			if i == m.index {
				cursor = ">"
				style = styleSelected
			}

			unread := ""
			if r.HasUnread {
				unread = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render(" *New")
			}

			s.WriteString(fmt.Sprintf("%s %s%s\n", cursor, style.Render(r.Name), unread))
		}
	}

	s.WriteString("\n\n")

	if m.processing {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true).Render("Deriving Cryptographic Identity...") + "\n(This takes ~2 seconds to defend against offline dictionary attacks)")
	} else if m.confirmingDelete {
		if len(m.rooms) > 0 {
			r := m.rooms[m.index]
			isOwner, _ := m.sqlite.IsRoomOwner(context.Background(), m.user.ID, r.ID)
			action := "Leave"
			if isOwner {
				action = "Delete (WARNING: Local DB only)"
			}
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Are you sure you want to %s '%s'? (y/N)", action, r.Name)))
		}
	} else if m.creatingRoom {
		s.WriteString("Create Room:\n")
		s.WriteString("  Name:     " + m.roomNameInput.View() + "\n")
		s.WriteString("  Password: " + m.roomPasswordInput.View() + "\n")
		s.WriteString("  (tab to switch, esc to cancel, enter to submit)")
	} else if m.joiningRoom {
		s.WriteString("Join by ID:\n")
		s.WriteString("  Room ID:  " + m.joinIDInput.View() + "\n")
		s.WriteString("  Password: " + m.joinPasswordInput.View() + "\n")
		s.WriteString("  (tab to switch, esc to cancel, enter to submit)")
	} else if m.mode == "search" {
		s.WriteString("up/down (or tab): Navigate • esc: Back to Pinned • enter: Join selected (or exact name)")
	} else {
		s.WriteString("j/k: Navigate • enter: Open • s: Search • c: Create • i: Join by ID • x: Delete/Leave • q: Quit")
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s.String())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
