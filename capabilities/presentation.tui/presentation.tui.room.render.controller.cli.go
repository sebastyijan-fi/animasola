package tui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	discovery "github.com/sebastyijan/animasola/capabilities/network.discovery"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func logTUIDebug(format string, a ...interface{}) {
	f, err := os.OpenFile("/tmp/network_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G302 -- Enforcing strict local-only permissions
	if err == nil {
		defer f.Close()
		msg := fmt.Sprintf(format, a...)
		f.WriteString(time.Now().Format(time.RFC3339) + " [TUI] " + msg + "\n")
	}
}

type MessagesLoadedMsg []sqlite.FeedMessage

type tickMsg time.Time
type heartbeatMsg struct{}
type roomMetadataUpdatedMsg struct {
	room *sqlite.Room
	err  error
}

type RoomModel struct {
	sqlite *sqlite.Store
	user   *sqlite.User

	width  int
	height int

	roomID     string
	roomName   string
	roomDesc   string
	isPrivate  bool
	roomKey    string
	requireDHT bool // Holds the boolean indicating if we should enforce a Kademlia DHT hit on join

	node        *p2p.Node
	disco       *discovery.Service
	activeTopic *p2p.Room

	feed  Feed
	input ChatInput

	editingMetadata bool
	editNameInput   textinput.Model
	editDescInput   textinput.Model

	err error // only meant for our explicit errors now
}

func NewRoomModel(s *sqlite.Store, u *sqlite.User, n *p2p.Node, d *discovery.Service) *RoomModel {
	nameInput := textinput.New()
	nameInput.Placeholder = "Room name"
	nameInput.CharLimit = 64

	descInput := textinput.New()
	descInput.Placeholder = "Description"
	descInput.CharLimit = 256

	return &RoomModel{
		sqlite:        s,
		user:          u,
		node:          n,
		disco:         d,
		feed:          NewFeed(0, 0),
		input:         NewChatInput(),
		editNameInput: nameInput,
		editDescInput: descInput,
	}
}

func (m *RoomModel) SetNode(n *p2p.Node) {
	m.node = n
}

func (m *RoomModel) SetDiscovery(d *discovery.Service) {
	m.disco = d
}

func (m *RoomModel) SetSize(w, h int) {
	m.width = w
	m.height = h

	// Allocate space: header needs 2 lines, input needs 3 lines + 1 margin, buffer for footer 2 lines. Total non-feed ~ 8 lines.
	feedHeight := h - 8
	if feedHeight < 0 {
		feedHeight = 0
	}

	feedWidth := w - 4
	if feedWidth < 0 {
		feedWidth = 0
	}

	inputWidth := w - 6
	if inputWidth < 0 {
		inputWidth = 0
	}

	m.feed.SetSize(feedWidth, feedHeight) // leave some padding on sides
	m.input.SetSize(inputWidth, 3)
}

func (m *RoomModel) Init() tea.Cmd {
	return tea.Batch(m.feed.Init(), m.input.Init())
}

func (m *RoomModel) OpenRoom(id, name string, isPrivate bool, roomKey string, requireDHT bool) tea.Cmd {
	// If we were already in a different room, guarantee we unsubscribe
	m.Leave()

	m.roomID = id
	m.roomName = name
	m.roomDesc = ""
	m.isPrivate = isPrivate
	m.roomKey = roomKey
	m.requireDHT = requireDHT
	m.editingMetadata = false
	m.feed.SetMessages([]sqlite.FeedMessage{}) // Reset
	m.input.SetValue("")                       // Reset
	m.editNameInput.SetValue(name)
	m.editDescInput.SetValue("")
	if m.sqlite != nil {
		if room, err := m.sqlite.GetRoom(context.Background(), id); err == nil {
			m.roomName = room.Name
			m.roomDesc = room.Description
			m.isPrivate = room.IsPrivate
			m.editNameInput.SetValue(room.Name)
			m.editDescInput.SetValue(room.Description)
		}
	}

	cmds := []tea.Cmd{m.FetchMessages(), tickCmd(), m.subscribeToTopicCmd()}
	if !isPrivate {
		// UX HARDENING: Fire a 0-second ping instantly so the room appears on the Global Search DHT
		// without forcing the user to wait up to 4 minutes for the first random jitter heartbeat loop.
		r := &sqlite.Room{
			ID:        id,
			Name:      name,
			IsPrivate: false,
			CreatedAt: time.Now().UTC(),
		}
		if m.disco != nil {
			go m.disco.BroadcastRoom(context.Background(), r)
		}

		// Queue up the long-term randomized background heartbeats to keep it alive
		cmds = append(cmds, heartbeatCmd())
	}
	return tea.Batch(cmds...)
}

func (m *RoomModel) Leave() {
	if m.activeTopic != nil {
		m.activeTopic.LeaveRoom()
		m.activeTopic = nil
	}
}

func (m *RoomModel) subscribeToTopicCmd() tea.Cmd {
	return func() tea.Msg {
		room, err := m.node.JoinRoom(m.roomID, m.isPrivate, m.roomKey, m.requireDHT)
		if err != nil {
			// UX HARDENING: If DHT resolution fails on a ghost room, return an explicitly typed error
			// so the AppModel can catch it, delete the local DB record, and kick the user back to Home.
			return ErrMsg{Err: err}
		}

		// Save the active topic
		m.activeTopic = room

		// Start listening for inbound messages in the background
		go room.Listen(m.sqlite, m.node.Host.ID().String(), func(msg sqlite.FeedMessage) {})

		return nil
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func heartbeatCmd() tea.Cmd {
	// Base interval 3 minutes, plus jitter up to 60s
	delay := time.Minute*3 + time.Second*time.Duration(rand.Intn(60))
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return heartbeatMsg{}
	})
}

func (m *RoomModel) FetchMessages() tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.sqlite.ListMessages(context.Background(), m.roomID, 100)
		if err != nil {
			return ErrMsg{Err: err}
		}

		// Since we fetched messages, we can mark this room as read
		_ = m.sqlite.MarkRoomAsRead(context.Background(), m.user.ID, m.roomID)

		return MessagesLoadedMsg(msgs)
	}
}

