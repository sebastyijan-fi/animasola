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
	viewProfile
	viewMembers
	viewMentions
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
	mainSearch
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
	returnSearch bool
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
	pinned       *store.FeedMessage
	feed         []store.FeedMessage
	feedSel      int
	feedSort     store.SortMode
	feedTopRange store.TopRange
	feedNewCur   *string
	feedTopCur   *store.RoomTopCursor
	feedHotCur   *store.RoomHotCursor
	feedHasMore  bool
	feedLoading  bool
	feedSpinIdx  int
	feedPending  int

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
	homeNewCur     *string
	homeTopCur     *store.HomeTopCursor
	homeHotCur     *store.HomeHotCursor
	homeHasMore    bool
	homeLoading    bool

	threadRootID string
	threadItems  []threadItem
	threadSel    int

	profile    *store.UserProfile
	profileSel int

	members    []string
	membersSel int

	mentions    []store.FeedMessage
	mentionsSel int

	replyToID       *string
	replyToUsername string

	deleteConfirm  bool
	deleteTargetID string

	leaveConfirm     bool
	leaveCommunityID string
	leaveCommunity   string

	deleteRoomConfirm bool
	deleteRoomID      string
	deleteRoomName    string

	deleteCommunityConfirm bool
	deleteCommunityID      string
	deleteCommunityName    string

	input textinput.Model

	cmdSuggestOpen bool
	cmdSuggestSel  int
	cmdSuggest     []cmdSuggestion

	searchOpen    bool
	searchInput   textinput.Model
	searchQuery   string
	searchResults []store.SearchResult
	searchSel     int
	searchToken   int
	searchLoading bool
	searchErr     string
	searchMatchID string
	searchTrunc   bool

	createStep  createCommunityStep
	pendingName string

	errMsg   string
	errUntil time.Time
}

type feedSpinTickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type searchDebounceMsg struct {
	token int
	q     string
}

type threadItem struct {
	msg         store.FeedMessage
	treePrefix  string
	overflowCtx string
}

