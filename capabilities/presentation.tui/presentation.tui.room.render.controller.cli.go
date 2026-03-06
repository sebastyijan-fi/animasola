package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sebastyijan/animasola/capabilities/network.p2p"
	"github.com/sebastyijan/animasola/capabilities/storage.sqlite"
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

type RoomModel struct {
	sqlite *sqlite.Store
	user   *sqlite.User

	width  int
	height int

	roomID    string
	roomName  string
	isPrivate bool
	roomKey   string

	node        *p2p.Node
	activeTopic *p2p.Room

	feed  Feed
	input ChatInput

	err error // only meant for our explicit errors now
}

func NewRoomModel(s *sqlite.Store, u *sqlite.User, n *p2p.Node) *RoomModel {
	return &RoomModel{
		sqlite: s,
		user:   u,
		node:   n,
		feed:   NewFeed(0, 0),
		input:  NewChatInput(),
	}
}

func (m *RoomModel) SetNode(n *p2p.Node) {
	m.node = n
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

func (m *RoomModel) OpenRoom(id, name string, isPrivate bool, roomKey string) tea.Cmd {
	// If we were already in a different room, guarantee we unsubscribe
	m.Leave()

	m.roomID = id
	m.roomName = name
	m.isPrivate = isPrivate
	m.roomKey = roomKey
	m.feed.SetMessages([]sqlite.FeedMessage{}) // Reset
	m.input.SetValue("")                       // Reset
	return tea.Batch(m.FetchMessages(), tickCmd(), m.subscribeToTopicCmd())
}

func (m *RoomModel) Leave() {
	if m.activeTopic != nil {
		m.activeTopic.LeaveRoom()
		m.activeTopic = nil
	}
}

func (m *RoomModel) subscribeToTopicCmd() tea.Cmd {
	return func() tea.Msg {
		room, err := m.node.JoinRoom(m.roomID, m.isPrivate, m.roomKey)
		if err != nil {
			return ErrMsg{Err: err}
		}

		// Save the active topic
		m.activeTopic = room

		// Start listening for inbound messages in the background
		go room.Listen(m.sqlite, m.node.Host.ID().String(), func(msg sqlite.FeedMessage) {
			// In Bubble Tea, we'd ideally pass this via a msg back to Update,
			// but we currently just rely on tick-based DB polling for simplicity.
			// Or we could trigger a specific message loaded refresh here if needed.
		})

		return nil
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg(t)
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

	case MessagesLoadedMsg:
		m.feed.SetMessages([]sqlite.FeedMessage(msg))

	case tickMsg:
		cmds = append(cmds, m.FetchMessages(), tickCmd())

	case tea.KeyMsg:
		// Route keyboard events: first we check room-level stuff, then we pass to
		switch msg.Type {
		case tea.KeyEsc:
			if m.activeTopic != nil {
				m.activeTopic.LeaveRoom()
				m.activeTopic = nil
			}
			return m, nil // AppModel intercepts this, but we keep it here to prevent it going downward
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

func (m *RoomModel) View() string {
	var s strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, true, false)

	s.WriteString(headerStyle.Render(fmt.Sprintf("# %s", m.roomName)))
	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(fmt.Sprintf(" (ID: %s)", m.roomID)))
	s.WriteString("\n\n")

	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	// Message Feed
	s.WriteString(m.feed.View())
	s.WriteString("\n\n")

	// Input Box
	s.WriteString(lipgloss.NewStyle().MarginLeft(2).Render(m.input.View()))
	s.WriteString("\n")

	// Footer instructions
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	s.WriteString("\n" + metaStyle.Render("(esc to return Home)"))

	// Add overall margin layout
	layout := lipgloss.NewStyle().Margin(1, 1).Render(s.String())
	return layout
}
