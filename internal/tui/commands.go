package tui

import (
	"strings"
)

type Command struct {
	Name string
	Args []string
}

func ParseCommand(line string) *Command {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '/' {
		return nil
	}
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	name := strings.TrimPrefix(parts[0], "/")
	if name == "" {
		return nil
	}
	return &Command{Name: name, Args: parts[1:]}
}