type cmdSuggestion struct {
	Text       string
	ExpectsArg bool
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

	si := textinput.New()
	si.Placeholder = "search"
	si.CharLimit = 200
	si.Width = 30

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
		searchInput:       si,
		homeSort:          store.SortHot,
		homeTopRange:      store.TopWeek,
		homeLoading:       true,
		feedSort:          store.SortHot,
		feedTopRange:      store.TopWeek,
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
		return tea.Batch(
			m.cmdLoadJoined(),
			m.cmdLoadUnread(),
			m.cmdLoadHomeReset(),
			textinput.Blink,
			waitForEvent(m.events),
			tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return feedSpinTickMsg{} }),
		)
	}
	return tea.Batch(
		m.cmdLoadJoined(),
		m.cmdLoadUnread(),
		m.cmdLoadHomeReset(),
		textinput.Blink,
		tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return feedSpinTickMsg{} }),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = max(10, m.mainWidth()-2)
		m.searchInput.Width = max(10, m.mainWidth()-len("Search: ")-2)
		m.rebuildSidebarItems()
		return m, nil
	}

	// spinner tick
	if _, ok := msg.(feedSpinTickMsg); ok {
		if m.feedLoading || m.homeLoading {
			m.feedSpinIdx = (m.feedSpinIdx + 1) % len(spinnerFrames)
		}
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return feedSpinTickMsg{} })
	}

	if mm, ok := msg.(tea.MouseMsg); ok {
		return m.handleMouse(mm)
	}

	// async results
	switch msg := msg.(type) {
	case searchDebounceMsg:
		// Only run the latest scheduled search.
		if msg.token != m.searchToken {
			return m, nil
		}
		if strings.TrimSpace(msg.q) == "" {
			m.searchResults = nil
			m.searchSel = 0
			m.searchLoading = false
			m.searchErr = ""
			return m, nil
		}
		return m, m.cmdSearch(msg.q)
	case searchLoadedMsg:
		if msg.err != nil {
			m.searchErr = "Something went wrong. Try again."
			m.searchLoading = false
			return m, nil
		}
		m.searchTrunc = len(msg.results) > 50
		if m.searchTrunc {
			m.searchResults = msg.results[:50]
		} else {
			m.searchResults = msg.results
		}
		m.searchSel = clampIndex(m.searchSel, len(m.searchResults))
		m.searchLoading = false
		m.searchErr = ""
		return m, nil
	case openSearchResultMsg:
		if msg.err != nil {
			m.searchErr = "Something went wrong. Try again."
			return m, nil
		}
		// Close the modal, open thread; stack remembers to restore search on Esc.
		m.searchOpen = false
		m.mainFocus = mainNav
		m.searchInput.Blur()
		m.searchMatchID = msg.matchID
		prevV := m.v
		m.v = viewThread
		m.push(nav{v: prevV, roomID: msg.roomID, communityID: msg.communityID, returnSearch: true})
		return m, tea.Batch(m.cmdLoadRoomContext(msg.communityID, msg.roomID), m.cmdLoadThread(msg.rootID))
	case openMentionMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		prevV := m.v
		m.v = viewThread
		m.push(nav{v: prevV})
		return m, tea.Batch(m.cmdLoadRoomContext(msg.communityID, msg.roomID), m.cmdLoadThread(msg.rootID))
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
	case profileLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		if msg.profile == nil {
			m.flashErr("User not found.")
			return (&m).pop()
		}
		m.profile = msg.profile
		m.profileSel = clampIndex(m.profileSel, len(m.profile.TopPosts))
		return m, nil
	case membersLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		// If user navigated away from this community while loading, ignore.
		if m.curCommunity != nil && m.curCommunity.ID != msg.communityID {
			return m, nil
		}
		m.members = msg.members
		m.membersSel = clampIndex(m.membersSel, len(m.members))
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
		return m, m.cmdLoadPinned()
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
			m.feedSort = store.SortHot
			m.feedTopRange = store.TopWeek
			m.feedNewCur = nil
			m.feedTopCur = nil
			m.feedHotCur = nil
			m.feedHasMore = false
			m.feedLoading = true
			m.feedSpinIdx = 0
			m.feedPending = 0
			m.focus = focusMain
			m.mainFocus = mainInput
			m.input.Focus()
			return m, tea.Batch(m.cmdLoadPinned(), m.cmdLoadFeedReset(), m.cmdLoadUnread())
		}
		m.v = viewCommunity
		return m, m.cmdLoadUnread()
	case feedLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			m.feedLoading = false
			return m, nil
		}
		if msg.append {
			m.feed = append(m.feed, msg.items...)
		} else {
			m.feed = msg.items
			m.feedSel = 0
			m.feedPending = 0
		}
		if m.feedSel >= len(m.feed) {
			m.feedSel = max(0, len(m.feed)-1)
		}
		m.feedNewCur = msg.nextNew
		m.feedTopCur = msg.nextTop
		m.feedHotCur = msg.nextHot
		m.feedHasMore = msg.hasMore
		m.feedLoading = false
		if m.v == viewRoom && m.curRoom != nil && len(m.feed) > 0 {
			id := newestMessageID(m.feed)
			return m, tea.Batch(m.cmdMarkRead(m.curRoom.ID, id), m.cmdLoadUnread())
		}
		return m, nil
	case homeLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			m.homeLoading = false
			return m, nil
		}
		if msg.append {
			m.homeItems = append(m.homeItems, msg.items...)
		} else {
			m.homeItems = msg.items
			m.homeOffset = 0
			m.homeSel = 0
			m.homeNewPending = 0
			m.homeNewCur = nil
			m.homeTopCur = nil
			m.homeHotCur = nil
		}
		if m.homeSel >= len(m.homeItems) {
			m.homeSel = max(0, len(m.homeItems)-1)
		}
		m.homeNewCur = msg.nextNew
		m.homeTopCur = msg.nextTop
		m.homeHotCur = msg.nextHot
		m.homeHasMore = msg.hasMore
		m.homeLoading = false
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
		if m.searchMatchID != "" {
			for i := range m.threadItems {
				if m.threadItems[i].msg.ID == m.searchMatchID {
					m.threadSel = i
					break
				}
			}
			m.searchMatchID = ""
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
	case roomCreatedMsg:
		if msg.err != nil {
			if errors.Is(msg.err, store.ErrLimit) {
				m.flashErr("Room limit reached (max 20).")
				return m, nil
			}
			m.flashErr("Could not create room.")
			return m, nil
		}
		m.curRoom = msg.room
		m.v = viewRoom
		m.feedSort = store.SortHot
		m.feedTopRange = store.TopWeek
		m.feedNewCur = nil
		m.feedTopCur = nil
		m.feedHotCur = nil
		m.feedHasMore = false
		m.feedLoading = true
		m.feedSpinIdx = 0
		m.feedPending = 0
		m.focus = focusMain
		m.mainFocus = mainInput
		m.input.Focus()
		m.push(nav{v: viewCommunity, communityID: msg.communityID, roomSel: m.roomSel})
		return m, tea.Batch(m.cmdLoadRooms(msg.communityID), m.cmdLoadFeedReset(), m.cmdLoadUnread())
	case leftCommunityMsg:
		if msg.err != nil {
			m.flashErr("Could not leave community.")
			return m, nil
		}
		m.v = viewHome
		m.curCommunity = nil
		m.curRoom = nil
		m.threadRootID = ""
		m.replyToID = nil
		m.replyToUsername = ""
		m.homeLoading = true
		return m, tea.Batch(m.cmdLoadJoined(), m.cmdLoadUnread(), m.cmdLoadHomeReset())
	case pinnedLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.pinned = msg.msg
		return m, nil
	case roomDeletedMsg:
		if msg.err != nil {
			m.flashErr("Could not delete room.")
			return m, nil
		}
		// If we were in that room, go back to community view.
		if m.curRoom != nil && m.curRoom.ID == msg.roomID {
			m.v = viewCommunity
			m.curRoom = nil
			m.pinned = nil
			return m, m.cmdLoadRooms(msg.communityID)
		}
		return m, m.cmdLoadRooms(msg.communityID)
	case communityDeletedMsg:
		if msg.err != nil {
			m.flashErr("Could not delete community.")
			return m, nil
		}
		m.v = viewHome
		m.curCommunity = nil
		m.curRoom = nil
		m.pinned = nil
		m.profile = nil
		m.members = nil
		m.mentions = nil
		m.homeLoading = true
		return m, tea.Batch(m.cmdLoadJoined(), m.cmdLoadUnread(), m.cmdLoadHomeReset())
	case mentionsLoadedMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		m.mentions = msg.items
		m.mentionsSel = clampIndex(m.mentionsSel, len(m.mentions))
		return m, nil
	case deleteRoomResolvedMsg:
		if msg.err != nil || msg.room == nil {
			m.flashErr("Room not found.")
			return m, nil
		}
		m.deleteRoomConfirm = true
		m.deleteRoomID = msg.room.ID
		m.deleteRoomName = msg.room.Name
		m.flashErr(fmt.Sprintf("Delete #%s and all messages? (y/n)", msg.room.Name))
		return m, nil
	case postedMsg:
		if msg.err != nil {
			m.flashErr("Could not post.")
			return m, nil
		}
		m.input.SetValue("")
		m.replyToID = nil
		m.replyToUsername = ""
		m.input.Placeholder = "/help"
		m.feedLoading = true
		return m, m.cmdLoadFeedReset()
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
			m.feedLoading = true
			return m, tea.Batch(m.cmdLoadPinned(), m.cmdLoadFeedReset())
		default:
			return m, nil
		}
	case pinnedSetMsg:
		if msg.err != nil {
			m.flashErr("Something went wrong. Try again.")
			return m, nil
		}
		return m, m.cmdLoadPinned()
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
			if m.curRoom != nil && em.evt.RoomID == m.curRoom.ID && em.evt.Type == pubsub.EventNewMessage {
				if m.feedSort == store.SortNew {
					return m, tea.Batch(m.cmdLoadFeedReset(), m.cmdLoadUnread(), waitForEvent(m.events))
				}
				m.feedPending++
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
		if m.mainFocus == mainSearch {
			switch km.String() {
			case "esc":
				m.searchOpen = false
				m.searchLoading = false
				m.searchErr = ""
				m.mainFocus = mainNav
				m.searchInput.Blur()
				return m, nil
			case "up":
				if m.searchSel > 0 {
					m.searchSel--
				}
				return m, nil
			case "down":
				if m.searchSel < len(m.searchResults)-1 {
					m.searchSel++
				}
				return m, nil
			case "enter":
				return m, m.cmdOpenSelectedSearchResult()
			}

			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(km)
			m.searchQuery = strings.TrimSpace(m.searchInput.Value())
			m.searchToken++
			token := m.searchToken
			q := m.searchQuery
			m.searchLoading = q != ""
			return m, tea.Batch(cmd, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
				return searchDebounceMsg{token: token, q: q}
			}))
		}

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

		if m.leaveConfirm {
			switch km.String() {
			case "y":
				m.leaveConfirm = false
				cid := m.leaveCommunityID
				m.leaveCommunityID = ""
				m.leaveCommunity = ""
				return m, m.cmdLeaveCommunity(cid)
			case "n", "esc":
				m.leaveConfirm = false
				m.leaveCommunityID = ""
				m.leaveCommunity = ""
				return m, nil
			default:
				return m, nil
			}
		}

		if m.deleteRoomConfirm {
			switch km.String() {
			case "y":
				m.deleteRoomConfirm = false
				rid := m.deleteRoomID
				m.deleteRoomID = ""
				m.deleteRoomName = ""
				return m, m.cmdDeleteRoom(rid)
			case "n", "esc":
				m.deleteRoomConfirm = false
				m.deleteRoomID = ""
				m.deleteRoomName = ""
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
		case "ctrl+r":
			switch m.v {
			case viewHome:
				m.homeLoading = true
				return m, m.cmdLoadHomeReset()
			case viewRoom:
				m.feedLoading = true
				return m, m.cmdLoadFeedReset()
			case viewThread:
				if m.threadRootID != "" {
					return m, m.cmdLoadThread(m.threadRootID)
				}
				return m, nil
			default:
				return m, nil
			}
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
			// Command autocomplete: when typing a command, Tab completes the current suggestion.
			// To switch focus while typing, press Esc to return to nav mode, then Tab.
			if m.focus == focusMain && m.mainFocus == mainInput && m.cmdSuggestOpen && len(m.cmdSuggest) > 0 {
				s := m.cmdSuggest[clampIndex(m.cmdSuggestSel, len(m.cmdSuggest))]
				val := s.Text
				if s.ExpectsArg && !strings.HasSuffix(val, " ") {
					val += " "
				}
				m.input.SetValue(val)
				m.updateCmdSuggest()
				return m, nil
			}
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
				m.clearCmdSuggest()
				m.deleteCommunityConfirm = false
				m.deleteCommunityID = ""
				m.deleteCommunityName = ""
				return m, nil
			}
			return (&m).pop()
		}

		// Open search from anywhere when the main input isn't focused.
		if km.Type == tea.KeyRunes && len(km.Runes) == 1 && km.Runes[0] == '/' && m.focus == focusMain && m.mainFocus == mainNav {
			m.openSearch("")
			return m, nil
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
				m.homeLoading = true
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
					m.homeLoading = true
					return m, m.cmdLoadHomeReset()
				}
				if m.v == viewRoom {
					m.cycleFeedSort()
					m.feedLoading = true
					return m, m.cmdLoadFeedReset()
				}
				return m, nil
			case "t":
				if m.v == viewHome && m.homeSort == store.SortTop {
					m.cycleHomeTopRange()
					m.homeLoading = true
					return m, m.cmdLoadHomeReset()
				}
				if m.v == viewRoom && m.feedSort == store.SortTop {
					m.cycleFeedTopRange()
					m.feedLoading = true
					return m, m.cmdLoadFeedReset()
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
					if m.homeSel >= len(m.homeItems)-1 && m.homeHasMore && !m.homeLoading {
						m.homeLoading = true
						return m, m.cmdLoadHomeMore()
					}
				}
				if m.homeSel >= len(m.homeItems)-1 && m.homeHasMore && !m.homeLoading {
					m.homeLoading = true
					return m, m.cmdLoadHomeMore()
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
				m.feedSort = store.SortHot
				m.feedTopRange = store.TopWeek
				m.feedNewCur = nil
				m.feedTopCur = nil
				m.feedHotCur = nil
				m.feedHasMore = false
				m.feedLoading = true
				m.feedSpinIdx = 0
				m.feedPending = 0
				m.push(nav{v: viewCommunity, communityID: m.curCommunity.ID, roomSel: m.roomSel})
				return m, m.cmdLoadFeedReset()
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
					if m.feedSel >= len(m.feed)-1 && m.feedHasMore && !m.feedLoading {
						m.feedLoading = true
						return m, m.cmdLoadFeedMore()
					}
					return m, nil
				}
				if m.feedHasMore && !m.feedLoading {
					m.feedLoading = true
					return m, m.cmdLoadFeedMore()
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
		case viewProfile:
			switch km.String() {
			case "up":
				if m.profileSel > 0 {
					m.profileSel--
				}
				return m, nil
			case "down":
				if m.profile != nil && m.profileSel < len(m.profile.TopPosts)-1 {
					m.profileSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				if m.profile == nil || m.profileSel < 0 || m.profileSel >= len(m.profile.TopPosts) {
					return m, nil
				}
				it := m.profile.TopPosts[m.profileSel]
				m.v = viewThread
				m.push(nav{v: viewProfile})
				return m, tea.Batch(m.cmdLoadRoomContext(it.CommunityID, it.RoomID), m.cmdLoadThread(it.ID))
			}
		case viewMembers:
			switch km.String() {
			case "up":
				if m.membersSel > 0 {
					m.membersSel--
				}
				return m, nil
			case "down":
				if m.membersSel < len(m.members)-1 {
					m.membersSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				if m.membersSel < 0 || m.membersSel >= len(m.members) {
					return m, nil
				}
				u := m.members[m.membersSel]
				prev := m.v
				m.v = viewProfile
				m.profile = nil
				m.profileSel = 0
				m.push(nav{v: prev})
				return m, m.cmdLoadProfile(u)
			}
		case viewMentions:
			switch km.String() {
			case "up":
				if m.mentionsSel > 0 {
					m.mentionsSel--
				}
				return m, nil
			case "down":
				if m.mentionsSel < len(m.mentions)-1 {
					m.mentionsSel++
				}
				return m, nil
			case "enter":
				if m.mainFocus != mainNav {
					goto input
				}
				return m, m.cmdOpenSelectedMention()
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
		if m.cmdSuggestOpen && len(m.cmdSuggest) > 0 {
			switch km.String() {
			case "up":
				if m.cmdSuggestSel > 0 {
					m.cmdSuggestSel--
				}
				return m, nil
			case "down":
				if m.cmdSuggestSel < len(m.cmdSuggest)-1 {
					m.cmdSuggestSel++
				}
				return m, nil
			}
		}
		switch km.String() {
		case "up", "down", "left", "right", "pgup", "pgdown", "home", "end":
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateCmdSuggest()
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
	if m.focus == focusMain && m.mainFocus == mainInput && !m.searchOpen {
		b.WriteString(m.viewCmdSuggest())
	}

	switch m.v {
	default:
		if m.searchOpen {
			b.WriteString(m.viewSearch())
			break
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
		case viewProfile:
			b.WriteString(m.viewProfile())
		case viewMembers:
			b.WriteString(m.viewMembers())
		case viewMentions:
			b.WriteString(m.viewMentions())
		}
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

func (m *Model) clearCmdSuggest() {
	m.cmdSuggestOpen = false
	m.cmdSuggestSel = 0
	m.cmdSuggest = nil
}

func (m *Model) updateCmdSuggest() {
	if m.createStep != createNone {
		m.clearCmdSuggest()
		return
	}
	if m.focus != focusMain || m.mainFocus != mainInput || m.searchOpen {
		m.clearCmdSuggest()
		return
	}

	raw := strings.TrimSpace(m.input.Value())
	if raw == "" || !strings.HasPrefix(raw, "/") {
		m.clearCmdSuggest()
		return
	}

	// Only support single-line command completion.
	if strings.Contains(raw, "\n") {
		m.clearCmdSuggest()
		return
	}

	type cmdSpec struct {
		Name       string
		ExpectsArg bool
	}
	specs := []cmdSpec{
		{Name: "help"},
		{Name: "explore"},
		{Name: "join", ExpectsArg: true},
		{Name: "leave", ExpectsArg: true},
		{Name: "create-community"},
		{Name: "create-room", ExpectsArg: true},
		{Name: "rooms"},
		{Name: "members"},
		{Name: "mentions"},
		{Name: "me"},
		{Name: "user", ExpectsArg: true},
		{Name: "pin"},
		{Name: "unpin"},
		{Name: "delete-room", ExpectsArg: true},
		{Name: "delete-community"},
		{Name: "search", ExpectsArg: true},
		{Name: "quit"},
		{Name: "q"},
	}

	parts := strings.SplitN(raw, " ", 2)
	cmdPart := strings.TrimPrefix(parts[0], "/")
	cmdLower := strings.ToLower(cmdPart)

	var out []cmdSuggestion
	if len(parts) == 1 {
		for _, s := range specs {
			if strings.HasPrefix(s.Name, cmdLower) {
				out = append(out, cmdSuggestion{Text: "/" + s.Name, ExpectsArg: s.ExpectsArg})
			}
		}
	} else {
		argPrefix := strings.ToLower(strings.TrimSpace(parts[1]))
		switch cmdLower {
		case "join":
			seen := make(map[string]bool, len(m.expl)+len(m.joined))
			for _, c := range m.expl {
				seen[c.Name] = true
				if strings.HasPrefix(c.Name, argPrefix) {
					out = append(out, cmdSuggestion{Text: "/join " + c.Name})
				}
			}
			// If explore isn't loaded, fall back to joined list for completion.
			if len(m.expl) == 0 {
				for _, c := range m.joined {
					if seen[c.Name] {
						continue
					}
					if strings.HasPrefix(c.Name, argPrefix) {
						out = append(out, cmdSuggestion{Text: "/join " + c.Name})
					}
				}
			}
		case "leave":
			for _, c := range m.joined {
				if strings.HasPrefix(c.Name, argPrefix) {
					out = append(out, cmdSuggestion{Text: "/leave " + c.Name})
				}
			}
		}
	}

	if len(out) > 5 {
		out = out[:5]
	}
	if len(out) == 0 {
		m.clearCmdSuggest()
		return
	}
	m.cmdSuggestOpen = true
	m.cmdSuggest = out
	m.cmdSuggestSel = clampIndex(m.cmdSuggestSel, len(m.cmdSuggest))
}

func (m Model) viewCmdSuggest() string {
	if !m.cmdSuggestOpen || len(m.cmdSuggest) == 0 {
		return ""
	}
	selStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	var b strings.Builder
	for i, s := range m.cmdSuggest {
		line := s.Text
		if s.ExpectsArg && !strings.HasSuffix(line, " ") {
			line += " "
		}
		prefix := "  "
		if i == m.cmdSuggestSel {
			prefix = "> "
			line = selStyle.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

func renderHighlightMarkers(s string) string {
	hl := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	var b strings.Builder
	for {
		i := strings.Index(s, "<hl>")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i+len("<hl>"):]
		j := strings.Index(s, "</hl>")
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(hl.Render(s[:j]))
		s = s[j+len("</hl>"):]
	}
	return b.String()
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
	case viewProfile:
		if m.profile != nil {
			loc = fmt.Sprintf("@%s", m.profile.Username)
		} else {
			loc = "profile"
		}
	case viewMembers:
		if m.curCommunity != nil {
			loc = fmt.Sprintf("%s · members", m.curCommunity.Name)
		} else {
			loc = "members"
		}
	case viewMentions:
		loc = "mentions"
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
		if m.homeLoading {
			b.WriteString(spinnerFrames[m.feedSpinIdx%len(spinnerFrames)])
			b.WriteString("\n")
			return b.String()
		}
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
	if m.homeLoading {
		b.WriteString(spinnerFrames[m.feedSpinIdx%len(spinnerFrames)])
		b.WriteString("\n\n")
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
	b.WriteString(fmt.Sprintf("Room · %s", m.feedSort))
	if m.feedSort == store.SortTop {
		b.WriteString(fmt.Sprintf(" · %s", m.feedTopRange))
	}
	b.WriteString("\n\n")
	if m.pinned != nil {
		author := m.pinned.AuthorUsername
		content := m.pinned.Content
		if m.pinned.IsDeleted {
			author = "[deleted]"
			content = "[deleted]"
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("PINNED"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s · %s  (%d↑ %d💬)\n", author, relTime(m.pinned.CreatedAt), m.pinned.Upvotes, m.pinned.Replies))
		b.WriteString(wrap(content, max(20, mw-6)))
		b.WriteString("\n\n")
	}
	if m.feedPending > 0 && m.feedSort != store.SortNew {
		b.WriteString(fmt.Sprintf("%d new posts  (new sort auto-refreshes)\n\n", m.feedPending))
	}
	if len(m.feed) == 0 {
		if m.feedLoading {
			b.WriteString(spinnerFrames[m.feedSpinIdx%len(spinnerFrames)])
			b.WriteString("\n")
		} else {
			b.WriteString("(no messages)\n")
		}
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
	if m.feedLoading {
		b.WriteString(spinnerFrames[m.feedSpinIdx%len(spinnerFrames)])
		b.WriteString("\n")
	}
	b.WriteString("Nav: up/down select, Enter thread, r reply, u upvote, d delete, Esc back. s cycle sort, t cycle top range. Tab toggles input/nav.\n")
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

func (m Model) viewSearch() string {
	mw := m.mainWidth()
	var b strings.Builder
	b.WriteString("Search: ")
	b.WriteString(m.searchInput.View())
	b.WriteString("\n\n")

	if m.searchErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(m.searchErr))
		b.WriteString("\n\n")
	}

	if strings.TrimSpace(m.searchQuery) == "" {
		b.WriteString("(type to search)\n")
		return b.String()
	}

	if m.searchLoading {
		b.WriteString(spinnerFrames[m.feedSpinIdx%len(spinnerFrames)])
		b.WriteString("\n\n")
	}

	if len(m.searchResults) == 0 && !m.searchLoading {
		b.WriteString("No results found\n")
		return b.String()
	}

	for i, it := range m.searchResults {
		sel := "  "
		if i == m.searchSel {
			sel = "> "
		}
		author := it.AuthorUsername
		if it.IsDeleted {
			author = "[deleted]"
		}
		content := it.HighlightedContent
		if content == "" {
			content = it.Content
		}
		if it.IsDeleted {
			content = "[deleted]"
		}
		b.WriteString(fmt.Sprintf("%s%s · #%s · %s · %s\n", sel, it.CommunityName, it.RoomName, author, relTime(it.CreatedAt)))
		b.WriteString(wrap(renderHighlightMarkers(content), max(20, mw-6)))
		b.WriteString(fmt.Sprintf("\n(%d↑ %d💬)\n\n", it.Upvotes, it.Replies))
	}
	if m.searchTrunc {
		b.WriteString("Showing first 50 results. Narrow your search.\n")
	}
	b.WriteString("Nav: up/down select, Enter open thread, Esc close search.\n")
	return b.String()
}

func (m Model) viewProfile() string {
	if m.profile == nil {
		return "(no profile)\n"
	}
	p := m.profile
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s · joined %s\n\n", p.Username, relTime(p.CreatedAt)))
	b.WriteString(fmt.Sprintf("Posts: %d · Replies: %d · Upvotes received: %d\n", p.Posts, p.Replies, p.UpvotesRec))
	if len(p.ActiveIn) > 0 {
		b.WriteString("Active in: ")
		b.WriteString(strings.Join(p.ActiveIn, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\nTop posts:\n\n")
	if len(p.TopPosts) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, it := range p.TopPosts {
		prefix := "  "
		if i == m.profileSel {
			prefix = "> "
		}
		content := it.Content
		if it.IsDeleted {
			content = "[deleted]"
		}
		// First line preview.
		if j := strings.IndexByte(content, '\n'); j >= 0 {
			content = content[:j]
		}
		content = strings.TrimSpace(content)
		if content == "" {
			content = "(empty)"
		}
		b.WriteString(fmt.Sprintf("%s%s  (%d↑)\n", prefix, content, it.Upvotes))
	}
	b.WriteString("\nEnter opens thread. Esc goes back.\n")
	return b.String()
}

func (m Model) viewMembers() string {
	if m.curCommunity == nil {
		return "No community selected.\n"
	}
	var b strings.Builder
	b.WriteString("Members\n\n")
	if len(m.members) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, u := range m.members {
		prefix := "  "
		if i == m.membersSel {
			prefix = "> "
		}
		b.WriteString(prefix + u + "\n")
	}
	b.WriteString("\nEnter opens profile. Esc goes back.\n")
	return b.String()
}

func (m Model) viewMentions() string {
	mw := m.mainWidth()
	var b strings.Builder
	b.WriteString("Mentions\n\n")
	if len(m.mentions) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, it := range m.mentions {
		sel := "  "
		if i == m.mentionsSel {
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
	b.WriteString("Enter opens thread. Esc goes back.\n")
	return b.String()
}

func (m *Model) handleEnter() tea.Cmd {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return nil
	}
	m.clearCmdSuggest()

	if m.createStep != createNone {
		return m.handleCreateFlow(line)
	}

	if m.deleteCommunityConfirm {
		// Double-confirm: user must type the community name exactly (case-insensitive).
		expected := m.deleteCommunityName
		cid := m.deleteCommunityID
		m.deleteCommunityConfirm = false
		m.deleteCommunityID = ""
		m.deleteCommunityName = ""
		m.input.SetValue("")
		m.input.Placeholder = "/help"
		if strings.EqualFold(strings.TrimSpace(line), expected) {
			return m.cmdDeleteCommunity(cid)
		}
		m.flashErr("Delete canceled.")
		return nil
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
	switch strings.ToLower(cmd.Name) {
	case "help":
		m.flashErr("Commands: /explore /join <community> /leave <community> /create-community /create-room <name> /rooms /members /mentions /me /user <name> /pin /unpin /delete-room <name> /delete-community /search <query> /quit (/q)")
		return nil
	case "explore":
		m.v = viewExplore
		return m.cmdLoadExplore()
	case "search":
		// /search <query...>
		q := strings.TrimSpace(strings.Join(cmd.Args, " "))
		m.openSearch(q)
		if q == "" {
			return nil
		}
		m.searchLoading = true
		return m.cmdSearch(q)
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
	case "leave":
		if len(cmd.Args) != 1 {
			m.flashErr("Usage: /leave community")
			return nil
		}
		target := cmd.Args[0]
		var found *store.Community
		for i := range m.joined {
			if strings.EqualFold(m.joined[i].Name, target) {
				found = &m.joined[i]
				break
			}
		}
		if found == nil {
			m.flashErr("Not a member of that community.")
			return nil
		}
		m.leaveConfirm = true
		m.leaveCommunityID = found.ID
		m.leaveCommunity = found.Name
		m.flashErr(fmt.Sprintf("Leave %s? (y/n)", found.Name))
		return nil
	case "rooms":
		if m.curCommunity == nil {
			m.flashErr("No community selected.")
			return nil
		}
		prev := m.v
		m.v = viewCommunity
		m.push(nav{v: prev})
		m.focus = focusMain
		m.mainFocus = mainNav
		m.input.Blur()
		return m.cmdLoadRooms(m.curCommunity.ID)
	case "members":
		if m.curCommunity == nil {
			m.flashErr("No community selected.")
			return nil
		}
		prev := m.v
		m.v = viewMembers
		m.members = nil
		m.membersSel = 0
		m.push(nav{v: prev})
		m.focus = focusMain
		m.mainFocus = mainNav
		m.input.Blur()
		return m.cmdLoadMembers(m.curCommunity.ID)
	case "create-room":
		if len(cmd.Args) != 1 {
			m.flashErr("Usage: /create-room name")
			return nil
		}
		if m.curCommunity == nil {
			m.flashErr("No community selected.")
			return nil
		}
		name := strings.TrimPrefix(cmd.Args[0], "#")
		if !communityNameRe.MatchString(name) {
			m.flashErr("Invalid room name (2-30, lowercase letters/numbers/hyphens; start with letter)")
			return nil
		}
		// TODO(phase 6): enforce admin role.
		return m.cmdCreateRoom(m.curCommunity.ID, name)
	case "mentions":
		if m.user == nil {
			m.flashErr("Not authenticated.")
			return nil
		}
		prev := m.v
		m.v = viewMentions
		m.mentions = nil
		m.mentionsSel = 0
		m.push(nav{v: prev})
		m.focus = focusMain
		m.mainFocus = mainNav
		m.input.Blur()
		return m.cmdLoadMentions()
	case "me":
		if m.user == nil {
			m.flashErr("Not authenticated.")
			return nil
		}
		prev := m.v
		m.v = viewProfile
		m.profile = nil
		m.profileSel = 0
		m.push(nav{v: prev})
		return m.cmdLoadProfile(m.user.Username)
	case "user":
		if len(cmd.Args) != 1 {
			m.flashErr("Usage: /user username")
			return nil
		}
		prev := m.v
		m.v = viewProfile
		m.profile = nil
		m.profileSel = 0
		m.push(nav{v: prev})
		return m.cmdLoadProfile(cmd.Args[0])
	case "pin":
		if m.curRoom == nil {
			m.flashErr("No room selected.")
			return nil
		}
		// TODO(phase 6): enforce admin role.
		id := m.selectedMessageID()
		if id == "" {
			m.flashErr("No message selected.")
			return nil
		}
		return m.cmdPin(m.curRoom.ID, id)
	case "unpin":
		if m.curRoom == nil {
			m.flashErr("No room selected.")
			return nil
		}
		// TODO(phase 6): enforce admin role.
		return m.cmdUnpin(m.curRoom.ID)
	case "delete-room":
		if len(cmd.Args) != 1 {
			m.flashErr("Usage: /delete-room name")
			return nil
		}
		if m.curCommunity == nil {
			m.flashErr("No community selected.")
			return nil
		}
		// TODO(phase 6): enforce admin role.
		name := strings.TrimPrefix(cmd.Args[0], "#")
		return m.cmdResolveRoomForDelete(m.curCommunity.ID, name)
	case "delete-community":
		if m.curCommunity == nil {
			m.flashErr("No community selected.")
			return nil
		}
		// TODO(phase 6): enforce admin role.
		m.deleteCommunityConfirm = true
		m.deleteCommunityID = m.curCommunity.ID
		m.deleteCommunityName = m.curCommunity.Name
		m.input.SetValue("")
		m.input.Placeholder = "type community name to confirm"
		m.flashErr(fmt.Sprintf("Type %q to confirm community deletion.", m.curCommunity.Name))
		return nil
	case "quit", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return tea.Quit
	default:
		m.flashErr("Unknown command. Type /help for available commands.")
		return nil
	}
}

func (m *Model) push(n nav) {
	m.stack = append(m.stack, n)
}

func (m *Model) openSearch(initialQuery string) {
	m.searchOpen = true
	m.searchErr = ""
	m.searchResults = nil
	m.searchSel = 0
	m.searchTrunc = false
	m.searchQuery = strings.TrimSpace(initialQuery)
	m.searchInput.SetValue(m.searchQuery)
	m.searchInput.Focus()
	m.mainFocus = mainSearch
	m.focus = focusMain
	m.input.Blur()
}

func (m Model) mainX0() int {
	if m.sidebarVisible() {
		return components.SidebarWidth + 1 // plus separator
	}
	return 1 // padding when sidebar hidden
}

func (m Model) contentStartY() int {
	// header() ends with "\n\n": 2 lines (header + blank)
	y := 2
	if m.errMsg != "" && time.Now().Before(m.errUntil) {
		// err line + blank line
		y += 2
	}
	if m.cmdSuggestOpen && m.focus == focusMain && m.mainFocus == mainInput && !m.searchOpen && len(m.cmdSuggest) > 0 {
		// suggestions block + blank line
		y += len(m.cmdSuggest) + 1
	}
	return y
}

func (m *Model) focusInput() {
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	m.updateCmdSuggest()
	m.searchOpen = false
	m.searchInput.Blur()
}

func (m Model) handleMouse(mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Only respond to clicks/wheel.
	if mm.Action != tea.MouseActionPress && !tea.MouseEvent(mm).IsWheel() {
		return m, nil
	}

	// Click on the input area should focus input.
	if mm.Button == tea.MouseButtonLeft && mm.Action == tea.MouseActionPress {
		// Bottom area: separator + optional "replying to" context + input
		if mm.Y >= m.height-2 {
			(&m).focusInput()
			return m, nil
		}
	}

	// Wheel routing based on cursor position.
	// If the wheel event happens over the main content, treat it as main scrolling even if sidebar is focused.
	if tea.MouseEvent(mm).IsWheel() {
		x0 := m.mainX0()
		// If search overlay is open, defer to the search handler (it has its own scroll/selection).
		if mm.X >= x0 && !m.searchOpen {
			// Wheel over the main content implies main navigation, regardless of where focus was.
			// This avoids the "sidebar focused but I'm clearly scrolling the feed" mismatch.
			m.focus = focusMain
			m.mainFocus = mainNav
			m.input.Blur()
			return m.handleMouseWheelMainAnyFocus(mm)
		}
	}

	// Sidebar interactions.
	if m.sidebarVisible() && mm.X < components.SidebarWidth {
		switch mm.Button {
		case tea.MouseButtonWheelUp:
			m.focus = focusSidebar
			m.mainFocus = mainNav
			m.input.Blur()
			m.sidebar.Selected = max(0, m.sidebar.Selected-3)
			m.joinedSel = m.sidebar.Selected
			return m, nil
		case tea.MouseButtonWheelDown:
			m.focus = focusSidebar
			m.mainFocus = mainNav
			m.input.Blur()
			m.sidebar.Selected = min(len(m.joined)-1, m.sidebar.Selected+3)
			m.joinedSel = m.sidebar.Selected
			return m, nil
		case tea.MouseButtonLeft:
			// Items start at y=2 (header + blank).
			i := mm.Y - 2
			if i < 0 || i >= len(m.joined) {
				return m, nil
			}
			m.focus = focusSidebar
			m.mainFocus = mainNav
			m.input.Blur()
			m.sidebar.Selected = i
			m.joinedSel = i
			c := m.joined[i]
			return m, m.cmdOpenCommunityDefault(c.ID)
		default:
			return m, nil
		}
	}

	// Main area interactions.
	x0 := m.mainX0()
	if mm.X < x0 {
		return m, nil
	}
	y0 := m.contentStartY()
	mainY := mm.Y - y0
	if mainY < 0 {
		return m, nil
	}

	if m.searchOpen {
		return m.handleMouseSearch(mainY, mm)
	}

	// Wheel scroll in main areas adjusts selection like keyboard nav.
	if tea.MouseEvent(mm).IsWheel() {
		return m.handleMouseWheelMain(mm)
	}

	if mm.Button != tea.MouseButtonLeft || mm.Action != tea.MouseActionPress {
		return m, nil
	}

	// Click-to-select / click-to-open.
	switch m.v {
	case viewHome:
		return m.handleMouseHomeClick(mainY, mm.X-x0)
	case viewRoom:
		return m.handleMouseRoomClick(mainY, mm.X-x0)
	case viewThread:
		return m.handleMouseThreadClick(mainY, mm.X-x0)
	case viewCommunities:
		return m.handleMouseSimpleListClick(mainY, len(m.joined), func(i int) (tea.Model, tea.Cmd) {
			m.joinedSel = i
			if len(m.joined) == 0 {
				return m, nil
			}
			c := m.joined[i]
			m.curCommunity = &c
			m.v = viewCommunity
			m.push(nav{v: viewCommunities, communitySel: i})
			return m, m.cmdLoadRooms(c.ID)
		})
	case viewExplore:
		return m.handleMouseSimpleListClick(mainY, len(m.expl), func(i int) (tea.Model, tea.Cmd) {
			m.explSel = i
			if len(m.expl) == 0 {
				return m, nil
			}
			c := m.expl[i]
			return m, m.cmdJoinCommunity(c.ID)
		})
	case viewCommunity:
		return m.handleMouseSimpleListClick(mainY, len(m.rooms), func(i int) (tea.Model, tea.Cmd) {
			m.roomSel = i
			if len(m.rooms) == 0 {
				return m, nil
			}
			r := m.rooms[i]
			m.curRoom = &r
			m.v = viewRoom
			m.feedSort = store.SortHot
			m.feedTopRange = store.TopWeek
			m.feedNewCur = nil
			m.feedTopCur = nil
			m.feedHotCur = nil
			m.feedHasMore = false
			m.feedLoading = true
			m.feedSpinIdx = 0
			m.feedPending = 0
			m.push(nav{v: viewCommunity, communityID: m.curCommunity.ID, roomSel: i})
			return m, m.cmdLoadFeedReset()
		})
	default:
		return m, nil
	}
}

func (m Model) handleMouseWheelMain(mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.focus != focusMain || m.mainFocus != mainNav {
		return m, nil
	}
	return m.handleMouseWheelMainAnyFocus(mm)
}

func (m Model) handleMouseWheelMainAnyFocus(mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	delta := 3
	if mm.Button == tea.MouseButtonWheelUp {
		delta = -3
	}
	switch m.v {
	case viewHome:
		m.homeSel = clampIndex(m.homeSel+delta, len(m.homeItems))
		if m.homeSel >= len(m.homeItems)-1 && m.homeHasMore && !m.homeLoading {
			m.homeLoading = true
			return m, m.cmdLoadHomeMore()
		}
		return m, nil
	case viewRoom:
		m.feedSel = clampIndex(m.feedSel+delta, len(m.feed))
		if m.feedSel >= len(m.feed)-1 && m.feedHasMore && !m.feedLoading {
			m.feedLoading = true
			return m, m.cmdLoadFeedMore()
		}
		return m, nil
	case viewThread:
		m.threadSel = clampIndex(m.threadSel+delta, len(m.threadItems))
		return m, nil
	case viewCommunities:
		m.joinedSel = clampIndex(m.joinedSel+delta, len(m.joined))
		return m, nil
	case viewExplore:
		m.explSel = clampIndex(m.explSel+delta, len(m.expl))
		return m, nil
	case viewCommunity:
		m.roomSel = clampIndex(m.roomSel+delta, len(m.rooms))
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleMouseSearch(mainY int, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Search view:
	// 0: "Search: <input>"
	// 1: blank
	if tea.MouseEvent(mm).IsWheel() {
		delta := 3
		if mm.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		m.searchSel = clampIndex(m.searchSel+delta, len(m.searchResults))
		return m, nil
	}
	if mm.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if mainY == 0 {
		m.focus = focusMain
		m.mainFocus = mainSearch
		m.searchInput.Focus()
		m.input.Blur()
		return m, nil
	}
	// Results start at y=2, but also have optional blocks for err/loading/empty states.
	y := mainY - 2
	if y < 0 {
		return m, nil
	}
	// Each result is 3+ lines: meta line, content (wrapped), votes line, blank line.
	// For hit-testing we approximate by selecting the closest item based on cumulative height.
	mw := m.mainWidth()
	w := max(20, mw-6)
	curY := 0
	for i, it := range m.searchResults {
		content := it.HighlightedContent
		if content == "" {
			content = it.Content
		}
		lines := 1 + lineCount(wrap(renderHighlightMarkers(content), w)) + 1 + 1
		if y >= curY && y < curY+lines {
			m.searchSel = i
			m.mainFocus = mainSearch
			m.searchInput.Focus()
			m.input.Blur()
			// Click opens the thread.
			return m, m.cmdOpenSelectedSearchResult()
		}
		curY += lines
	}
	return m, nil
}

func (m Model) handleMouseHomeClick(mainY int, relX int) (tea.Model, tea.Cmd) {
	// Home view layout:
	// 0: "Home · ..."
	// 1: blank
	// then items
	// Click in the first line cycles sort.
	if mainY == 0 {
		if m.homeSort == store.SortTop {
			m.cycleHomeTopRange()
		} else {
			m.cycleHomeSort()
		}
		m.homeLoading = true
		return m, m.cmdLoadHomeReset()
	}
	y := mainY - 2
	if y < 0 {
		return m, nil
	}
	mw := m.mainWidth()
	w := max(20, mw-6)
	curY := 0
	for i, it := range m.homeItems {
		content := it.Content
		lines := 1 + lineCount(wrap(content, w)) + 1 + 1
		if y >= curY && y < curY+lines {
			// Click on the selector column selects only; elsewhere opens.
			m.homeSel = i
			m.focus = focusMain
			m.mainFocus = mainNav
			m.input.Blur()
			if relX <= 2 {
				return m, nil
			}
			id := it.ID
			roomID := it.RoomID
			communityID := it.CommunityID
			m.v = viewThread
			m.push(nav{v: viewHome, homeSel: i})
			return m, tea.Batch(m.cmdLoadRoomContext(communityID, roomID), m.cmdLoadThread(id))
		}
		curY += lines
	}
	return m, nil
}

func (m Model) handleMouseRoomClick(mainY int, relX int) (tea.Model, tea.Cmd) {
	// Room view layout:
	// 0: "Room · ..."
	// 1: blank
	// then items
	// Click in the first line cycles sort.
	if mainY == 0 {
		if m.feedSort == store.SortTop {
			m.cycleFeedTopRange()
		} else {
			m.cycleFeedSort()
		}
		m.feedLoading = true
		return m, m.cmdLoadFeedReset()
	}
	y := mainY - 2
	if y < 0 {
		return m, nil
	}
	mw := m.mainWidth()
	w := max(20, mw-6)
	curY := 0
	for i, it := range m.feed {
		content := it.Content
		lines := 1 + lineCount(wrap(content, w)) + 1
		if y >= curY && y < curY+lines {
			m.feedSel = i
			m.focus = focusMain
			m.mainFocus = mainNav
			m.input.Blur()
			if relX <= 2 {
				return m, nil
			}
			root := it.ID
			m.v = viewThread
			m.push(nav{v: viewRoom, roomID: m.curRoom.ID, feedSel: i})
			return m, m.cmdLoadThread(root)
		}
		curY += lines
	}
	return m, nil
}

func (m Model) handleMouseThreadClick(mainY int, relX int) (tea.Model, tea.Cmd) {
	// Thread layout:
	// 0: "← Back..."
	// 1: blank
	y := mainY - 2
	if y < 0 {
		return m, nil
	}
	mw := m.mainWidth()
	curY := 0
	for i, it := range m.threadItems {
		content := it.msg.Content
		if it.overflowCtx != "" {
			content = "replying to " + it.overflowCtx + ": " + content
		}
		w := max(20, mw-6-len(it.treePrefix))
		lines := 1 + lineCount(wrap(content, w)) + 1
		if y >= curY && y < curY+lines {
			m.threadSel = i
			m.focus = focusMain
			m.mainFocus = mainNav
			m.input.Blur()
			return m, nil
		}
		curY += lines
	}
	return m, nil
}

func (m Model) handleMouseSimpleListClick(mainY int, n int, onSelect func(i int) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	// Simple list views render a title line, blank line, then one item per line.
	i := mainY - 2
	if i < 0 || i >= n {
		return m, nil
	}
	m.focus = focusMain
	m.mainFocus = mainNav
	m.input.Blur()
	return onSelect(i)
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func (m *Model) pop() (tea.Model, tea.Cmd) {
	if len(m.stack) == 0 {
		return *m, nil
	}
	last := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	m.v = last.v
	if last.returnSearch {
		m.searchOpen = true
		m.mainFocus = mainSearch
		m.focus = focusMain
		m.searchInput.Focus()
		m.input.Blur()
		return *m, nil
	}
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
		m.feedLoading = true
		return *m, m.cmdLoadFeedReset()
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

type searchLoadedMsg struct {
	results []store.SearchResult
	err     error
}

type profileLoadedMsg struct {
	profile *store.UserProfile
	err     error
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

func (m Model) cmdLoadProfile(username string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p, err := m.st.GetUserProfile(ctx, username)
		return profileLoadedMsg{profile: p, err: err}
	}
}

type homeLoadedMsg struct {
	items   []store.FeedMessage
	append  bool
	hasMore bool
	nextNew *string
	nextTop *store.HomeTopCursor
	nextHot *store.HomeHotCursor
	err     error
}

func (m Model) cmdSearch(q string) tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		results, err := m.st.SearchMessages(ctx, userID, q, 51)
		return searchLoadedMsg{results: results, err: err}
	}
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
		switch sortMode {
		case store.SortNew:
			items, next, err := m.st.ListHomeNewPage(ctx, userID, 30, nil)
			return homeLoadedMsg{items: items, append: false, hasMore: next != nil, nextNew: next, err: err}
		case store.SortTop:
			items, next, err := m.st.ListHomeTopPage(ctx, userID, topRange, 30, nil)
			return homeLoadedMsg{items: items, append: false, hasMore: next != nil, nextTop: next, err: err}
		case store.SortHot:
			items, next, err := m.st.ListHomeHotPage(ctx, userID, time.Now().UTC(), 30, nil)
			return homeLoadedMsg{items: items, append: false, hasMore: next != nil, nextHot: next, err: err}
		default:
			items, next, err := m.st.ListHomeNewPage(ctx, userID, 30, nil)
			return homeLoadedMsg{items: items, append: false, hasMore: next != nil, nextNew: next, err: err}
		}
	}
}

func (m Model) cmdLoadHomeMore() tea.Cmd {
	if !m.homeHasMore {
		return nil
	}
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	sortMode := m.homeSort
	topRange := m.homeTopRange
	beforeNew := m.homeNewCur
	beforeTop := m.homeTopCur
	beforeHot := m.homeHotCur
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		switch sortMode {
		case store.SortNew:
			items, next, err := m.st.ListHomeNewPage(ctx, userID, 30, beforeNew)
			return homeLoadedMsg{items: items, append: true, hasMore: next != nil, nextNew: next, err: err}
		case store.SortTop:
			items, next, err := m.st.ListHomeTopPage(ctx, userID, topRange, 30, beforeTop)
			return homeLoadedMsg{items: items, append: true, hasMore: next != nil, nextTop: next, err: err}
		case store.SortHot:
			items, next, err := m.st.ListHomeHotPage(ctx, userID, time.Now().UTC(), 30, beforeHot)
			return homeLoadedMsg{items: items, append: true, hasMore: next != nil, nextHot: next, err: err}
		default:
			items, next, err := m.st.ListHomeNewPage(ctx, userID, 30, beforeNew)
			return homeLoadedMsg{items: items, append: true, hasMore: next != nil, nextNew: next, err: err}
		}
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

type membersLoadedMsg struct {
	communityID string
	members     []string
	err         error
}

func (m Model) cmdLoadMembers(communityID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ms, err := m.st.ListCommunityMembers(ctx, communityID, 100)
		return membersLoadedMsg{communityID: communityID, members: ms, err: err}
	}
}

type pinnedLoadedMsg struct {
	msg *store.FeedMessage
	err error
}

func (m Model) cmdLoadPinned() tea.Cmd {
	if m.curRoom == nil {
		return nil
	}
	roomID := m.curRoom.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		msg, err := m.st.GetPinnedMessage(ctx, roomID)
		return pinnedLoadedMsg{msg: msg, err: err}
	}
}

type mentionsLoadedMsg struct {
	items []store.FeedMessage
	err   error
}

func (m Model) cmdLoadMentions() tea.Cmd {
	if m.user == nil {
		return nil
	}
	userID := m.user.ID
	username := m.user.Username
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		items, err := m.st.ListMentions(ctx, userID, username, 50)
		return mentionsLoadedMsg{items: items, err: err}
	}
}

type openMentionMsg struct {
	rootID      string
	roomID      string
	communityID string
	err         error
}

func (m Model) cmdOpenSelectedMention() tea.Cmd {
	if m.mentionsSel < 0 || m.mentionsSel >= len(m.mentions) {
		return nil
	}
	match := m.mentions[m.mentionsSel]
	messageID := match.ID
	roomID := match.RoomID
	communityID := match.CommunityID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rootID, err := m.st.ResolveThreadRootID(ctx, messageID)
		if err != nil {
			return openMentionMsg{err: err}
		}
		if rootID == "" {
			return openMentionMsg{err: store.ErrNotFound}
		}
		return openMentionMsg{rootID: rootID, roomID: roomID, communityID: communityID}
	}
}

type pinnedSetMsg struct {
	err error
}

func (m Model) cmdPin(roomID, messageID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.PinMessage(ctx, roomID, messageID)
		return pinnedSetMsg{err: err}
	}
}

func (m Model) cmdUnpin(roomID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.UnpinRoom(ctx, roomID)
		return pinnedSetMsg{err: err}
	}
}

type deleteRoomResolvedMsg struct {
	room *store.Room
	err  error
}

func (m Model) cmdResolveRoomForDelete(communityID, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := m.st.GetRoomByName(ctx, communityID, name)
		return deleteRoomResolvedMsg{room: r, err: err}
	}
}

type roomDeletedMsg struct {
	roomID      string
	communityID string
	err         error
}

func (m Model) cmdDeleteRoom(roomID string) tea.Cmd {
	communityID := ""
	if m.curCommunity != nil {
		communityID = m.curCommunity.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.DeleteRoom(ctx, roomID)
		return roomDeletedMsg{roomID: roomID, communityID: communityID, err: err}
	}
}

type communityDeletedMsg struct {
	communityID string
	err         error
}

func (m Model) cmdDeleteCommunity(communityID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.DeleteCommunity(ctx, communityID)
		return communityDeletedMsg{communityID: communityID, err: err}
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
	items   []store.FeedMessage
	append  bool
	hasMore bool
	nextNew *string
	nextTop *store.RoomTopCursor
	nextHot *store.RoomHotCursor
	err     error
}

func (m Model) cmdLoadFeedReset() tea.Cmd {
	if m.curRoom == nil {
		return nil
	}
	roomID := m.curRoom.ID
	sortMode := m.feedSort
	topRange := m.feedTopRange
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		switch sortMode {
		case store.SortNew:
			items, next, err := m.st.ListRoomTopLevelNewPage(ctx, roomID, 30, nil)
			return feedLoadedMsg{items: items, append: false, hasMore: next != nil, nextNew: next, err: err}
		case store.SortTop:
			items, next, err := m.st.ListRoomTopLevelTopPage(ctx, roomID, topRange, 30, nil)
			return feedLoadedMsg{items: items, append: false, hasMore: next != nil, nextTop: next, err: err}
		case store.SortHot:
			items, next, err := m.st.ListRoomTopLevelHotPage(ctx, roomID, time.Now().UTC(), 30, nil)
			return feedLoadedMsg{items: items, append: false, hasMore: next != nil, nextHot: next, err: err}
		default:
			items, next, err := m.st.ListRoomTopLevelNewPage(ctx, roomID, 30, nil)
			return feedLoadedMsg{items: items, append: false, hasMore: next != nil, nextNew: next, err: err}
		}
	}
}

type openSearchResultMsg struct {
	rootID      string
	roomID      string
	communityID string
	matchID     string
	err         error
}

func (m Model) cmdOpenSelectedSearchResult() tea.Cmd {
	if !m.searchOpen || m.searchSel < 0 || m.searchSel >= len(m.searchResults) {
		return nil
	}
	sel := m.searchResults[m.searchSel]
	matchID := sel.ID
	roomID := sel.RoomID
	communityID := sel.CommunityID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rootID, err := m.st.ResolveThreadRootID(ctx, matchID)
		if err != nil {
			return openSearchResultMsg{err: err}
		}
		if rootID == "" {
			return openSearchResultMsg{err: store.ErrNotFound}
		}
		return openSearchResultMsg{rootID: rootID, roomID: roomID, communityID: communityID, matchID: matchID}
	}
}

func (m Model) cmdLoadFeedMore() tea.Cmd {
	if m.curRoom == nil || !m.feedHasMore {
		return nil
	}
	roomID := m.curRoom.ID
	sortMode := m.feedSort
	topRange := m.feedTopRange
	beforeNew := m.feedNewCur
	beforeTop := m.feedTopCur
	beforeHot := m.feedHotCur
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		switch sortMode {
		case store.SortNew:
			items, next, err := m.st.ListRoomTopLevelNewPage(ctx, roomID, 30, beforeNew)
			return feedLoadedMsg{items: items, append: true, hasMore: next != nil, nextNew: next, err: err}
		case store.SortTop:
			items, next, err := m.st.ListRoomTopLevelTopPage(ctx, roomID, topRange, 30, beforeTop)
			return feedLoadedMsg{items: items, append: true, hasMore: next != nil, nextTop: next, err: err}
		case store.SortHot:
			items, next, err := m.st.ListRoomTopLevelHotPage(ctx, roomID, time.Now().UTC(), 30, beforeHot)
			return feedLoadedMsg{items: items, append: true, hasMore: next != nil, nextHot: next, err: err}
		default:
			items, next, err := m.st.ListRoomTopLevelNewPage(ctx, roomID, 30, beforeNew)
			return feedLoadedMsg{items: items, append: true, hasMore: next != nil, nextNew: next, err: err}
		}
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

type roomCreatedMsg struct {
	communityID string
	room        *store.Room
	err         error
}

func (m Model) cmdCreateRoom(communityID, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := m.st.CountRoomsByCommunity(ctx, communityID)
		if err != nil {
			return roomCreatedMsg{communityID: communityID, err: err}
		}
		if n >= 20 {
			return roomCreatedMsg{communityID: communityID, err: store.ErrLimit}
		}
		r, err := m.st.CreateRoom(ctx, communityID, name)
		return roomCreatedMsg{communityID: communityID, room: r, err: err}
	}
}

type joinedCommunityMsg struct {
	community *store.Community
	err       error
}

type leftCommunityMsg struct {
	err error
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

func (m Model) cmdLeaveCommunity(communityID string) tea.Cmd {
	userID := ""
	if m.user != nil {
		userID = m.user.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := m.st.LeaveCommunity(ctx, userID, communityID)
		return leftCommunityMsg{err: err}
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

func (m *Model) cycleFeedSort() {
	switch m.feedSort {
	case store.SortHot:
		m.feedSort = store.SortNew
	case store.SortNew:
		m.feedSort = store.SortTop
	case store.SortTop:
		m.feedSort = store.SortHot
	default:
		m.feedSort = store.SortHot
	}
	m.feedNewCur = nil
	m.feedTopCur = nil
	m.feedHotCur = nil
	m.feedHasMore = false
	m.feedPending = 0
}

func (m *Model) cycleFeedTopRange() {
	switch m.feedTopRange {
	case store.TopToday:
		m.feedTopRange = store.TopWeek
	case store.TopWeek:
		m.feedTopRange = store.TopMonth
	case store.TopMonth:
		m.feedTopRange = store.TopAll
	case store.TopAll:
		m.feedTopRange = store.TopToday
	default:
		m.feedTopRange = store.TopWeek
	}
	m.feedNewCur = nil
	m.feedTopCur = nil
	m.feedHotCur = nil
	m.feedHasMore = false
	m.feedPending = 0
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

func min(a, b int) int {
	if a < b {
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
