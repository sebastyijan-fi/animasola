package tui

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	discovery "github.com/sebastyijan/animasola/capabilities/network.discovery"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type OpenRoomMsg struct {
	RoomID     string
	RoomName   string
	IsPrivate  bool
	RoomKey    string
	RequireDHT bool
}

type RoomsLoadedMsg []sqlite.Room

type SearchRoomsLoadedMsg struct {
	Query string
	Rooms []sqlite.Room
}

type RoomCreatedNeedsSyncMsg struct{ Room *sqlite.Room }

type RoomJoinNeedsSyncMsg struct {
	RoomID     string
	RoomName   string
	IsPrivate  bool
	RoomKey    string
	RequireDHT bool
}

// RoomDiscoveredMsg is sent when the background P2P node discovers a new public room
type RoomDiscoveredMsg struct {
	Room sqlite.Room
	Ch   chan sqlite.Room
}

type clearToastMsg struct{}

func clearToastCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(time.Time) tea.Msg {
		return clearToastMsg{}
	})
}

type roomPolicyUpdatedMsg struct {
	err error
}

type homePanelState string

const (
	homePanelPinned         homePanelState = "pinned"
	homePanelSearch         homePanelState = "search"
	homePanelCreate         homePanelState = "create"
	homePanelJoin           homePanelState = "join"
	homePanelInfo           homePanelState = "info"
	homePanelConfirmDelete  homePanelState = "confirm_delete"
	homePanelSyncing        homePanelState = "syncing"
	homePanelResolving      homePanelState = "resolving"
	homePanelProcessing     homePanelState = "processing"
)

type HomeModel struct {
	sqlite *sqlite.Store
	user   *sqlite.User
	keys   *keys.Keys
	node   *p2p.Node
	disco  *discovery.Service
	registry *registry.Client

	width  int
	height int

	mode           string // "pinned" or "search"
	rooms          []sqlite.Room
	allPublicRooms []sqlite.Room
	index          int

	creatingRoom      bool
	joiningRoom       bool
	syncingRoom       bool
	resolvingRoom     bool
	showingInfo       bool // Whether the Room Info Modal is open
	copiedToast       bool // True if the room ID was just copied
	processing        bool
	busyAction        string
	confirmingDelete  bool
	roomNameInput     textinput.Model
	roomPasswordInput textinput.Model
	joinIDInput       textinput.Model
	joinPasswordInput textinput.Model
	searchInput       textinput.Model
	err               error
	updateVersion     string
}

func NewHomeModel(s *sqlite.Store, u *sqlite.User, identity *keys.Keys, n *p2p.Node, d *discovery.Service, registryClient *registry.Client) *HomeModel {
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
		keys:              identity,
		node:              n,
		disco:             d,
		registry:          registryClient,
		mode:              "pinned",
		roomNameInput:     ti,
		roomPasswordInput: pi,
		joinIDInput:       ji,
		joinPasswordInput: jp,
		searchInput:       si,
	}
}

func (m *HomeModel) panelState() homePanelState {
	switch {
	case m.confirmingDelete:
		return homePanelConfirmDelete
	case m.showingInfo:
		return homePanelInfo
	case m.syncingRoom:
		return homePanelSyncing
	case m.resolvingRoom:
		return homePanelResolving
	case m.processing && m.busyAction != "":
		return homePanelProcessing
	case m.creatingRoom:
		return homePanelCreate
	case m.joiningRoom:
		return homePanelJoin
	case m.mode == "search":
		return homePanelSearch
	default:
		return homePanelPinned
	}
}

func (m *HomeModel) processingTitle() string {
	switch m.busyAction {
	case "sync":
		return "Making room available..."
	case "join", "resolve":
		return "Joining room..."
	case "create":
		return "Creating your room..."
	default:
		return "Working..."
	}
}

func (m *HomeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *HomeModel) SetNode(n *p2p.Node) {
	m.node = n
}

func (m *HomeModel) SetDiscovery(d *discovery.Service) {
	m.disco = d
}

func (m *HomeModel) Init() tea.Cmd {
	m.processing = false
	m.busyAction = ""
	m.err = nil
	return m.FetchRooms()
}

