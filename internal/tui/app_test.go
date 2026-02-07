package tui

import (
	"context"
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

	created []*store.Community
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
func (f *fakeStore) ListExploreCommunities(ctx context.Context, userID string, limit int) ([]store.Community, error) {
	return f.explore, nil
}
func (f *fakeStore) ListJoinedCommunities(ctx context.Context, userID string) ([]store.Community, error) {
	return f.joined, nil
}
func (f *fakeStore) ListHomeFeed(ctx context.Context, userID string, sortMode store.SortMode, topRange store.TopRange, limit, offset int) ([]store.FeedMessage, error) {
	return nil, nil
}
func (f *fakeStore) ListRoomsByCommunity(ctx context.Context, communityID string) ([]store.Room, error) {
	return f.rooms[communityID], nil
}
func (f *fakeStore) GetRoomByID(ctx context.Context, id string) (*store.Room, error) { return nil, nil }
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
func (f *fakeStore) ListThread(ctx context.Context, rootMessageID string) ([]store.FeedMessage, error) {
	return nil, nil
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
		for _, m := range msg {
			updated, next := model.Update(m)
			model = updated.(tea.Model)
			model = applyCmd(t, model, next)
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
