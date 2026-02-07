package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type SidebarItem struct {
	ID     string
	Name   string
	Unread int
}

type SidebarModel struct {
	Items    []SidebarItem
	Selected int
	Focused  bool
}

const SidebarWidth = 20

func (m SidebarModel) View(height int) string {
	header := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("COMMUNITIES")

	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")

	for i, it := range m.Items {
		lines = append(lines, m.renderItem(i, it))
	}

	box := lipgloss.NewStyle().Width(SidebarWidth).Height(height)
	if m.Focused {
		box = box.Foreground(lipgloss.Color("6"))
	}
	return box.Render(padLines(lines, height))
}

func (m SidebarModel) renderItem(i int, it SidebarItem) string {
	dot := " "
	if it.Unread > 0 {
		dot = "●"
	}

	unread := ""
	if it.Unread > 0 {
		unread = fmt.Sprintf(" (%d)", it.Unread)
	}

	nameMax := SidebarWidth - 2 - len(dot) - len(unread) // "x name"
	name := truncate(it.Name, nameMax)

	line := fmt.Sprintf("%s %s%s", dot, name, unread)
	line = lipgloss.NewStyle().Width(SidebarWidth).Render(line)

	if i == m.Selected {
		if m.Focused {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Render(line)
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(line)
	}
	return line
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func padLines(lines []string, height int) string {
	if height <= 0 {
		return strings.Join(lines, "\n")
	}
	if len(lines) >= height {
		return strings.Join(lines[:height], "\n")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