func (m *HomeModel) FetchRooms() tea.Cmd {
	return func() tea.Msg {
		var rooms []sqlite.Room
		var err error

		if m.mode == "search" {
			query := strings.TrimSpace(m.searchInput.Value())
			if query == "" {
				return SearchRoomsLoadedMsg{Query: query, Rooms: nil}
			}
			if m.registry != nil {
				registryRooms, registryErr := m.registry.SearchPublicRooms(context.Background(), query)
				if registryErr == nil {
					rooms = make([]sqlite.Room, 0, len(registryRooms))
					for _, room := range registryRooms {
						rooms = append(rooms, sqlite.Room{
							ID:        room.RoomID,
							Name:      room.Name,
							CreatorID: room.PeerID,
							IsPrivate: false,
						})
					}
					return SearchRoomsLoadedMsg{Query: query, Rooms: rooms}
				}
			}
			rooms, err = m.sqlite.SearchAllRooms(context.Background())
			if err != nil {
				return err
			}
			return SearchRoomsLoadedMsg{Query: query, Rooms: filterSearchVisiblePublicRooms(rooms, query)}
		} else {
			rooms, err = m.sqlite.ListRooms(context.Background(), m.user.ID)
		}

		if err != nil {
			return err
		}
		return RoomsLoadedMsg(rooms)
	}
}

func filterSearchVisiblePublicRooms(rooms []sqlite.Room, rawQuery string) []sqlite.Room {
	query := strings.ToLower(strings.TrimSpace(rawQuery))
	if query == "" {
		return nil
	}

	filtered := make([]sqlite.Room, 0, min(len(rooms), 20))
	for _, r := range rooms {
		if strings.Contains(strings.ToLower(r.Name), query) {
			filtered = append(filtered, r)
			if len(filtered) == 20 {
				break
			}
		}
	}

	return filtered
}

func (m *HomeModel) setRoomHidden(roomID string, hidden bool) tea.Cmd {
	return func() tea.Msg {
		return roomPolicyUpdatedMsg{err: m.sqlite.SetRoomHidden(context.Background(), roomID, hidden)}
	}
}

func (m *HomeModel) setRoomTrusted(roomID string, trusted bool) tea.Cmd {
	return func() tea.Msg {
		return roomPolicyUpdatedMsg{err: m.sqlite.SetRoomTrusted(context.Background(), roomID, trusted)}
	}
}

func (m *HomeModel) setPeerMuted(peerID string, muted bool) tea.Cmd {
	return func() tea.Msg {
		return roomPolicyUpdatedMsg{err: m.sqlite.SetPeerMuted(context.Background(), peerID, muted)}
	}
}

func (m *HomeModel) setPeerBlocked(peerID string, blocked bool) tea.Cmd {
	return func() tea.Msg {
		return roomPolicyUpdatedMsg{err: m.sqlite.SetPeerBlocked(context.Background(), peerID, blocked)}
	}
}

