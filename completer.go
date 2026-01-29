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

	// 分析输入，按空格拆分
	inputParts := strings.Fields(input)
	currentInput := input
	if len(inputParts) > 0 {
		currentInput = inputParts[len(inputParts)-1]
	}

	// 查找匹配的命令
	for name := range c.commands {
		// 支持前缀匹配
		if strings.HasPrefix(name, input) {
			completions = append(completions, name)
		} else {
			// 支持多级命令补全
			nameParts := strings.Split(name, " ")

			// 如果输入包含空格，进行多级匹配
			if len(inputParts) > 0 {
				// 检查前几级是否匹配
				if len(nameParts) >= len(inputParts) {
					matched := true
					for i := 0; i < len(inputParts)-1; i++ {
						if i >= len(nameParts) || inputParts[i] != nameParts[i] {
							matched = false
							break
						}
					}

					// 如果前几级匹配，检查最后一级
					if matched && len(nameParts) >= len(inputParts) {
						lastPart := nameParts[len(inputParts)-1]
						if strings.HasPrefix(lastPart, currentInput) {
							// 返回完整的命令名
							completions = append(completions, name)
						}
					}
				}
			} else {
				// 单级匹配：如果输入是命令的一部分，也提供补全
				// 例如：输入 "sh" 可以匹配 "show test1", "show test2"
				for i := 1; i <= len(nameParts); i++ {
					partialCmd := strings.Join(nameParts[:i], " ")
					if strings.HasPrefix(partialCmd, input) {
						completions = append(completions, name)
						break
					}
				}
			}
		}
	}

	return completions
}
