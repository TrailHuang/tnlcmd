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
		// 支持前缀匹配
		if strings.HasPrefix(name, input) {
			completions = append(completions, name)
		} else {
			// 支持部分匹配：如果输入是命令的一部分，也提供补全
			// 例如：输入 "sh" 可以匹配 "show test1", "show test2"
			parts := strings.Split(name, " ")
			for i := 1; i <= len(parts); i++ {
				partialCmd := strings.Join(parts[:i], " ")
				if strings.HasPrefix(partialCmd, input) {
					completions = append(completions, name)
					break
				}
			}
		}
	}

	return completions
}
