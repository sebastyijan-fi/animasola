package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ChatInput struct {
	textarea textarea.Model
	width    int
	height   int
}

func NewChatInput() ChatInput {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (alt+enter for new line, enter to send)"
	ta.Focus()

	ta.Prompt = "┃ "
	ta.CharLimit = 2000

	ta.SetWidth(0)
	ta.SetHeight(3)

	// Remove cursor line styling
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	return ChatInput{
		textarea: ta,
		height:   3,
	}
}

func (c *ChatInput) Init() tea.Cmd {
	return textarea.Blink
}

func (c *ChatInput) SetSize(width, height int) {
	c.width = width
	c.height = height
	c.textarea.SetWidth(width)
	c.textarea.SetHeight(height)
}

func (c *ChatInput) Update(msg tea.Msg) (ChatInput, tea.Cmd, bool) {
	var cmd tea.Cmd
	var submitted bool

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if msg.Alt {
				// Alt+Enter -> new line
				// We pass a standard Enter back into the textarea to force a newline
				newMsg := tea.KeyMsg{Type: tea.KeyEnter}
				var updateCmd tea.Cmd
				c.textarea, updateCmd = c.textarea.Update(newMsg)
				return *c, updateCmd, false
			} else {
				// Enter -> submit
				submitted = true
				return *c, nil, submitted
			}
		}
	}

	c.textarea, cmd = c.textarea.Update(msg)
	return *c, cmd, submitted
}

func (c *ChatInput) Value() string {
	return c.textarea.Value()
}

func (c *ChatInput) SetValue(v string) {
	c.textarea.SetValue(v)
}

func (c *ChatInput) Focus() {
	c.textarea.Focus()
}

func (c *ChatInput) View() string {
	return c.textarea.View()
}
