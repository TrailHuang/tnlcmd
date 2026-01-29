package tnlcmd

import (
	"strings"
)

// CommandCompleter 命令补全器
type CommandCompleter struct {
	commands    map[string]CommandInfo // 向后兼容的平面命令存储
	commandTree *CommandTree           // 新的树形命令存储
}

// NewCommandCompleter 创建新的命令补全器
func NewCommandCompleter() *CommandCompleter {
	return &CommandCompleter{
		commands: make(map[string]CommandInfo),
	}
}

// NewCommandCompleterWithTree 创建带命令树的补全器
func NewCommandCompleterWithTree(tree *CommandTree) *CommandCompleter {
	return &CommandCompleter{
		commandTree: tree,
		commands:    make(map[string]CommandInfo),
	}
}

// UpdateCommands 更新命令列表
func (c *CommandCompleter) UpdateCommands(commands map[string]CommandInfo) {
	c.commands = commands
}

// UpdateCommandTree 更新命令树
func (c *CommandCompleter) UpdateCommandTree(tree *CommandTree) {
	c.commandTree = tree
}

// Complete 命令补全
func (c *CommandCompleter) Complete(input string) []string {
	var completions []string

	// 如果使用命令树，优先使用树形匹配
	if c.commandTree != nil {
		inputParts := strings.Fields(input)
		node := c.commandTree.Root

		// 遍历到当前层级
		for i := 0; i < len(inputParts)-1; i++ {
			if child, exists := node.Children[inputParts[i]]; exists {
				node = child
			} else {
				// 找不到匹配节点，返回空
				return completions
			}
		}

		// 获取当前节点的补全选项
		if len(inputParts) > 0 {
			currentInput := inputParts[len(inputParts)-1]
			for name, child := range node.Children {
				// 只补全命令节点，跳过参数节点
				if child.Type == NodeTypeCommand && strings.HasPrefix(name, currentInput) {
					completions = append(completions, name)
				}
			}
		} else {
			// 空输入，返回所有一级命令（只显示命令节点）
			for name, child := range node.Children {
				if child.Type == NodeTypeCommand {
					completions = append(completions, name)
				}
			}
		}

		return completions
	}

	// 向后兼容：使用旧的平面命令匹配逻辑
	if input == "" {
		for name := range c.commands {
			if !strings.Contains(name, " ") {
				completions = append(completions, name)
			}
		}
		return completions
	}

	inputParts := strings.Fields(input)
	currentInput := input
	if len(inputParts) > 0 {
		currentInput = inputParts[len(inputParts)-1]
	}

	// 单级命令前缀匹配
	if len(inputParts) == 1 {
		firstLevelCommands := make(map[string]bool)
		for name := range c.commands {
			nameParts := strings.Split(name, " ")
			if len(nameParts) > 0 {
				firstLevelCommands[nameParts[0]] = true
			}
		}

		for cmd := range firstLevelCommands {
			if strings.HasPrefix(cmd, input) {
				completions = append(completions, cmd)
			}
		}

		if len(completions) > 0 {
			return completions
		}
	}

	// 多级命令分段匹配
	if len(inputParts) > 0 {
		for name := range c.commands {
			nameParts := strings.Split(name, " ")

			if len(nameParts) >= len(inputParts) {
				matched := true
				for i := 0; i < len(inputParts)-1; i++ {
					if i >= len(nameParts) || inputParts[i] != nameParts[i] {
						matched = false
						break
					}
				}

				if matched && len(nameParts) >= len(inputParts) {
					lastPart := nameParts[len(inputParts)-1]
					if strings.HasPrefix(lastPart, currentInput) {
						completions = append(completions, name)
					}
				}
			}
		}
	}

	return completions
}

// GetNextLevelCompletions 获取下一级补全选项
func (c *CommandCompleter) GetNextLevelCompletions(input string) []string {
	var nextLevel []string

	// 如果使用命令树，优先使用树形匹配
	if c.commandTree != nil {
		inputParts := strings.Fields(input)
		node := c.commandTree.Root

		// 遍历到当前层级
		for i := 0; i < len(inputParts); i++ {
			if child, exists := node.Children[inputParts[i]]; exists {
				node = child
			} else {
				// 找不到匹配节点，返回空
				return nextLevel
			}
		}

		// 获取当前节点的所有子节点（只显示命令节点）
		for name, child := range node.Children {
			if child.Type == NodeTypeCommand {
				nextLevel = append(nextLevel, name)
			}
		}

		return nextLevel
	}

	// 向后兼容：使用旧的平面命令匹配逻辑
	inputParts := strings.Fields(input)
	if len(inputParts) == 0 {
		return nextLevel
	}

	// 查找所有以当前输入开头的多级命令
	for name := range c.commands {
		nameParts := strings.Split(name, " ")

		// 检查是否匹配当前层级
		if len(nameParts) > len(inputParts) {
			matched := true
			for i := 0; i < len(inputParts); i++ {
				if i >= len(nameParts) || inputParts[i] != nameParts[i] {
					matched = false
					break
				}
			}

			if matched {
				// 添加下一级选项
				nextLevelOption := nameParts[len(inputParts)]
				// 去重
				found := false
				for _, existing := range nextLevel {
					if existing == nextLevelOption {
						found = true
						break
					}
				}
				if !found {
					nextLevel = append(nextLevel, nextLevelOption)
				}
			}
		}
	}

	return nextLevel
}

// GetCommandSuggestions 获取命令建议（用于提示功能）
func (c *CommandCompleter) GetCommandSuggestions(input string) []string {
	var suggestions []string

	inputParts := strings.Fields(input)
	if len(inputParts) == 0 {
		// 返回所有一级命令
		for name := range c.commands {
			if !strings.Contains(name, " ") {
				suggestions = append(suggestions, name)
			}
		}
		return suggestions
	}

	// 查找匹配的多级命令
	for name := range c.commands {
		nameParts := strings.Split(name, " ")

		if len(nameParts) >= len(inputParts) {
			matched := true
			for i := 0; i < len(inputParts); i++ {
				if i >= len(nameParts) || inputParts[i] != nameParts[i] {
					matched = false
					break
				}
			}

			if matched {
				suggestions = append(suggestions, name)
			}
		}
	}

	return suggestions
}