func (m *RoomModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ErrMsg:
		m.err = msg.Err
		return m, nil

	case roomMetadataUpdatedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if msg.room != nil {
			m.roomName = msg.room.Name
			m.roomDesc = msg.room.Description
			m.editNameInput.SetValue(msg.room.Name)
			m.editDescInput.SetValue(msg.room.Description)
		}
		m.editingMetadata = false
		return m, nil

	case MessagesLoadedMsg:
		m.feed.SetMessages([]sqlite.FeedMessage(msg))

	case tickMsg:
		cmds = append(cmds, m.FetchMessages(), tickCmd())

	case heartbeatMsg:
		if !m.isPrivate && m.activeTopic != nil {
			peers := len(m.activeTopic.Topic.ListPeers())
			var prob float32 = 1.0
			if peers >= 10 {
				prob = 0.10
			} else if peers >= 3 {
				prob = 0.50
			}

			if rand.Float32() <= prob {
				r := &sqlite.Room{
					ID:        m.roomID,
					Name:      m.roomName,
					IsPrivate: false,
					CreatedAt: time.Now().UTC(),
				}
				if m.disco != nil {
					go m.disco.BroadcastRoom(context.Background(), r)
				}
				logTUIDebug("Heartbeat sent! Jitter interval resolved. Peers: %d, Prob: %.2f", peers, prob)
			} else {
				logTUIDebug("Heartbeat skipped (subsampling threshold). Peers: %d, Prob: %.2f", peers, prob)
			}
			cmds = append(cmds, heartbeatCmd())
		}

	case tea.KeyMsg:
		if m.editingMetadata {
			switch msg.String() {
			case "esc":
				m.editingMetadata = false
				m.editNameInput.Blur()
				m.editDescInput.Blur()
				return m, nil
			case "tab", "shift+tab", "up", "down":
				if m.editNameInput.Focused() {
					m.editNameInput.Blur()
					m.editDescInput.Focus()
				} else {
					m.editDescInput.Blur()
					m.editNameInput.Focus()
				}
				return m, nil
			case "enter":
				name := strings.TrimSpace(m.editNameInput.Value())
				desc := strings.TrimSpace(m.editDescInput.Value())
				if name == "" {
					m.err = fmt.Errorf("room name cannot be empty")
					return m, nil
				}
				return m, m.updateMetadataCmd(name, desc)
			default:
				var cmd tea.Cmd
				if m.editNameInput.Focused() {
					m.editNameInput, cmd = m.editNameInput.Update(msg)
				} else {
					m.editDescInput, cmd = m.editDescInput.Update(msg)
				}
				return m, cmd
			}
		}

		// Route keyboard events: first we check room-level stuff, then we pass to
		switch msg.Type {
		case tea.KeyEsc:
			if m.activeTopic != nil {
				m.activeTopic.LeaveRoom()
				m.activeTopic = nil
			}
			return m, nil // AppModel intercepts this, but we keep it here to prevent it going downward
		}
		switch msg.String() {
		case "e":
			if !m.isPrivate && m.disco != nil && m.sqlite != nil {
				isOwner, err := m.sqlite.IsRoomOwner(context.Background(), m.user.ID, m.roomID)
				if err == nil && isOwner {
					m.editingMetadata = true
					m.editNameInput.SetValue(m.roomName)
					m.editDescInput.SetValue(m.roomDesc)
					m.editNameInput.Focus()
					m.editDescInput.Blur()
					return m, textinput.Blink
				}
			}
		}

		// Pass to Input
		var cmd tea.Cmd
		var submitted bool
		m.input, cmd, submitted = m.input.Update(msg)
		cmds = append(cmds, cmd)

		if submitted {
			content := strings.TrimSpace(m.input.Value())
			logTUIDebug("Submitted Input: '%s'", content)
			if content != "" {
				m.input.SetValue("")
				cmds = append(cmds, m.sendMessage(content))
			}
		}

		// Pass to Feed (for scrolling logic, mostly Up/Down/PgUp/PgDown/Wheel)
		m.feed, cmd = m.feed.Update(msg)
		cmds = append(cmds, cmd)

	case tea.MouseMsg:
		// Pass to Feed for mouse wheel scrolling
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *RoomModel) sendMessage(content string) tea.Cmd {
	return func() tea.Msg {
		msg, err := m.sqlite.CreateMessage(context.Background(), m.roomID, m.user.ID, content)
		if err != nil {
			return ErrMsg{Err: err}
		}

		// Broadcast to P2P Swarm async
		if m.activeTopic != nil {
			go m.activeTopic.Publish(context.Background(), msg, m.roomName, m.user.Username)
		}

		msgs, err := m.sqlite.ListMessages(context.Background(), m.roomID, 100)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return MessagesLoadedMsg(msgs)
	}
}

