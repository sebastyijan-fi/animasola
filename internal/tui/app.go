package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"animasola/internal/pubsub"
	"animasola/internal/store"
	"animasola/internal/tui/components"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type view int

const (
	viewHome view = iota
	viewCommunities
	viewExplore
	viewCommunity
	viewRoom
	viewThread
)

type focus int

const (
	focusSidebar focus = iota
	focusMain
)

type mainFocus int

const (
	mainNav mainFocus = iota
	mainInput
)

type nav struct {
	v            view
	communityID  string
	roomID       string
	communitySel int
	roomSel      int
	feedSel      int
	threadSel    int
	homeSel      int
}

type createCommunityStep int

const (
	createNone createCommunityStep = iota
	createName
	createDesc
)

var communityNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,29}$`)

type Model struct {
	appName string
	st      Store
	user    *store.User
	broker  *pubsub.Broker[pubsub.Event]
	events  <-chan pubsub.Event
	cancel  func()

	width  int
	height int

	focus     focus
	mainFocus mainFocus

	sidebar components.SidebarModel

	v     view
	stack []nav

	joined []store.Community
	expl   []store.Community
	rooms  []store.Room

	joinedSel int
	explSel   int
	roomSel   int

	curCommunity *store.Community
	curRoom      *store.Room
	feed         []store.FeedMessage
	feedSel      int

	unreadByCommunity map[string]int

	pendingReadRoomID string
	pendingReadMsgID  string
	lastReadWrite     time.Time

	homeItems      []store.FeedMessage
	homeSel        int
	homeOffset     int
	homeSort       store.SortMode
	homeTopRange   store.TopRange
	homeNewPending int

	threadRootID string
	threadItems  []threadItem
	threadSel    int

	replyToID       *string
	replyToUsername string

	deleteConfirm  bool
	deleteTargetID string

	input textinput.Model

	createStep  createCommunityStep
	pendingName string

	errMsg   string
	errUntil time.Time
}

type threadItem struct {
	msg         store.FeedMessage
	treePrefix  string
	overflowCtx string
}

func isShortcutRune(r rune) bool {
	switch r {
	case 'h', 'c', 'e', 'q', 'r', 'u', 'd', 's', 't':
		return true
	default:
		return false
	}
}

func NewApp(appName string, st Store, broker *pubsub.Broker[pubsub.Event], user *store.User) Model {
	ti := textinput.New()
	ti.Placeholder = "/help"
	ti.CharLimit = 5000
	ti.Width = 60
	ti.Focus()

	m := Model{
		appName:           appName,
		st:                st,
		broker:            broker,
		user:              user,
		focus:             focusMain,
		mainFocus:         mainInput,
		sidebar:           components.SidebarModel{Focused: false},
		v:                 viewHome,
		input:             ti,
		homeSort:          store.SortHot,
		homeTopRange:      store.TopWeek,
		unreadByCommunity: make(map[string]int),
	}
	if broker != nil {
		_, ch, cancel := broker.Subscribe(256)
		m.events = ch
		m.cancel = cancel
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.events != nil {
		return tea.Batch(m.cmdLoadJoined(), m.cmdLoadUnread(), m.cmdLoadHomeReset(), textinput.Blink, waitForEvent(m.events))
	}
	return tea.Batch(m.cmdLoadJoined(), m.cmdLoadUnread(), m.cmdLoadHomeReset(), textinput.Blink)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = max(10, m.mainWidth()-2)
		m.rebuildSidebarItems()
		return m, nil
	}

	// async results
	switch msg := msg.(type) {
	case joinedLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.joined = msg.communities
		m.sidebar.Selected = clampIndex(m.sidebar.Selected, len(m.joined))
		m.rebuildSidebarItems()
		return m, m.cmdLoadUnread()
	case unreadLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.unreadByCommunity = make(map[string]int, len(msg.rows))
		for _, r := range msg.rows {
			m.unreadByCommunity[r.CommunityID] = r.Count
		}
		m.rebuildSidebarItems()
		return m, nil
	case exploreLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.expl = msg.communities
		return m, nil
	case roomsLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.rooms = msg.rooms
		return m, nil
	case roomCtxLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		if msg.community != nil {
			m.curCommunity = msg.community
		}
		if msg.room != nil {
			m.curRoom = msg.room
		}
		return m, nil
	case openCommunityMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.curCommunity = msg.community
		m.rooms = msg.rooms
		if msg.room != nil {
			m.curRoom = msg.room
			m.v = viewRoom
			m.focus = focusMain
			m.mainFocus = mainInput
			m.input.Focus()
			return m, tea.Batch(m.cmdLoadFeed(), m.cmdLoadUnread())
		}
		m.v = viewCommunity
		return m, m.cmdLoadUnread()
	case feedLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.feed = msg.items
		if m.feedSel >= len(m.feed) {
			m.feedSel = max(0, len(m.feed)-1)
		}
		if m.v == viewRoom && m.curRoom != nil && len(m.feed) > 0 {
			id := newestMessageID(m.feed)
			return m, tea.Batch(m.cmdMarkRead(m.curRoom.ID, id), m.cmdLoadUnread())
		}
		return m, nil
	case homeLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		if msg.append {
			m.homeItems = append(m.homeItems, msg.items...)
		} else {
			m.homeItems = msg.items
			m.homeOffset = 0
			m.homeSel = 0
			m.homeNewPending = 0
		}
		if m.homeSel >= len(m.homeItems) {
			m.homeSel = max(0, len(m.homeItems)-1)
		}
		return m, nil
	case threadLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.threadRootID = msg.rootID
		m.threadItems = buildThreadItems(msg.items, msg.rootID)
		if m.threadSel >= len(m.threadItems) {
			m.threadSel = max(0, len(m.threadItems)-1)
		}
		if m.v == viewThread && m.curRoom != nil && len(msg.items) > 0 {
			id := newestMessageID(msg.items)
			return m, tea.Batch(m.cmdMarkRead(m.curRoom.ID, id), m.cmdLoadUnread())
		}
		return m, nil
	case communityCreatedMsg:
		if msg.err != nil {
			m.flashErr("Could not create community.")
			return m, nil
		}
		m.createStep = createNone
		m.pendingName = ""
		m.curCommunity = msg.community
		m.curRoom = msg.room
		m.v = viewCommunity
		m.push(nav{v: viewHome})
		return m, tea.Batch(m.cmdLoadJoined(), m.cmdLoadRooms(msg.community.ID))
	case joinedCommunityMsg:
		if msg.err != nil {
			m.flashErr("Could not join community.")
			return m, nil
		}
		m.curCommunity = msg.community
		m.v = viewCommunity
		m.push(nav{v: viewExplore})
		return m, tea.Batch(m.cmdLoadJoined(), m.cmdLoadRooms(msg.community.ID))
	case postedMsg:
		if msg.err != nil {
			m.flashErr("Could not post.")
			return m, nil
		}
		m.input.SetValue("")
		m.replyToID = nil
		m.replyToUsername = ""
		m.input.Placeholder = "/help"
		return m, m.cmdLoadFeed()
	case upvoteToggledMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.applyUpvote(msg.messageID, msg.state.Count)
		return m, nil
	case deletedMsg:
		if msg.err != nil {
			if errors.Is(msg.err, store.ErrPermission) {
				m.flashErr("You don't have permission to do that.")
				return m, nil
			}
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.deleteConfirm = false
		m.deleteTargetID = ""
		switch m.v {
		case viewThread:
			return m, m.cmdLoadThread(m.threadRootID)
		case viewRoom:
			return m, m.cmdLoadFeed()
		default:
			return m, nil
		}
	}

	if em, ok := msg.(eventMsg); ok {
		if !em.ok {
			return m, nil
		}
		// Minimal realtime: refresh the active feed/thread if the event affects it.
		switch m.v {
		case viewHome:
			if em.evt.Type == pubsub.EventNewMessage {
				if m.homeSort == store.SortNew {
					return m, tea.Batch(m.cmdLoadHomeReset(), m.cmdLoadUnread(), waitForEvent(m.events))
				}
				m.homeNewPending++
			}
			return m, tea.Batch(m.cmdLoadUnread(), waitForEvent(m.events))
		case viewRoom:
			if m.curRoom != nil && em.evt.RoomID == m.curRoom.ID {
				return m, tea.Batch(m.cmdLoadFeed(), m.cmdLoadUnread(), waitForEvent(m.events))
			}
		case viewThread:
			if m.curRoom != nil && em.evt.RoomID == m.curRoom.ID {
				return m, tea.Batch(m.cmdLoadThread(m.threadRootID), m.cmdLoadUnread(), waitForEvent(m.events))
			}
		}
		return m, tea.Batch(m.cmdLoadUnread(), waitForEvent(m.events))
	}

	// key handling depends on focus.
	if km, ok := msg.(tea.KeyMsg); ok {
		if m.deleteConfirm {
			switch km.String() {
			case "y":
				m.deleteConfirm = false
				id := m.deleteTargetID
				m.deleteTargetID = ""
				return m, m.cmdDelete(id)
			case "n", "esc":
				m.deleteConfirm = false
				m.deleteTargetID = ""
				return m, nil
			default:
				return m, nil
			}
		}

		switch km.String() {
		case "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		// Global shortcuts that should work even while typing.
		// We use Alt-modified keys to avoid conflicting with normal text entry.
		case "alt+q":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "alt+h":
			m.v = viewHome
			return m, m.cmdLoadHomeReset()
		case "alt+c":
			m.v = viewCommunities
			return m, m.cmdLoadJoined()
		case "alt+e":
			m.v = viewExplore
			return m, m.cmdLoadExplore()
		case "alt+r":
			switch m.v {
			case viewRoom, viewThread:
				return m.startReply()
			case viewHome:
				return m, m.cmdLoadHomeReset()
			default:
				return m, nil
			}
		case "alt+u":
			return m, m.cmdToggleUpvote()
		case "alt+d":
			return m.startDeleteConfirm()
		case "alt+s":
			if m.v == viewHome {
				m.cycleHomeSort()
				return m, m.cmdLoadHomeReset()
			}
			return m, nil
		case "alt+t":
			if m.v == viewHome && m.homeSort == store.SortTop {
				m.cycleHomeTopRange()
				return m, m.cmdLoadHomeReset()
			}
			return m, nil
		case "tab":
			if m.sidebarVisible() {
				if m.focus == focusMain {
					m.focus = focusSidebar
					m.mainFocus = mainNav
					m.input.Blur()
				} else {
					m.focus = focusMain
					m.mainFocus = mainInput
					m.input.Focus()
				}
				m.rebuildSidebarItems()
				return m, nil
			}
			return m, nil
		case "esc":
			if m.focus == focusMain && m.mainFocus == mainInput {
				m.mainFocus = mainNav
				m.input.Blur()
				return m, nil
			}
			return (&m).pop()
		}

		if m.focus == focusSidebar {
			switch km.String() {
			case "up":
				if m.sidebar.Selected > 0 {
					m.sidebar.Selected--
				}
				m.joinedSel = m.sidebar.Selected
				return m, nil
			case "down":
				if m.sidebar.Selected < len(m.joined)-1 {
					m.sidebar.Selected++
				}
				m.joinedSel = m.sidebar.Selected
				return m, nil
			case "enter":
				if len(m.joined) == 0 {
					return m, nil
				}
				c := m.joined[m.sidebar.Selected]
				return m, m.cmdOpenCommunityDefault(c.ID)
			}
		}

		if m.focus == focusMain && m.mainFocus == mainNav {
			// Allow typing to re-focus the input after Esc/Tab, without stealing single-key shortcuts.
			// Alt-modified keys are handled above.
			if km.Type == tea.KeyRunes && len(km.Runes) == 1 && !km.Alt {
				r := km.Runes[0]
				if r == '/' || r == ' ' || !isShortcutRune(r) {
					m.mainFocus = mainInput
					m.input.Focus()
					var cmd tea.Cmd
					m.input, cmd = m.input.Update(km)
					return m, cmd
				}
			}

			switch km.String() {
			case "q":
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			case "h":
				m.v = viewHome
				return m, m.cmdLoadHomeReset()
			case "c":
				m.v = viewCommunities
				return m, m.cmdLoadJoined()
			case "e":
				m.v = viewExplore
				return m, m.cmdLoadExplore()
			case "r":
				switch m.v {
				case viewRoom, viewThread:
					return m.startReply()
				case viewHome:
					return m, m.cmdLoadHomeReset()
				default:
					return m, nil
				}
			case "u":
				return m, m.cmdToggleUpvote()
			case "d":
				return m.startDeleteConfirm()
			case "s":
				if m.v == viewHome {
					m.cycleHomeSort()
					return m, m.cmdLoadHomeReset()
				}
				return m, nil
			case "t":
				if m.v == viewHome && m.homeSort == store.SortTop {
					m.cycleHomeTopRange()
					return m, m.cmdLoadHomeReset()
				}
				return m, nil
			}
		}
	}

	// local navigation per view
	if km, ok := msg.(tea.KeyMsg); ok {
		if m.focus != focusMain || m.mainFocus != mainNav {
			goto input
		}
		switch m.v {
		case viewHome:
			switch km.String() {
			case "up":
				if m.homeSel > 0 {
					m.homeSel--
				}
				return m, nil
			case "down":
				if m.homeSel < len(m.homeItems)-1 {
					m.homeSel++
					// mimic "infinite scroll": load more when we reach the end.
					if m.homeSel >= len(m.homeItems)-1 {
						return m, m.cmdLoadHomeMore()
					}
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				id := m.selectedHomeMessageID()
				if id == "" {
					return m, nil
				}
				roomID := m.homeItems[m.homeSel].RoomID
				communityID := m.homeItems[m.homeSel].CommunityID
				m.v = viewThread
				m.push(nav{v: viewHome, homeSel: m.homeSel})
				return m, tea.Batch(m.cmdLoadRoomContext(communityID, roomID), m.cmdLoadThread(id))
			}
		case viewCommunities:
			switch km.String() {
			case "up":
				if m.joinedSel > 0 {
					m.joinedSel--
				}
				return m, nil
			case "down":
				if m.joinedSel < len(m.joined)-1 {
					m.joinedSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				if len(m.joined) == 0 {
					return m, nil
				}
				c := m.joined[m.joinedSel]
				m.curCommunity = &c
				m.v = viewCommunity
				m.push(nav{v: viewCommunities, communitySel: m.joinedSel})
				return m, m.cmdLoadRooms(c.ID)
			}
		case viewExplore:
			switch km.String() {
			case "up":
				if m.explSel > 0 {
					m.explSel--
				}
				return m, nil
			case "down":
				if m.explSel < len(m.expl)-1 {
					m.explSel++
				}
				return m, nil
			case "j", "enter":
				if km.String() == "enter" && m.mainFocus != mainNav {
					goto input
				}
				if len(m.expl) == 0 {
					return m, nil
				}
				c := m.expl[m.explSel]
				return m, m.cmdJoinCommunity(c.ID)
			}
		case viewCommunity:
			switch km.String() {
			case "up":
				if m.roomSel > 0 {
					m.roomSel--
				}
				return m, nil
			case "down":
				if m.roomSel < len(m.rooms)-1 {
					m.roomSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				if len(m.rooms) == 0 {
					return m, nil
				}
				r := m.rooms[m.roomSel]
				m.curRoom = &r
				m.v = viewRoom
				m.push(nav{v: viewCommunity, communityID: m.curCommunity.ID, roomSel: m.roomSel})
				return m, m.cmdLoadFeed()
			}
		case viewRoom:
			switch km.String() {
			case "up":
				if m.feedSel > 0 {
					m.feedSel--
				}
				return m, nil
			case "down":
				if m.feedSel < len(m.feed)-1 {
					m.feedSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				root := m.selectedMessageID()
				if root == "" {
					return m, nil
				}
				m.v = viewThread
				m.push(nav{v: viewRoom, roomID: m.curRoom.ID, feedSel: m.feedSel})
				return m, m.cmdLoadThread(root)
			}
		case viewThread:
			switch km.String() {
			case "up":
				if m.threadSel > 0 {
					m.threadSel--
				}
				return m, nil
			case "down":
				if m.threadSel < len(m.threadItems)-1 {
					m.threadSel++
				}
				return m, nil
			}
		}
	}

input:
	// input handling
	if m.focus != focusMain || m.mainFocus != mainInput {
		return m, nil
	}

	// Don't feed navigation keys into the input.
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "up", "down", "left", "right", "pgup", "pgdown", "home", "end":
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "enter" {
		cmd2 := (&m).handleEnter()
		// Stay in input mode.
		m.mainFocus = mainInput
		m.input.Focus()
		return m, tea.Batch(cmd, cmd2)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.width > 0 && m.height > 0 && (m.width < 80 || m.height < 24) {
		return lipgloss.NewStyle().Padding(1, 2).Render("Resize terminal to at least 80x24.\n")
	}

	mw := m.mainWidth()

	var b strings.Builder
	b.WriteString(m.header())
	if m.errMsg != "" && time.Now().Before(m.errUntil) {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(m.errMsg))
		b.WriteString("\n\n")
	}

	switch m.v {
	case viewHome:
		b.WriteString(m.viewHome())
	case viewCommunities:
		b.WriteString(m.viewJoined())
	case viewExplore:
		b.WriteString(m.viewExplore())
	case viewCommunity:
		b.WriteString(m.viewCommunity())
	case viewRoom:
		b.WriteString(m.viewRoom())
	case viewThread:
		b.WriteString(m.viewThread())
	}

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(strings.Repeat("─", max(0, mw-1))))
	b.WriteString("\n")
	if m.replyToID != nil && m.replyToUsername != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("replying to " + m.replyToUsername + ": "))
	}
	b.WriteString(m.input.View())
	b.WriteString("\n")

	mainStyle := lipgloss.NewStyle().Width(mw).Height(m.height)
	main := mainStyle.Render(b.String())

	if !m.sidebarVisible() {
		return lipgloss.NewStyle().Padding(0, 1).Render(main)
	}

	sb := m.sidebar
	sb.Focused = m.focus == focusSidebar
	sbStr := sb.View(m.height)
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("│")
	return lipgloss.JoinHorizontal(lipgloss.Top, sbStr, sep, main)
}

func (m *Model) flashErr(s string) {
	m.errMsg = s
	m.errUntil = time.Now().Add(5 * time.Second)
}

func (m Model) header() string {
	u := "<unknown>"
	if m.user != nil {
		u = m.user.Username
	}
	loc := "home"
	switch m.v {
	case viewCommunities:
		loc = "communities"
	case viewExplore:
		loc = "explore"
	case viewCommunity:
		if m.curCommunity != nil {
			loc = m.curCommunity.Name
		} else {
			loc = "community"
		}
	case viewRoom:
		if m.curCommunity != nil && m.curRoom != nil {
			loc = fmt.Sprintf("%s · #%s", m.curCommunity.Name, m.curRoom.Name)
		} else {
			loc = "room"
		}
	case viewThread:
		if m.curCommunity != nil && m.curRoom != nil {
			loc = fmt.Sprintf("%s · #%s · thread", m.curCommunity.Name, m.curRoom.Name)
		} else {
			loc = "thread"
		}
	}

	title := lipgloss.NewStyle().Bold(true).Render(m.appName)
	return fmt.Sprintf("%s  |  %s  |  %s\n\n", title, u, loc)
}

func (m Model) viewHome() string {
	if len(m.joined) == 0 {
		return "No communities joined.\n\nKeys: e explore, /create-community\n"
	}
	mw := m.mainWidth()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Home · %s", m.homeSort))
	if m.homeSort == store.SortTop {
		b.WriteString(fmt.Sprintf(" · %s", m.homeTopRange))
	}
	b.WriteString("\n\n")
	if m.homeNewPending > 0 && m.homeSort != store.SortNew {
		b.WriteString(fmt.Sprintf("%d new posts  press r to refresh\n\n", m.homeNewPending))
	}

	if len(m.homeItems) == 0 {
		b.WriteString("(no posts yet)\n")
		b.WriteString("\nNav: up/down select, Enter open thread, s cycle sort, t cycle top range, r refresh.\n")
		return b.String()
	}

	for i, it := range m.homeItems {
		sel := "  "
		if i == m.homeSel {
			sel = "> "
		}
		author := it.AuthorUsername
		if it.IsDeleted {
			author = "[deleted]"
		}
		content := it.Content
		if it.IsDeleted {
			content = "[deleted]"
		}
		b.WriteString(fmt.Sprintf("%s%s · #%s · %s · %s\n", sel, it.CommunityName, it.RoomName, author, relTime(it.CreatedAt)))
		b.WriteString(wrap(content, max(20, mw-6)))
		b.WriteString(fmt.Sprintf("\n(%d↑ %d💬)\n\n", it.Upvotes, it.Replies))
	}
	b.WriteString("Nav: up/down select, Enter open thread, s cycle sort, t cycle top range, r refresh. Tab toggles input/nav.\n")
	return b.String()
}

func (m Model) viewJoined() string {
	if len(m.joined) == 0 {
		return "No communities joined.\n\nKeys: e explore, /create-community\n"
	}
	var b strings.Builder
	b.WriteString("Joined Communities\n\n")
	for i, c := range m.joined {
		prefix := "  "
		if i == m.joinedSel {
			prefix = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s (%d)\n", prefix, c.Name, c.MemberCount))
	}
	b.WriteString("\nEnter to open.\n")
	return b.String()
}

func (m Model) viewExplore() string {
	var b strings.Builder
	b.WriteString("Explore Communities\n\n")
	if len(m.expl) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, c := range m.expl {
		prefix := "  "
		if i == m.explSel {
			prefix = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s (%d)  %q\n", prefix, c.Name, c.MemberCount, c.Description))
	}
	b.WriteString("\nEnter/j to join.\n")
	return b.String()
}

func (m Model) viewCommunity() string {
	if m.curCommunity == nil {
		return "No community selected.\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s (%d members)\n", m.curCommunity.Name, m.curCommunity.MemberCount))
	if m.curCommunity.Description != "" {
		b.WriteString(fmt.Sprintf("%q\n", m.curCommunity.Description))
	}
	b.WriteString("\nRooms\n\n")
	if len(m.rooms) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, r := range m.rooms {
		prefix := "  "
		if i == m.roomSel {
			prefix = "> "
		}
		b.WriteString(fmt.Sprintf("%s#%s\n", prefix, r.Name))
	}
	b.WriteString("\nEnter to open room.\n")
	return b.String()
}

func (m Model) viewRoom() string {
	if m.curRoom == nil {
		return "No room selected.\n"
	}
	mw := m.mainWidth()
	var b strings.Builder
	if len(m.feed) == 0 {
		b.WriteString("(no messages)\n")
		return b.String()
	}
	for i, it := range m.feed {
		sel := "  "
		if i == m.feedSel {
			sel = "> "
		}
		author := it.AuthorUsername
		if it.IsDeleted {
			author = "[deleted]"
		}
		content := it.Content
		if it.IsDeleted {
			content = "[deleted]"
		}
		b.WriteString(fmt.Sprintf("%s%s · %s  (%d↑ %d💬)\n", sel, author, relTime(it.CreatedAt), it.Upvotes, it.Replies))
		b.WriteString(wrap(content, max(20, mw-6)))
		b.WriteString("\n\n")
	}
	b.WriteString("Nav: up/down select, Enter thread, r reply, u upvote, d delete, Esc back. Tab toggles input/nav.\n")
	return b.String()
}

func (m Model) viewThread() string {
	if m.curRoom == nil {
		return "No room selected.\n"
	}
	mw := m.mainWidth()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("← Back to #%s\n\n", m.curRoom.Name))
	if len(m.threadItems) == 0 {
		b.WriteString("(no messages)\n")
		return b.String()
	}
	for i, it := range m.threadItems {
		sel := "  "
		if i == m.threadSel {
			sel = "> "
		}
		author := it.msg.AuthorUsername
		if it.msg.IsDeleted {
			author = "[deleted]"
		}
		content := it.msg.Content
		if it.msg.IsDeleted {
			content = "[deleted]"
		}
		if it.overflowCtx != "" {
			content = "replying to " + it.overflowCtx + ": " + content
		}
		b.WriteString(fmt.Sprintf("%s%s%s · %s  (%d↑ %d💬)\n", sel, it.treePrefix, author, relTime(it.msg.CreatedAt), it.msg.Upvotes, it.msg.Replies))
		b.WriteString(wrap(content, max(20, mw-6-len(it.treePrefix))))
		b.WriteString("\n\n")
	}
	b.WriteString("Nav: up/down select, r reply, u upvote, d delete, Esc back. Tab toggles input/nav.\n")
	return b.String()
}

func (m *Model) handleEnter() tea.Cmd {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return nil
	}

	if m.createStep != createNone {
		return m.handleCreateFlow(line)
	}

	if cmd := ParseCommand(line); cmd != nil {
		m.input.SetValue("")
		return m.execCommand(cmd)
	}

	if (m.v == viewRoom || m.v == viewThread) && m.curRoom != nil && m.user != nil {
		m.input.SetValue("")
		return m.cmdPost(line)
	}

	// Non-command text outside a room is ignored.
	m.input.SetValue("")
	return nil
}

func (m *Model) handleCreateFlow(line string) tea.Cmd {
	switch m.createStep {
	case createName:
		if !communityNameRe.MatchString(line) {
			m.flashErr("Invalid community name (2-30, lowercase letters/numbers/hyphens; start with letter)")
			return nil
		}
		m.pendingName = line
		m.createStep = createDesc
		m.input.SetValue("")
		m.input.Placeholder = "description (max 200)"
		return nil
	case createDesc:
		desc := line
		if len(desc) > 200 {
			m.flashErr("Description too long (max 200)")
			return nil
		}
		name := m.pendingName
		m.input.SetValue("")
		m.input.Placeholder = "/help"
		m.createStep = createNone
		return m.cmdCreateCommunity(name, desc)
	default:
		return nil
	}
}

func (m *Model) execCommand(cmd *Command) tea.Cmd {
	switch cmd.Name {
	case "help":
		m.flashErr("Commands: /explore /join <community> /create-community")
		return nil
	case "explore":
		m.v = viewExplore
		return m.cmdLoadExplore()
	case "create-community":
		m.createStep = createName
		m.pendingName = ""
		m.input.SetValue("")
		m.input.Placeholder = "community name"
		return nil
	case "join":
		if len(cmd.Args) != 1 {
			m.flashErr("Usage: /join community")
			return nil
		}
		return m.cmdJoinByName(cmd.Args[0])
	default:
		m.flashErr("Unknown command. Type /help for available commands.")
		return nil
	}
}

func (m *Model) push(n nav) {
	m.stack = append(m.stack, n)
}

func (m *Model) pop() (tea.Model, tea.Cmd) {
	if len(m.stack) == 0 {
		return *m, nil
	}
	last := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	m.v = last.v
	switch last.v {
	case viewCommunities:
		m.joinedSel = last.communitySel
		return *m, m.cmdLoadJoined()
	case viewExplore:
		return *m, m.cmdLoadExplore()
	case viewCommunity:
		if last.communityID != "" {
			// Keep existing selected community/rooms if possible.
			return *m, m.cmdLoadRooms(last.communityID)
		}
		return *m, nil
	case viewRoom:
		m.feedSel = last.feedSel
		m.threadSel = last.threadSel
		m.replyToID = nil
		m.replyToUsername = ""
		m.input.Placeholder = "/help"
		return *m, m.cmdLoadFeed()
	case viewHome:
		m.replyToID = nil
		m.replyToUsername = ""
		m.input.Placeholder = "/help"
		m.homeSel = last.homeSel
		if m.homeSel >= len(m.homeItems) {
			m.homeSel = max(0, len(m.homeItems)-1)
		}
		return *m, nil
	default:
		return *m, nil
	}
}

type joinedLoadedMsg struct {
	communities []store.Community
	err         error
}

func (m Model) cmdLoadJoined() tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cs, err := m.st.ListJoinedCommunities(ctx, userID)
		return joinedLoadedMsg{communities: cs, err: err}
	}
}

type unreadLoadedMsg struct {
	rows []store.CommunityUnread
	err  error
}

func (m Model) cmdLoadUnread() tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := m.st.UnreadCountsByCommunity(ctx, userID)
		return unreadLoadedMsg{rows: rows, err: err}
	}
}

type exploreLoadedMsg struct {
	communities []store.Community
	err         error
}

func (m Model) cmdLoadExplore() tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cs, err := m.st.ListExploreCommunities(ctx, userID, 100)
		return exploreLoadedMsg{communities: cs, err: err}
	}
}

type homeLoadedMsg struct {
	items  []store.FeedMessage
	append bool
	err    error
}

func (m Model) cmdLoadHomeReset() tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	sortMode := m.homeSort
	topRange := m.homeTopRange
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		items, err := m.st.ListHomeFeed(ctx, userID, sortMode, topRange, 30, 0)
		return homeLoadedMsg{items: items, append: false, err: err}
	}
}

func (m Model) cmdLoadHomeMore() tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	sortMode := m.homeSort
	topRange := m.homeTopRange
	offset := len(m.homeItems)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		items, err := m.st.ListHomeFeed(ctx, userID, sortMode, topRange, 30, offset)
		return homeLoadedMsg{items: items, append: true, err: err}
	}
}

type roomsLoadedMsg struct {
	rooms []store.Room
	err   error
}

func (m Model) cmdLoadRooms(communityID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rs, err := m.st.ListRoomsByCommunity(ctx, communityID)
		return roomsLoadedMsg{rooms: rs, err: err}
	}
}

type roomCtxLoadedMsg struct {
	community *store.Community
	room      *store.Room
	err       error
}

func (m Model) cmdLoadRoomContext(communityID, roomID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := m.st.GetCommunityByID(ctx, communityID)
		if err != nil {
			return roomCtxLoadedMsg{err: err}
		}
		r, err := m.st.GetRoomByID(ctx, roomID)
		return roomCtxLoadedMsg{community: c, room: r, err: err}
	}
}

type openCommunityMsg struct {
	community *store.Community
	rooms     []store.Room
	room      *store.Room
	err       error
}

func (m Model) cmdOpenCommunityDefault(communityID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := m.st.GetCommunityByID(ctx, communityID)
		if err != nil {
			return openCommunityMsg{err: err}
		}
		rooms, err := m.st.ListRoomsByCommunity(ctx, communityID)
		if err != nil {
			return openCommunityMsg{err: err}
		}
		var chosen *store.Room
		for i := range rooms {
			if rooms[i].Name == "general" {
				r := rooms[i]
				chosen = &r
				break
			}
		}
		if chosen == nil && len(rooms) > 0 {
			r := rooms[0]
			chosen = &r
		}
		return openCommunityMsg{community: c, rooms: rooms, room: chosen}
	}
}

type feedLoadedMsg struct {
	items []store.FeedMessage
	err   error
}

func (m Model) cmdLoadFeed() tea.Cmd {
	if m.curRoom == nil {
		return nil
	}
	roomID := m.curRoom.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		items, err := m.st.ListRoomTopLevelNew(ctx, roomID, 30, nil)
		return feedLoadedMsg{items: items, err: err}
	}
}

type communityCreatedMsg struct {
	community *store.Community
	room      *store.Room
	err       error
}

func (m Model) cmdCreateCommunity(name, desc string) tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, r, err := m.st.CreateCommunity(ctx, userID, name, desc)
		return communityCreatedMsg{community: c, room: r, err: err}
	}
}

type joinedCommunityMsg struct {
	community *store.Community
	err       error
}

func (m Model) cmdJoinByName(name string) tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := m.st.GetCommunityByName(ctx, name)
		if err != nil {
			return joinedCommunityMsg{err: err}
		}
		if c == nil {
			return joinedCommunityMsg{err: store.ErrNotFound}
		}
		if err := m.st.JoinCommunity(ctx, userID, c.ID); err != nil {
			return joinedCommunityMsg{err: err}
		}
		return joinedCommunityMsg{community: c}
	}
}

func (m Model) cmdJoinCommunity(communityID string) tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.st.JoinCommunity(ctx, userID, communityID); err != nil {
			return joinedCommunityMsg{err: err}
		}
		c, err := m.st.GetCommunityByID(ctx, communityID)
		return joinedCommunityMsg{community: c, err: err}
	}
}

type postedMsg struct {
	err error
}

func (m Model) cmdPost(content string) tea.Cmd {
	roomID := m.curRoom.ID
	authorID := m.user.ID
	parentID := m.replyToID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := m.st.CreateMessage(ctx, roomID, authorID, content, parentID)
		return postedMsg{err: err}
	}
}

func (m *Model) startReply() (tea.Model, tea.Cmd) {
	id, username := m.selectedMessageIDAndAuthor()
	if id == "" {
		return *m, nil
	}
	m.replyToID = &id
	m.replyToUsername = username
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	return *m, nil
}

func (m *Model) startDeleteConfirm() (tea.Model, tea.Cmd) {
	id := m.selectedMessageID()
	if id == "" {
		return *m, nil
	}
	m.deleteConfirm = true
	m.deleteTargetID = id
	m.flashErr("Delete this message? (y/n)")
	return *m, nil
}

func (m *Model) selectedMessageID() string {
	switch m.v {
	case viewHome:
		if m.homeSel < 0 || m.homeSel >= len(m.homeItems) {
			return ""
		}
		return m.homeItems[m.homeSel].ID
	case viewRoom:
		if m.feedSel < 0 || m.feedSel >= len(m.feed) {
			return ""
		}
		return m.feed[m.feedSel].ID
	case viewThread:
		if m.threadSel < 0 || m.threadSel >= len(m.threadItems) {
			return ""
		}
		return m.threadItems[m.threadSel].msg.ID
	default:
		return ""
	}
}

func (m *Model) selectedHomeMessageID() string {
	if m.v != viewHome {
		return ""
	}
	if m.homeSel < 0 || m.homeSel >= len(m.homeItems) {
		return ""
	}
	return m.homeItems[m.homeSel].ID
}

func (m *Model) selectedMessageIDAndAuthor() (string, string) {
	switch m.v {
	case viewHome:
		if m.homeSel < 0 || m.homeSel >= len(m.homeItems) {
			return "", ""
		}
		return m.homeItems[m.homeSel].ID, m.homeItems[m.homeSel].AuthorUsername
	case viewRoom:
		if m.feedSel < 0 || m.feedSel >= len(m.feed) {
			return "", ""
		}
		return m.feed[m.feedSel].ID, m.feed[m.feedSel].AuthorUsername
	case viewThread:
		if m.threadSel < 0 || m.threadSel >= len(m.threadItems) {
			return "", ""
		}
		return m.threadItems[m.threadSel].msg.ID, m.threadItems[m.threadSel].msg.AuthorUsername
	default:
		return "", ""
	}
}

func (m *Model) cycleHomeSort() {
	switch m.homeSort {
	case store.SortHot:
		m.homeSort = store.SortNew
	case store.SortNew:
		m.homeSort = store.SortTop
	case store.SortTop:
		m.homeSort = store.SortHot
	default:
		m.homeSort = store.SortHot
	}
}

func (m *Model) cycleHomeTopRange() {
	switch m.homeTopRange {
	case store.TopToday:
		m.homeTopRange = store.TopWeek
	case store.TopWeek:
		m.homeTopRange = store.TopMonth
	case store.TopMonth:
		m.homeTopRange = store.TopAll
	case store.TopAll:
		m.homeTopRange = store.TopToday
	default:
		m.homeTopRange = store.TopWeek
	}
}

func (m *Model) applyUpvote(messageID string, newCount int) {
	for i := range m.homeItems {
		if m.homeItems[i].ID == messageID {
			m.homeItems[i].Upvotes = newCount
		}
	}
	for i := range m.feed {
		if m.feed[i].ID == messageID {
			m.feed[i].Upvotes = newCount
		}
	}
	for i := range m.threadItems {
		if m.threadItems[i].msg.ID == messageID {
			m.threadItems[i].msg.Upvotes = newCount
		}
	}
}

func buildThreadItems(items []store.FeedMessage, rootID string) []threadItem {
	byID := make(map[string]store.FeedMessage, len(items))
	children := make(map[string][]string)
	for _, it := range items {
		byID[it.ID] = it
		if it.ParentID != nil {
			children[*it.ParentID] = append(children[*it.ParentID], it.ID)
		}
	}

	var out []threadItem
	var walk func(id string, ancMore []bool, depth int)
	walk = func(id string, ancMore []bool, depth int) {
		msg, ok := byID[id]
		if !ok {
			return
		}
		prefix := ""
		if depth > 0 {
			for i := 0; i < len(ancMore)-1; i++ {
				if ancMore[i] {
					prefix += "│  "
				} else {
					prefix += "   "
				}
			}
			if ancMore[len(ancMore)-1] {
				prefix += "├─ "
			} else {
				prefix += "└─ "
			}
		}

		overflowCtx := ""
		if depth > 5 && msg.ParentID != nil {
			if p, ok := byID[*msg.ParentID]; ok {
				overflowCtx = p.AuthorUsername
				if p.IsDeleted {
					overflowCtx = "[deleted]"
				}
			}
		}

		out = append(out, threadItem{
			msg:         msg,
			treePrefix:  prefix,
			overflowCtx: overflowCtx,
		})

		kids := children[id]
		for i, kid := range kids {
			last := i == len(kids)-1
			// ancMore indicates whether *this* node has more siblings after it.
			// For the last child, use false so rendering uses └─ and stops vertical line.
			walk(kid, append(ancMore, !last), depth+1)
		}
	}

	walk(rootID, nil, 0)
	return out
}

type threadLoadedMsg struct {
	rootID string
	items  []store.FeedMessage
	err    error
}

func (m Model) cmdLoadThread(rootID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		items, err := m.st.ListThread(ctx, rootID)
		return threadLoadedMsg{rootID: rootID, items: items, err: err}
	}
}

type upvoteToggledMsg struct {
	messageID string
	state     *store.UpvoteState
	err       error
}

func (m Model) cmdToggleUpvote() tea.Cmd {
	if m.user == nil {
		return nil
	}
	id := m.selectedMessageID()
	if id == "" {
		return nil
	}
	userID := m.user.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		state, err := m.st.ToggleUpvote(ctx, userID, id)
		return upvoteToggledMsg{messageID: id, state: state, err: err}
	}
}

type deletedMsg struct {
	err error
}

func (m Model) cmdDelete(messageID string) tea.Cmd {
	if m.user == nil {
		return nil
	}
	userID := m.user.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.SoftDeleteMessage(ctx, messageID, userID)
		return deletedMsg{err: err}
	}
}

func (m Model) cmdMarkRead(roomID, msgID string) tea.Cmd {
	if m.user == nil || roomID == "" || msgID == "" {
		return nil
	}
	userID := m.user.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.st.UpsertReadPosition(ctx, userID, roomID, msgID)
		return nil
	}
}

type eventMsg struct {
	ok  bool
	evt pubsub.Event
}

func waitForEvent(ch <-chan pubsub.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return eventMsg{ok: false}
		}
		return eventMsg{ok: true, evt: evt}
	}
}

func wrap(s string, w int) string {
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := words[0]
		for _, word := range words[1:] {
			if len(cur)+1+len(word) > w {
				out = append(out, cur)
				cur = word
				continue
			}
			cur += " " + word
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}

func relTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dmin ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	if days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	weeks := days / 7
	if days < 30 {
		return fmt.Sprintf("%dw ago", weeks)
	}
	if d < 365*24*time.Hour {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2, 2006")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func (m *Model) sidebarVisible() bool {
	return m.width >= 60 && m.v != viewThread
}

func (m *Model) mainWidth() int {
	w := m.width
	if m.sidebarVisible() {
		w = w - components.SidebarWidth - 1
	}
	if w < 0 {
		return 0
	}
	return w
}

func (m *Model) rebuildSidebarItems() {
	items := make([]components.SidebarItem, 0, len(m.joined))
	for _, c := range m.joined {
		items = append(items, components.SidebarItem{
			ID:     c.ID,
			Name:   c.Name,
			Unread: m.unreadByCommunity[c.ID],
		})
	}
	m.sidebar.Items = items
	m.sidebar.Selected = clampIndex(m.sidebar.Selected, len(items))
	m.sidebar.Focused = m.focus == focusSidebar
}

func newestMessageID(items []store.FeedMessage) string {
	maxID := ""
	for _, it := range items {
		if it.ID > maxID {
			maxID = it.ID
		}
	}
	return maxID
}
