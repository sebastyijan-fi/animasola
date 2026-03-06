package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

type Feed struct {
	viewport viewport.Model
	messages []sqlite.FeedMessage
	width    int
	height   int
}

func NewFeed(width, height int) Feed {
	vp := viewport.New(width, height)
	vp.YPosition = 0

	return Feed{
		viewport: vp,
		width:    width,
		height:   height,
	}
}

func (f *Feed) Init() tea.Cmd {
	return nil
}

func (f *Feed) SetSize(width, height int) {
	f.width = width
	f.height = height
	f.viewport.Width = width
	f.viewport.Height = height
	f.renderViewport()
}

func (f *Feed) SetMessages(messages []sqlite.FeedMessage) {
	// If the viewport is effectively empty, or if we were at the very bottom
	// of the view, we should auto-scroll down after rendering.
	atBottom := f.viewport.AtBottom()
	wasEmpty := len(f.messages) == 0

	f.messages = messages

	// Record position BEFORE rendering
	savedY := f.viewport.YPosition

	f.renderViewport()

	if wasEmpty || atBottom {
		f.viewport.GotoBottom()
	} else {
		// If we weren't at the bottom, strictly preserve the previous Y offset.
		f.viewport.YPosition = savedY
	}
}

func (f *Feed) Update(msg tea.Msg) (Feed, tea.Cmd) {
	var cmd tea.Cmd
	f.viewport, cmd = f.viewport.Update(msg)
	return *f, cmd
}

func (f *Feed) renderViewport() {
	var s strings.Builder

	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)

	if len(f.messages) == 0 {
		s.WriteString(metaStyle.Render("No messages yet. Be the first!"))
		s.WriteString("\n")
	} else {
		for _, msg := range f.messages {
			timeStr := msg.CreatedAt.Local().Format(time.Kitchen)
			// We want to align wrapped text so it falls under the message content, not the timestamp
			prefix := fmt.Sprintf("%s %s ", metaStyle.Render(fmt.Sprintf("[%s]", timeStr)), userStyle.Render(fmt.Sprintf("<%s>", msg.AuthorUsername)))
			prefixWidth := lipgloss.Width(prefix)

			// Ensure it doesn't exceed feed width
			contentMaxW := f.width - prefixWidth - 2 // small margin
			if contentMaxW < 10 {
				contentMaxW = 10
			}

			// Render the content wrapped and indented
			styledContent := msgStyle.
				Width(contentMaxW).
				Render(msg.Content)

			// The first line gets the prefix. If the content wraps, we need to pad the subsequent lines
			lines := strings.Split(styledContent, "\n")
			for i, line := range lines {
				if i == 0 {
					s.WriteString(prefix + line + "\n")
				} else {
					s.WriteString(strings.Repeat(" ", prefixWidth) + line + "\n")
				}
			}
		}
	}

	content := strings.TrimRight(s.String(), "\n")
	f.viewport.SetContent(content)
}

func (f *Feed) View() string {
	return f.viewport.View()
}
