package tnlcmd

import (
	"strings"
)

// CommandCompleter 命令补全器
type CommandCompleter struct {
	commands map[string]CommandInfo
}

// NewCommandCompleter 创建新的命令补全器
func NewCommandCompleter() *CommandCompleter {
	return &CommandCompleter{
		commands: make(map[string]CommandInfo),
	}
}

// UpdateCommands 更新命令列表
func (c *CommandCompleter) UpdateCommands(commands map[string]CommandInfo) {
	c.commands = commands
}

// Complete 命令补全
func (c *CommandCompleter) Complete(input string) []string {
	var completions []string

	// 如果输入为空，返回所有命令
	if input == "" {
		for name := range c.commands {
			completions = append(completions, name)
		}
		return completions
	}

	// 查找匹配的命令
	for name := range c.commands {
		if strings.HasPrefix(name, input) {
			completions = append(completions, name)
		}
	}

	return completions
}