func (m *HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	// A new version was found on GitHub!
	case UpdateAvailableMsg:
		m.updateVersion = msg.Version
		return m, nil

	case clearToastMsg:
		m.copiedToast = false
		return m, nil

	case roomPolicyUpdatedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.showingInfo = false
		m.copiedToast = false
		return m, m.FetchRooms()

	case error:
		m.err = msg
		m.processing = false
		m.busyAction = ""
		m.syncingRoom = false
		m.resolvingRoom = false
		return m, nil

	case RoomsLoadedMsg:
		if m.mode == "search" {
			m.allPublicRooms = []sqlite.Room(msg)
			m.rooms = filterSearchVisiblePublicRooms(m.allPublicRooms, m.searchInput.Value())
		} else {
			m.rooms = []sqlite.Room(msg)
		}

		if m.index >= len(m.rooms) {
			m.index = max(0, len(m.rooms)-1)
		}

	case SearchRoomsLoadedMsg:
		currentQuery := strings.TrimSpace(m.searchInput.Value())
		if m.mode != "search" || strings.TrimSpace(msg.Query) != currentQuery {
			return m, nil
		}
		m.rooms = msg.Rooms
		if m.index >= len(m.rooms) {
			m.index = max(0, len(m.rooms)-1)
		}

	case RoomDiscoveredMsg:
		// ALWAYS update allPublicRooms to keep state fresh, regardless of mode
		isDup := false
		for _, r := range m.allPublicRooms {
			if r.ID == msg.Room.ID {
				isDup = true
				break
			}
		}

		if !isDup {
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
				m.rooms = filterSearchVisiblePublicRooms(m.allPublicRooms, m.searchInput.Value())
			}
		}

	case RoomCreatedNeedsSyncMsg:
		m.processing = false
		m.busyAction = "sync"
		m.syncingRoom = true
		return m, m.syncRoomDiscovery(msg.Room)

	case RoomJoinNeedsSyncMsg:
		m.processing = false
		m.busyAction = "resolve"
		m.resolvingRoom = true
		return m, m.syncRoomJoin(msg)

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
							if !r.IsPrivate && r.CreatorID == m.user.ID {
								if m.registry == nil {
									m.err = fmt.Errorf("public room deletion requires a live registry connection")
									m.confirmingDelete = false
									return m, nil
								}
								if err := m.registry.ReleasePublicRoom(context.Background(), m.keys, m.user.ID, r.ID); err != nil {
									m.err = err
									m.confirmingDelete = false
									return m, nil
								}
							}
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
		} else if m.showingInfo {
			switch msg.String() {
			case "c", "C":
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					_ = clipboard.WriteAll(m.rooms[m.index].ID)
					m.copiedToast = true
					cmds = append(cmds, clearToastCmd())
				}
				return m, tea.Batch(cmds...)
			case "h", "H":
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					return m, m.setRoomHidden(m.rooms[m.index].ID, !m.rooms[m.index].IsHidden)
				}
			case "t", "T":
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					return m, m.setRoomTrusted(m.rooms[m.index].ID, !m.rooms[m.index].IsTrusted)
				}
			case "m", "M":
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					r := m.rooms[m.index]
					if r.CreatorID != "" && r.CreatorID != m.user.ID {
						return m, m.setPeerMuted(r.CreatorID, !r.CreatorMuted)
					}
				}
			case "b", "B":
				if len(m.rooms) > 0 && m.index < len(m.rooms) {
					r := m.rooms[m.index]
					if r.CreatorID != "" && r.CreatorID != m.user.ID {
						return m, m.setPeerBlocked(r.CreatorID, !r.CreatorBlocked)
					}
				}
			case "esc", "i", "enter":
				m.showingInfo = false
			}
			return m, tea.Batch(cmds...)
		} else if m.syncingRoom {
			// CRITICAL: We intentionally block all input while the network is syncing!
			// We only accept 'esc' to cancel the sync and return to the main menu.
			if msg.String() == "esc" {
				m.syncingRoom = false
				m.busyAction = ""
				return m, m.FetchRooms()
			}
			return m, nil
		} else if m.resolvingRoom {
			if msg.String() == "esc" {
				m.resolvingRoom = false
				m.busyAction = ""
				return m, m.FetchRooms()
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
					m.busyAction = "create"
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
					m.busyAction = "join"
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
					if _, err := m.sqlite.JoinExternalRoom(context.Background(), r.ID, r.Name, m.user.ID, r.CreatorID, r.IsPrivate, r.RoomKey); err != nil {
						m.err = err
						return m, nil
					}
					_ = m.sqlite.MarkRoomAsRead(context.Background(), m.user.ID, r.ID)
					return m, func() tea.Msg {
						return OpenRoomMsg{
							RoomID:     r.ID,
							RoomName:   r.Name,
							IsPrivate:  r.IsPrivate,
							RoomKey:    r.RoomKey,
							RequireDHT: false,
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
					cmds = append(cmds, m.FetchRooms())
				}
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
							RoomID:     m.rooms[m.index].ID,
							RoomName:   m.rooms[m.index].Name,
							IsPrivate:  m.rooms[m.index].IsPrivate,
							RoomKey:    m.rooms[m.index].RoomKey,
							RequireDHT: false, // Already locally pinned and known
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
				if len(m.rooms) > 0 {
					m.showingInfo = true
					m.copiedToast = false
				}
			case "p":
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
		startTime := time.Now()
		if !isPrivate && m.registry == nil {
			return fmt.Errorf("public rooms require a live registry connection")
		}
		r, err := m.sqlite.CreateRoom(context.Background(), name, "", m.user.ID, isPrivate, "")
		if err != nil {
			return err
		}
		if !isPrivate {
			if err := m.registry.RegisterPublicRoom(context.Background(), m.keys, m.user.Username, m.user.ID, r.ID, r.Name); err != nil {
				_ = m.sqlite.DeleteRoom(context.Background(), r.ID)
				return err
			}
		}
		if isPrivate {
			r.RoomKey = sqlite.EncodePrivateRoomKey(r.ID, password)
			if err := m.sqlite.UpdateRoomSecret(context.Background(), r.ID, r.RoomKey); err != nil {
				return err
			}
		}
		duration := int(time.Since(startTime).Milliseconds())

		if m.node != nil && m.node.Telemetry != nil && isPrivate {
			m.node.Telemetry.RecordEvent(context.Background(), "Argon2id_Derivation_Latency", duration, map[string]string{
				"action": "room_create",
			}, runtime.GOOS, runtime.GOARCH)
		}

		if !isPrivate && m.disco != nil {
			go m.disco.BroadcastRoom(context.Background(), r)
		}

		return OpenRoomMsg{
			RoomID:     r.ID,
			RoomName:   r.Name,
			IsPrivate:  isPrivate,
			RoomKey:    r.RoomKey,
			RequireDHT: false, // DO NOT timeout the creator just because they have 0 peers!
		}
	}
}

func (m *HomeModel) syncRoomDiscovery(r *sqlite.Room) tea.Cmd {
	return func() tea.Msg {
		if m.disco != nil {
			_ = m.disco.PublishRoomSync(context.Background(), r)
		}

		// Wait loop succeeded (or timed out after 5m), physically open the room now!
		return OpenRoomMsg{
			RoomID:     r.ID,
			RoomName:   r.Name,
			IsPrivate:  false,
			RoomKey:    r.RoomKey,
			RequireDHT: false, // DO NOT timeout the creator just because they have 0 peers!
		}
	}
}

func (m *HomeModel) syncRoomJoin(msg RoomJoinNeedsSyncMsg) tea.Cmd {
	return func() tea.Msg {
		if msg.RequireDHT && !msg.IsPrivate && m.node != nil {
			err := m.node.WaitForRoomPeers(context.Background(), msg.RoomID, 4*time.Minute)
			if err != nil {
				return err // Will be caught by 'case error:' in HomeModel
			}
		}

		return OpenRoomMsg{
			RoomID:     msg.RoomID,
			RoomName:   msg.RoomName,
			IsPrivate:  msg.IsPrivate,
			RoomKey:    msg.RoomKey,
			RequireDHT: false, // We already resolved the network natively!
		}
	}
}

func (m *HomeModel) joinRoomByID(id, password string) tea.Cmd {
	return func() tea.Msg {
		isPrivate := password != ""
		startTime := time.Now()
		storedRoomKey := password
		if isPrivate {
			storedRoomKey = sqlite.EncodePrivateRoomKey(id, password)
		}
		r, err := m.sqlite.JoinExternalRoom(context.Background(), id, "Remote Room", m.user.ID, "", isPrivate, storedRoomKey)
		if err != nil {
			return err
		}
		duration := int(time.Since(startTime).Milliseconds())

		if m.node != nil && m.node.Telemetry != nil && isPrivate {
			m.node.Telemetry.RecordEvent(context.Background(), "Argon2id_Derivation_Latency", duration, map[string]string{
				"action": "room_join",
			}, runtime.GOOS, runtime.GOARCH)
		}

		return OpenRoomMsg{
			RoomID:     r.ID,
			RoomName:   r.Name,
			IsPrivate:  isPrivate,
			RoomKey:    password,
			RequireDHT: false,
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
					RoomID:     r.ID,
					RoomName:   r.Name,
					IsPrivate:  r.IsPrivate,
					RoomKey:    r.RoomKey,
					RequireDHT: false,
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
		title := "Find a room"
		if m.updateVersion != "" {
			s.WriteString(lipgloss.NewStyle().Background(lipgloss.Color("205")).Foreground(lipgloss.Color("232")).Bold(true).Render(fmt.Sprintf(" 🚀 UPDATE AVAILABLE: %s — Check GitHub Releases! ", m.updateVersion)) + "\n\n")
		}
		s.WriteString(styleHeader.Render(title))
		s.WriteString("\n\n")
		s.WriteString(m.searchInput.View())
		s.WriteString("\n\n")
	} else {
		title := fmt.Sprintf("Welcome, %s", m.user.Username)
		if m.updateVersion != "" {
			s.WriteString(lipgloss.NewStyle().Background(lipgloss.Color("205")).Foreground(lipgloss.Color("232")).Bold(true).Render(fmt.Sprintf(" 🚀 UPDATE AVAILABLE: %s — Check GitHub Releases! ", m.updateVersion)) + "\n\n")
		}
		s.WriteString(styleHeader.Render(title))
		s.WriteString("\n\n")
	}

	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	if len(m.rooms) == 0 {
		if m.mode == "pinned" {
			s.WriteString("You have no rooms yet. Press 's' to find a room or 'c' to create one.\n")
		} else {
			if strings.TrimSpace(m.searchInput.Value()) == "" {
				s.WriteString("Type a room name. Press enter to try that exact name.\n")
			} else {
				s.WriteString("No rooms found. Press enter to try that exact name.\n")
			}
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

			prefix := "#"
			if r.IsPrivate {
				prefix = "🔒"
			}

			s.WriteString(fmt.Sprintf("%s %s %s%s\n", cursor, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(prefix), style.Render(r.Name), unread))
		}
	}

	s.WriteString("\n\n")

	switch m.panelState() {
	case homePanelProcessing:
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true).Render(m.processingTitle()))
	case homePanelConfirmDelete:
		if len(m.rooms) > 0 {
			r := m.rooms[m.index]
			isOwner, _ := m.sqlite.IsRoomOwner(context.Background(), m.user.ID, r.ID)
			action := "Leave"
			if isOwner {
				action = "Remove"
			}
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Are you sure you want to %s '%s'? (y/N)", action, r.Name)))
		}
	case homePanelSyncing:
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render("Making room available..."))
		s.WriteString("\n")
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("This can take a minute or two."))
		s.WriteString("\n\n")
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("esc: Cancel for now (the room stays saved locally)"))
	case homePanelResolving:
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render("Joining room..."))
		s.WriteString("\n")
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("This can take a minute or two."))
		s.WriteString("\n\n")
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("esc: Cancel"))
	case homePanelCreate:
		s.WriteString("Create a room\n")
		s.WriteString("  Name:     " + m.roomNameInput.View() + "\n")
		s.WriteString("  Password: " + m.roomPasswordInput.View() + "\n")
		s.WriteString("  Leave password empty for a public room.\n")
		s.WriteString("  tab: switch  esc: cancel  enter: create")
	case homePanelJoin:
		s.WriteString("Join a room\n")
		s.WriteString("  Room ID:  " + m.joinIDInput.View() + "\n")
		s.WriteString("  Password: " + m.joinPasswordInput.View() + "\n")
		s.WriteString("  tab: switch  esc: cancel  enter: join")
	case homePanelInfo:
		if len(m.rooms) > 0 {
			r := m.rooms[m.index]
			creatorText := "You created this room"
			isOwner, err := m.sqlite.IsRoomOwner(context.Background(), m.user.ID, r.ID)
			if err == nil && !isOwner {
				creatorText = "You joined this room"
			}
			roomType := "Public room"
			if r.IsPrivate {
				roomType = "Private room"
			}

			box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
			creatorText = "Joined room"
			isOwner, err = m.sqlite.IsRoomOwner(context.Background(), m.user.ID, r.ID)
			if err == nil {
				if isOwner {
					creatorText = "You created this room"
				} else {
					creatorText = "You joined this room"
				}
			}

			statuses := make([]string, 0, 4)
			if r.IsTrusted {
				statuses = append(statuses, "Trusted")
			}
			if r.IsHidden {
				statuses = append(statuses, "Hidden from public discovery")
			}
			if r.CreatorMuted {
				statuses = append(statuses, "Creator muted")
			}
			if r.CreatorBlocked {
				statuses = append(statuses, "Creator blocked")
			}

			content := fmt.Sprintf("Room Details: %s\n\n%s\n%s\n\nInvitation ID (Share exactly):\n%s\n",
				lipgloss.NewStyle().Bold(true).Render(r.Name),
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(roomType),
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(creatorText),
				r.ID)

			if len(statuses) > 0 {
				content += "\nLocal filters:\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Join(statuses, " • ")) + "\n"
			}

			actions := []string{
				"[h] Hide/Unhide from public discovery",
				"[t] Trust/Untrust room",
			}
			if r.CreatorID != "" && r.CreatorID != m.user.ID {
				actions = append(actions, "[m] Mute/Unmute creator")
				actions = append(actions, "[b] Block/Unblock creator")
			}
			content += "\n" + strings.Join(actions, "\n")

			if m.copiedToast {
				content += "\n  " + lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true).Render("(ID Copied to Clipboard!)")
			} else {
				content += "\n  [ Press 'c' to Copy ID to Clipboard ]"
			}
			s.WriteString(box.Render(content))
		}
	}

	if m.mode == "search" {
		s.WriteString("type: Search • up/down (or tab): Navigate • esc: Back to Pinned • enter: Join selected (or exact name)")
	} else {
		s.WriteString("j/k: Navigate • enter: Open • s: Search • c: Create • p: Join by ID • i: Room Info • x: Delete/Leave • q: Quit")
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s.String())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