func (m *RoomModel) updateMetadataCmd(name, description string) tea.Cmd {
	return func() tea.Msg {
		if m.disco == nil {
			return roomMetadataUpdatedMsg{err: fmt.Errorf("discovery service unavailable")}
		}
		room, err := m.disco.UpdatePublicRoomMetadata(context.Background(), m.roomID, m.user.ID, name, description)
		return roomMetadataUpdatedMsg{room: room, err: err}
	}
}

func (m *RoomModel) View() string {
	var s strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, true, false)

	s.WriteString(headerStyle.Render(fmt.Sprintf("# %s", m.roomName)))
	s.WriteString("\n\n")

	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	// Message Feed
	if m.editingMetadata {
		s.WriteString("Edit Public Room Metadata:\n")
		s.WriteString("  Name:        " + m.editNameInput.View() + "\n")
		s.WriteString("  Description: " + m.editDescInput.View() + "\n")
		s.WriteString("  (tab to switch, esc to cancel, enter to save)\n\n")
	} else {
		s.WriteString(m.feed.View())
	}
	s.WriteString("\n\n")

	// Input Box
	if !m.editingMetadata {
		s.WriteString(lipgloss.NewStyle().MarginLeft(2).Render(m.input.View()))
		s.WriteString("\n")
	}

	// Footer instructions
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	footer := "(esc to return Home)"
	if !m.isPrivate {
		footer = "(e to edit public room metadata, esc to return Home)"
	}
	s.WriteString("\n" + metaStyle.Render(footer))

	// Add overall margin layout
	layout := lipgloss.NewStyle().Margin(1, 1).Render(s.String())
	return layout
}
