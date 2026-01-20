package session

import (
	"sort"
	"strings"
)

type CommandCompleter struct {
	commands map[string]string
}

func NewCommandCompleter() *CommandCompleter {
	return &CommandCompleter{
		commands: map[string]string{
			"help":    "Show help message",
			"history": "Show command history",
			"clear":   "Clear the screen",
			"echo":    "Echo arguments",
			"time":    "Show current time",
			"exit":    "Exit the session",
			"quit":    "Exit the session",
		},
	}
}

func (c *CommandCompleter) Complete(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return c.getAllCommands()
	}

	var matches []string
	for cmd := range c.commands {
		if strings.HasPrefix(cmd, input) {
			matches = append(matches, cmd)
		}
	}

	sort.Strings(matches)
	return matches
}

func (c *CommandCompleter) getAllCommands() []string {
	var commands []string
	for cmd := range c.commands {
		commands = append(commands, cmd)
	}
	sort.Strings(commands)
	return commands
}

func (c *CommandCompleter) AddCommand(cmd, description string) {
	c.commands[cmd] = description
}

func (c *CommandCompleter) RemoveCommand(cmd string) {
	delete(c.commands, cmd)
}