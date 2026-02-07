package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"animasola/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeStore struct {
	joined  []store.Community
	explore []store.Community
	rooms   map[string][]store.Room
	feed    map[string][]store.FeedMessage
	home    []store.FeedMessage

	created []*store.Community

	search    []store.SearchResult
	thread    map[string][]store.FeedMessage
	roomsByID map[string]store.Room
}

func (f *fakeStore) CreateCommunity(ctx context.Context, createdByUserID, name, description string) (*store.Community, *store.Room, error) {
	c := &store.Community{ID: "c1", Name: name, Description: description, MemberCount: 1}
	r := &store.Room{ID: "r1", Name: "general", CommunityID: c.ID}
	f.created = append(f.created, c)
	return c, r, nil
}
func (f *fakeStore) GetCommunityByName(ctx context.Context, name string) (*store.Community, error) {
	for _, c := range f.explore {
		if c.Name == name {
			cc := c
			return &cc, nil
		}
	}
	return nil, nil
}
func (f *fakeStore) GetCommunityByID(ctx context.Context, id string) (*store.Community, error) {
	for _, c := range append(f.joined, f.explore...) {
		if c.ID == id {
			cc := c
			return &cc, nil
		}
	}
	return nil, nil
}
func (f *fakeStore) JoinCommunity(ctx context.Context, userID, communityID string) error { return nil }
func (f *fakeStore) LeaveCommunity(ctx context.Context, userID, communityID string) error {
	out := f.joined[:0]
	for _, c := range f.joined {
		if c.ID != communityID {
			out = append(out, c)
		}
	}
	f.joined = out
	return nil
}
func (f *fakeStore) ListExploreCommunities(ctx context.Context, userID string, limit int) ([]store.Community, error) {
	return f.explore, nil
}
func (f *fakeStore) ListJoinedCommunities(ctx context.Context, userID string) ([]store.Community, error) {
	return f.joined, nil
}
func (f *fakeStore) ListHomeFeed(ctx context.Context, userID string, sortMode store.SortMode, topRange store.TopRange, limit, offset int) ([]store.FeedMessage, error) {
	return nil, nil
}
func (f *fakeStore) ListHomeNewPage(ctx context.Context, userID string, limit int, beforeID *string) ([]store.FeedMessage, *string, error) {
	items := f.home
	start := 0
	if beforeID != nil {
		for i := range items {
			if items[i].ID == *beforeID {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	out := append([]store.FeedMessage(nil), items[start:end]...)
	var next *string
	if len(out) == limit {
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, nil
}
func (f *fakeStore) ListHomeTopPage(ctx context.Context, userID string, topRange store.TopRange, limit int, before *store.HomeTopCursor) ([]store.FeedMessage, *store.HomeTopCursor, error) {
	return nil, nil, nil
}
func (f *fakeStore) ListHomeHotPage(ctx context.Context, userID string, now time.Time, limit int, before *store.HomeHotCursor) ([]store.FeedMessage, *store.HomeHotCursor, error) {
	return nil, nil, nil
}
func (f *fakeStore) ListRoomsByCommunity(ctx context.Context, communityID string) ([]store.Room, error) {
	return f.rooms[communityID], nil
}
func (f *fakeStore) CountRoomsByCommunity(ctx context.Context, communityID string) (int, error) {
	return len(f.rooms[communityID]), nil
}
func (f *fakeStore) CreateRoom(ctx context.Context, communityID, name string) (*store.Room, error) {
	if f.rooms == nil {
		f.rooms = make(map[string][]store.Room)
	}
	r := store.Room{ID: "r_new", Name: name, CommunityID: communityID}
	f.rooms[communityID] = append(f.rooms[communityID], r)
	rr := r
	return &rr, nil
}
func (f *fakeStore) GetRoomByID(ctx context.Context, id string) (*store.Room, error) {
	if f.roomsByID != nil {
		if r, ok := f.roomsByID[id]; ok {
			rr := r
			return &rr, nil
		}
	}
	return nil, nil
}
func (f *fakeStore) UpsertReadPosition(ctx context.Context, userID, roomID, lastReadMessageID string) error {
	return nil
}
func (f *fakeStore) UnreadCountsByCommunity(ctx context.Context, userID string) ([]store.CommunityUnread, error) {
	return nil, nil
}
func (f *fakeStore) ListRoomTopLevelNew(ctx context.Context, roomID string, limit int, beforeID *string) ([]store.FeedMessage, error) {
	return f.feed[roomID], nil
}
func (f *fakeStore) ListRoomTopLevelNewPage(ctx context.Context, roomID string, limit int, beforeID *string) ([]store.FeedMessage, *string, error) {
	items := f.feed[roomID]
	start := 0
	if beforeID != nil {
		for i := range items {
			if items[i].ID == *beforeID {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	out := append([]store.FeedMessage(nil), items[start:end]...)
	var next *string
	if len(out) == limit {
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, nil
}
func (f *fakeStore) ListRoomTopLevelTopPage(ctx context.Context, roomID string, topRange store.TopRange, limit int, before *store.RoomTopCursor) ([]store.FeedMessage, *store.RoomTopCursor, error) {
	return nil, nil, nil
}
func (f *fakeStore) ListRoomTopLevelHotPage(ctx context.Context, roomID string, now time.Time, limit int, before *store.RoomHotCursor) ([]store.FeedMessage, *store.RoomHotCursor, error) {
	return nil, nil, nil
}
func (f *fakeStore) SearchMessages(ctx context.Context, userID string, query string, limit int) ([]store.SearchResult, error) {
	return f.search, nil
}
func (f *fakeStore) ResolveThreadRootID(ctx context.Context, messageID string) (string, error) {
	return messageID, nil
}
func (f *fakeStore) ListThread(ctx context.Context, rootMessageID string) ([]store.FeedMessage, error) {
	if f.thread == nil {
		return nil, nil
	}
	return f.thread[rootMessageID], nil
}
func (f *fakeStore) CreateMessage(ctx context.Context, roomID, authorID, content string, parentID *string) (*store.Message, error) {
	return &store.Message{ID: "m1"}, nil
}
func (f *fakeStore) ToggleUpvote(ctx context.Context, userID, messageID string) (*store.UpvoteState, error) {
	return &store.UpvoteState{Upvoted: true, Count: 1}, nil
}
func (f *fakeStore) SoftDeleteMessage(ctx context.Context, messageID, requesterUserID string) error {
	return nil
}

func TestCreateCommunityFlow(t *testing.T) {
	fs := &fakeStore{}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)

	m.input.SetValue("/create-community")
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.createStep != createName {
		t.Fatalf("expected createName step, got %v", m.createStep)
	}

	// Provide name.
	m.input.SetValue("rust")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.createStep != createDesc {
		t.Fatalf("expected createDesc step, got %v", m.createStep)
	}

	// Provide desc and expect async command.
	m.input.SetValue("All things Rust")
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected create cmd")
	}

	// Run cmd and apply any messages (tea.Batch produces tea.BatchMsg).
	model = applyCmd(t, model, cmd)
	m = model.(Model)
	if m.curCommunity == nil || m.curCommunity.Name != "rust" {
		t.Fatalf("expected current community rust, got %#v", m.curCommunity)
	}
}

func TestTypingDoesNotTriggerShortcuts(t *testing.T) {
	fs := &fakeStore{}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)

	// Enable sidebar visibility logic.
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)

	// Default focus is input.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = model.(Model)
	if m.v != viewHome {
		t.Fatalf("expected to stay on home view, got %v", m.v)
	}
	if m.input.Value() == "" {
		t.Fatalf("expected input to capture typed rune")
	}

	// In nav mode, single-key shortcuts should work.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = model.(Model)
	if m.v != viewCommunities {
		t.Fatalf("expected to navigate to communities view, got %v", m.v)
	}
}

func TestSidebarTabTogglesFocusAndHidesBelow60(t *testing.T) {
	fs := &fakeStore{}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	if m.sidebarVisible() != true {
		t.Fatalf("expected sidebar visible")
	}
	if m.focus != focusMain {
		t.Fatalf("expected focusMain by default")
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(Model)
	if m.focus != focusSidebar {
		t.Fatalf("expected focusSidebar after tab")
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(Model)
	if m.focus != focusMain {
		t.Fatalf("expected focusMain after tab back")
	}

	model, _ = m.Update(tea.WindowSizeMsg{Width: 59, Height: 24})
	m = model.(Model)
	if m.sidebarVisible() != false {
		t.Fatalf("expected sidebar hidden below 60 cols")
	}

	prev := m.focus
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(Model)
	if m.focus != prev {
		t.Fatalf("expected tab no-op when sidebar hidden")
	}
}

func applyCmd(t *testing.T, model tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg := cmd()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		// In Bubble Tea, BatchMsg contains a list of Cmds to be executed.
		for _, c := range msg {
			model = applyCmd(t, model, c)
		}
		return model
	case nil:
		return model
	default:
		updated, next := model.Update(msg)
		model = updated.(tea.Model)
		return applyCmd(t, model, next)
	}
}

func TestRoomFeedPaginationLoadsMoreAtBottom(t *testing.T) {
	fs := &fakeStore{
		feed: map[string][]store.FeedMessage{
			"r1": {
				{Message: store.Message{ID: "m3"}, AuthorUsername: "a"},
				{Message: store.Message{ID: "m2"}, AuthorUsername: "a"},
				{Message: store.Message{ID: "m1"}, AuthorUsername: "a"},
			},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.v = viewRoom
	m.curRoom = &store.Room{ID: "r1", Name: "general"}
	m.feedSort = store.SortNew
	m.focus = focusMain
	m.mainFocus = mainNav

	// Initial load.
	m.feedLoading = true
	model := applyCmd(t, m, m.cmdLoadFeedReset())
	m = model.(Model)
	if len(m.feed) != 3 {
		t.Fatalf("expected initial feed loaded, got %d", len(m.feed))
	}

	// Force a small page size by pretending we have more and a cursor.
	// Our fake store uses the whole slice but honors `beforeID` by position.
	// Make first "page" end at m2 so "more" appends m1.
	m.feed = m.feed[:2]
	m.feedSel = len(m.feed) - 1
	cur := m.feed[len(m.feed)-1].ID
	m.feedNewCur = &cur
	m.feedHasMore = true
	m.feedLoading = false

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected load more cmd at bottom")
	}
	model = applyCmd(t, m, cmd)
	m = model.(Model)
	if len(m.feed) != 3 {
		t.Fatalf("expected feed appended, got %d", len(m.feed))
	}
	if m.feed[2].ID != "m1" {
		t.Fatalf("expected last item m1, got %q", m.feed[2].ID)
	}
}

func TestLeaveCommandPromptsAndLeavesOnYes(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)
	m.v = viewHome
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	m.input.SetValue("/leave rust")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd on prompt")
	}
	if !m.leaveConfirm {
		t.Fatalf("expected leaveConfirm")
	}
	if m.leaveCommunityID != "c1" {
		t.Fatalf("expected leaveCommunityID=c1, got %q", m.leaveCommunityID)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected leave cmd")
	}
	model = applyCmd(t, m, cmd)
	m = model.(Model)
	if len(fs.joined) != 0 {
		t.Fatalf("expected store joined empty after leave, got %d", len(fs.joined))
	}
	if len(m.joined) != 0 {
		t.Fatalf("expected joined empty after leave, got %d", len(m.joined))
	}
	if m.v != viewHome {
		t.Fatalf("expected viewHome after leave")
	}
}

func TestQuitCommandReturnsQuitMsg(t *testing.T) {
	fs := &fakeStore{}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	cmd := m.execCommand(ParseCommand("/q"))
	if cmd == nil {
		t.Fatalf("expected cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected QuitMsg")
	}
}

func TestCreateRoomCommandCreatesRoomAndOpensIt(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		rooms: map[string][]store.Room{
			"c1": {{ID: "r1", Name: "general", CommunityID: "c1"}},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)

	m.curCommunity = &store.Community{ID: "c1", Name: "rust"}
	m.v = viewCommunity
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	m.input.SetValue("/create-room help")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected create room cmd")
	}
	model = applyCmd(t, m, cmd)
	m = model.(Model)
	if m.v != viewRoom {
		t.Fatalf("expected viewRoom, got %v", m.v)
	}
	if m.curRoom == nil || m.curRoom.Name != "help" {
		t.Fatalf("expected curRoom=help, got %#v", m.curRoom)
	}
	found := false
	for _, r := range fs.rooms["c1"] {
		if r.Name == "help" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected room to be created in store")
	}
}

func TestCreateRoomCommandRejectsInvalidName(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		rooms:  map[string][]store.Room{"c1": {{ID: "r1", Name: "general", CommunityID: "c1"}}},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.curCommunity = &store.Community{ID: "c1", Name: "rust"}
	m.v = viewCommunity
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	m.input.SetValue("/create-room BadName")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd")
	}
	if !strings.Contains(m.errMsg, "Invalid room name") {
		t.Fatalf("expected error about invalid room name, got %q", m.errMsg)
	}
	if len(fs.rooms["c1"]) != 1 {
		t.Fatalf("expected no room created")
	}
}

func TestRoomsCommandNavigatesToCommunityView(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		rooms: map[string][]store.Room{
			"c1": {{ID: "r1", Name: "general", CommunityID: "c1"}},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.curCommunity = &store.Community{ID: "c1", Name: "rust"}
	m.curRoom = &store.Room{ID: "r1", Name: "general", CommunityID: "c1"}
	m.v = viewRoom
	m.focus = focusMain
	m.mainFocus = mainInput
	m.input.Focus()
	m.input.SetValue("/rooms")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected load rooms cmd")
	}
	model := applyCmd(t, m, cmd)
	m = model.(Model)
	if m.v != viewCommunity {
		t.Fatalf("expected viewCommunity")
	}
}

func TestHomeFeedPaginationLoadsMoreAtBottom(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		home: []store.FeedMessage{
			{Message: store.Message{ID: "m3"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
			{Message: store.Message{ID: "m2"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
			{Message: store.Message{ID: "m1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.v = viewHome
	m.homeSort = store.SortNew
	m.focus = focusMain
	m.mainFocus = mainNav

	m.homeLoading = true
	model := applyCmd(t, m, m.cmdLoadHomeReset())
	m = model.(Model)
	if len(m.homeItems) != 3 {
		t.Fatalf("expected initial home loaded, got %d", len(m.homeItems))
	}

	// Force a small page size by pretending we have more and a cursor.
	m.homeItems = m.homeItems[:2]
	m.homeSel = len(m.homeItems) - 1
	cur := m.homeItems[len(m.homeItems)-1].ID
	m.homeNewCur = &cur
	m.homeHasMore = true
	m.homeLoading = false

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected load more cmd at bottom")
	}
	model = applyCmd(t, m, cmd)
	m = model.(Model)
	if len(m.homeItems) != 3 {
		t.Fatalf("expected home appended, got %d", len(m.homeItems))
	}
	if m.homeItems[2].ID != "m1" {
		t.Fatalf("expected last item m1, got %q", m.homeItems[2].ID)
	}
}

func TestCtrlRRefreshesRoomFeed(t *testing.T) {
	fs := &fakeStore{
		feed: map[string][]store.FeedMessage{
			"r1": {
				{Message: store.Message{ID: "m1"}, AuthorUsername: "a"},
			},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.v = viewRoom
	m.curRoom = &store.Room{ID: "r1", Name: "general"}
	m.feedSort = store.SortNew
	m.focus = focusMain
	m.mainFocus = mainNav

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected refresh cmd")
	}
	model := applyCmd(t, m, cmd)
	m = model.(Model)
	if len(m.feed) != 1 || m.feed[0].ID != "m1" {
		t.Fatalf("expected feed reloaded, got %#v", m.feed)
	}
}

func TestSlashOpensSearchAndEscCloses(t *testing.T) {
	fs := &fakeStore{}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.focus = focusMain
	m.mainFocus = mainNav

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = model.(Model)
	if !m.searchOpen || m.mainFocus != mainSearch {
		t.Fatalf("expected search open and focused, got open=%v focus=%v", m.searchOpen, m.mainFocus)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.searchOpen || m.mainFocus != mainNav {
		t.Fatalf("expected search closed and nav focus, got open=%v focus=%v", m.searchOpen, m.mainFocus)
	}
}

func TestSearchEnterOpensThreadAndEscRestoresSearch(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		roomsByID: map[string]store.Room{
			"r1": {ID: "r1", Name: "general", CommunityID: "c1"},
		},
		search: []store.SearchResult{
			{
				FeedMessage: store.FeedMessage{
					Message:        store.Message{ID: "m2", RoomID: "r1"},
					AuthorUsername: "a",
					CommunityID:    "c1",
					CommunityName:  "rust",
					RoomName:       "general",
				},
				HighlightedContent: "<hl>tokio</hl> is neat",
			},
		},
		thread: map[string][]store.FeedMessage{
			"m2": {
				{Message: store.Message{ID: "m2", RoomID: "r1"}, AuthorUsername: "a"},
			},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.focus = focusMain
	m.mainFocus = mainNav

	// Open search and load results.
	m.openSearch("tokio")
	model := applyCmd(t, m, m.cmdSearch("tokio"))
	m = model.(Model)
	if len(m.searchResults) != 1 {
		t.Fatalf("expected 1 search result")
	}

	// Enter should open thread.
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected open-selected cmd")
	}
	model = applyCmd(t, model, cmd)
	m = model.(Model)
	if m.v != viewThread {
		t.Fatalf("expected thread view, got %v", m.v)
	}

	// Esc should restore search.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if !m.searchOpen || m.mainFocus != mainSearch {
		t.Fatalf("expected search restored after esc, got open=%v focus=%v", m.searchOpen, m.mainFocus)
	}
}

func TestSearchShowsTruncationHintWhenMoreThan50Results(t *testing.T) {
	var results []store.SearchResult
	for i := 0; i < 51; i++ {
		results = append(results, store.SearchResult{
			FeedMessage: store.FeedMessage{
				Message:        store.Message{ID: "m" + string(rune('a'+(i%26))), RoomID: "r1"},
				AuthorUsername: "a",
				CommunityID:    "c1",
				CommunityName:  "rust",
				RoomName:       "general",
			},
			HighlightedContent: "<hl>tokio</hl>",
		})
	}
	fs := &fakeStore{
		search: results,
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	m.openSearch("tokio")
	model := applyCmd(t, m, m.cmdSearch("tokio"))
	m = model.(Model)
	if len(m.searchResults) != 50 {
		t.Fatalf("expected 50 results, got %d", len(m.searchResults))
	}
	if !m.searchTrunc {
		t.Fatalf("expected searchTrunc true")
	}
	if !strings.Contains(m.viewSearch(), "Showing first 50 results") {
		t.Fatalf("expected truncation hint in view")
	}
}

func TestMouseClickSidebarOpensCommunityDefaultRoom(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		rooms: map[string][]store.Room{
			"c1": {{ID: "r1", Name: "general", CommunityID: "c1"}},
		},
		feed: map[string][]store.FeedMessage{
			"r1": {{Message: store.Message{ID: "m1", RoomID: "r1"}, AuthorUsername: "a"}},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	// Make sidebar visible.
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	// Load joined list into the model.
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)
	// Ensure nav focus so clicks operate on lists.
	m.focus = focusMain
	m.mainFocus = mainNav

	model, cmd := m.Update(tea.MouseMsg{X: 0, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatalf("expected open community cmd")
	}
	model = applyCmd(t, model, cmd)
	m = model.(Model)
	if m.v != viewRoom || m.curRoom == nil || m.curRoom.ID != "r1" {
		t.Fatalf("expected to be in room r1, got view=%v room=%#v", m.v, m.curRoom)
	}
}

func TestMouseClickRoomMessageOpensThread(t *testing.T) {
	fs := &fakeStore{
		feed: map[string][]store.FeedMessage{
			"r1": {
				{Message: store.Message{ID: "m1", RoomID: "r1"}, AuthorUsername: "a"},
			},
		},
		thread: map[string][]store.FeedMessage{
			"m1": {{Message: store.Message{ID: "m1", RoomID: "r1"}, AuthorUsername: "a"}},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	m.v = viewRoom
	m.curRoom = &store.Room{ID: "r1", Name: "general"}
	m.feedSort = store.SortNew
	m.focus = focusMain
	m.mainFocus = mainNav

	// Load feed.
	m.feedLoading = true
	model = applyCmd(t, m, m.cmdLoadFeedReset())
	m = model.(Model)

	// Click on the first item's meta line (main content starts at y=2; room view adds 2 lines before items).
	// Sidebar is visible, so main starts after 21 columns.
	clickY := 2 + 2 + 0
	clickX := 21 + 5
	model, cmd := m.Update(tea.MouseMsg{X: clickX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatalf("expected open thread cmd")
	}
	model = applyCmd(t, model, cmd)
	m = model.(Model)
	if m.v != viewThread {
		t.Fatalf("expected thread view, got %v", m.v)
	}
}

func TestMouseClickHomeHeaderCyclesSort(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		home: []store.FeedMessage{
			{Message: store.Message{ID: "m1", RoomID: "r1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)
	m.v = viewHome
	m.homeSort = store.SortHot
	m.focus = focusMain
	m.mainFocus = mainNav

	// Click on home header (mainY=0).
	clickY := 2 + 0
	clickX := 21 + 5
	model, cmd := m.Update(tea.MouseMsg{X: clickX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = model.(Model)
	if m.homeSort == store.SortHot {
		t.Fatalf("expected sort to cycle")
	}
	if cmd == nil {
		t.Fatalf("expected reload cmd")
	}
}

func TestMouseWheelOnSidebarMovesSelectionWithoutFocus(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{
			{ID: "c1", Name: "rust"},
			{ID: "c2", Name: "linux"},
			{ID: "c3", Name: "privacy"},
			{ID: "c4", Name: "go"},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)

	// Ensure we're not already focused on sidebar.
	m.focus = focusMain
	m.mainFocus = mainNav
	m.sidebar.Selected = 0

	model, _ = m.Update(tea.MouseMsg{X: 0, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m = model.(Model)
	if m.sidebar.Selected == 0 {
		t.Fatalf("expected sidebar selection to move on wheel")
	}
	if m.focus != focusSidebar {
		t.Fatalf("expected focusSidebar after wheel")
	}
}

func TestMouseWheelOverMainScrollsMainEvenIfSidebarFocused(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		home: []store.FeedMessage{
			{Message: store.Message{ID: "m3", RoomID: "r1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
			{Message: store.Message{ID: "m2", RoomID: "r1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
			{Message: store.Message{ID: "m1", RoomID: "r1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)
	m.v = viewHome
	m.homeSort = store.SortNew
	m.focus = focusSidebar
	m.mainFocus = mainNav
	m.homeLoading = true
	model = applyCmd(t, m, m.cmdLoadHomeReset())
	m = model.(Model)
	m.homeSel = 0

	// Wheel down over main (x beyond sidebar+sep).
	model, _ = m.Update(tea.MouseMsg{X: 25, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m = model.(Model)
	if m.homeSel == 0 {
		t.Fatalf("expected home selection to move on wheel over main")
	}
}

func TestMouseClickHomeHeaderCyclesTopRangeWhenTop(t *testing.T) {
	fs := &fakeStore{
		joined: []store.Community{{ID: "c1", Name: "rust"}},
		home: []store.FeedMessage{
			{Message: store.Message{ID: "m1", RoomID: "r1"}, CommunityName: "rust", RoomName: "general", AuthorUsername: "a"},
		},
	}
	u := &store.User{ID: "u1", Username: "seba"}
	m := NewApp("animasola", fs, nil, u)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	model = applyCmd(t, m, m.cmdLoadJoined())
	m = model.(Model)
	m.v = viewHome
	m.homeSort = store.SortTop
	m.homeTopRange = store.TopWeek
	m.focus = focusMain
	m.mainFocus = mainNav

	clickY := 2 + 0
	clickX := 21 + 5
	model, cmd := m.Update(tea.MouseMsg{X: clickX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = model.(Model)
	if m.homeTopRange == store.TopWeek {
		t.Fatalf("expected top range to cycle")
	}
	if cmd == nil {
		t.Fatalf("expected reload cmd")
	}
}
