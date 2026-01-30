package tnlcmd

import (
	"strings"
)

// CommandCompleter 命令补全器
type CommandCompleter struct {
	commandTree *CommandTree    // 树形命令存储（向后兼容）
	context     *CommandContext // 命令上下文，用于访问当前视图的独立命令树
}

// NewCommandCompleter 创建新的命令补全器
func NewCommandCompleter() *CommandCompleter {
	return &CommandCompleter{}
}

// NewCommandCompleterWithTree 创建带命令树的补全器
func NewCommandCompleterWithTree(tree *CommandTree) *CommandCompleter {
	return &CommandCompleter{
		commandTree: tree,
	}
}

// NewCommandCompleterWithContext 创建带上下文的补全器
func NewCommandCompleterWithContext(context *CommandContext) *CommandCompleter {
	return &CommandCompleter{
		context: context,
	}
}

// UpdateCommandTree 更新命令树
func (c *CommandCompleter) UpdateCommandTree(tree *CommandTree) {
	c.commandTree = tree
}

// Complete 命令补全
func (c *CommandCompleter) Complete(input string) []string {
	var completions []string

	// 优先使用当前视图的独立命令树
	if c.context != nil && c.context.CurrentMode != nil && c.context.CurrentMode.commandTree != nil {
		inputParts := strings.Fields(input)
		node := c.context.CurrentMode.commandTree.Root

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
			var matchingChildren []string

			// 收集所有匹配的子节点（包括视图切换命令）
			for name, child := range node.Children {
				// 补全命令节点和视图切换命令节点
				if (child.Type == NodeTypeCommand || child.Type == NodeTypeModeSwitch) && strings.HasPrefix(name, currentInput) {
					matchingChildren = append(matchingChildren, name)
				}
			}

			// 智能补全逻辑
			if len(matchingChildren) == 1 {
				completions = matchingChildren
			} else if len(matchingChildren) > 1 {
				// 检查是否存在多个不同的前缀模式
				allSamePrefix := true
				firstChild := matchingChildren[0]

				for i := 1; i < len(matchingChildren); i++ {
					if !strings.HasPrefix(matchingChildren[i], firstChild) {
						allSamePrefix = false
						break
					}
				}

				if allSamePrefix {
					completions = []string{firstChild}
				} else {
					completions = matchingChildren
				}
			}
		} else {
			// 空输入，返回所有一级命令（包括视图切换命令）
			for name, child := range node.Children {
				if child.Type == NodeTypeCommand || child.Type == NodeTypeModeSwitch {
					completions = append(completions, name)
				}
			}
		}

		return completions
	}

	// 向后兼容：如果使用全局命令树
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
			var matchingChildren []string

			// 首先收集所有匹配的子节点
			for name, child := range node.Children {
				// 只补全命令节点，跳过参数节点
				if child.Type == NodeTypeCommand && strings.HasPrefix(name, currentInput) {
					matchingChildren = append(matchingChildren, name)
				}
			}

			// 智能补全逻辑：如果只有一个匹配项，直接返回该匹配项
			// 除非存在多个以相同前缀开头的命令（如 set d 和 set device）
			if len(matchingChildren) == 1 {
				completions = matchingChildren
			} else if len(matchingChildren) > 1 {
				// 检查是否存在多个不同的前缀模式
				// 例如：set d 应该只补全 debug，除非存在 set device
				allSamePrefix := true
				firstChild := matchingChildren[0]

				// 检查所有匹配项是否都以当前输入为前缀，并且没有其他不同的前缀模式
				for i := 1; i < len(matchingChildren); i++ {
					// 如果存在不以第一个匹配项为前缀的项，说明有多个不同的前缀模式
					if !strings.HasPrefix(matchingChildren[i], firstChild) {
						allSamePrefix = false
						break
					}
				}

				if allSamePrefix {
					// 所有匹配项都以第一个匹配项为前缀，说明只有一个前缀模式
					// 例如：set d 匹配 debug 和 debug2，但都以 debug 为前缀
					completions = []string{firstChild}
				} else {
					// 存在多个不同的前缀模式，返回所有匹配项
					completions = matchingChildren
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

	// 如果命令树为 nil，直接返回空结果
	return completions
}

// GetNextLevelCompletions 获取下一级补全选项
func (c *CommandCompleter) GetNextLevelCompletions(input string) []string {
	var nextLevel []string

	if c.commandTree != nil {
		inputParts := strings.Fields(input)
		node := c.commandTree.Root

		for i := 0; i < len(inputParts)-1; i++ {
			if child, exists := node.Children[inputParts[i]]; exists {
				node = child
			} else {
				return nextLevel
			}
		}

		var matchingChildren []string
		lastPart := ""
		if len(inputParts) > 0 {
			lastPart = inputParts[len(inputParts)-1]
		}

		for name := range node.Children {
			if strings.HasPrefix(name, lastPart) {
				matchingChildren = append(matchingChildren, name)
			}
		}

		if len(matchingChildren) == 1 {
			baseParts := inputParts[:len(inputParts)-1]
			var fullCommand string
			if len(baseParts) > 0 {
				fullCommand = strings.Join(baseParts, " ") + " " + matchingChildren[0]
			} else {
				fullCommand = matchingChildren[0]
			}
			nextLevel = []string{fullCommand}
		} else if len(matchingChildren) > 1 {
			nextLevel = matchingChildren
		}

		return nextLevel
	}

	return nextLevel
}

// GetCommandSuggestions 获取命令建议（用于提示功能）
func (c *CommandCompleter) GetCommandSuggestions(input string) []string {
	var suggestions []string

	// 如果命令树为 nil，直接返回空结果
	if c.commandTree == nil {
		return suggestions
	}

	// 基于命令树获取建议
	// 这个方法需要重新设计以使用命令树结构
	// 目前返回空结果，待后续实现
	return suggestions
}

// GetCompletions 获取补全选项（统一入口）
func (c *CommandCompleter) GetCompletions(input string) []string {
	var completions []string

	if c.commandTree == nil {
		return completions
	}

	inputParts := strings.Fields(input)
	node := c.commandTree.Root

	for i := 0; i < len(inputParts)-1; i++ {
		if child, exists := node.Children[inputParts[i]]; exists {
			node = child
		} else {
			return completions
		}
	}

	if len(inputParts) == 0 {
		for name, child := range node.Children {
			if child.Type == NodeTypeCommand {
				completions = append(completions, name)
			}
		}
		return completions
	}

	currentInput := inputParts[len(inputParts)-1]
	var matchingChildren []string

	for name, child := range node.Children {
		if child.Type == NodeTypeCommand && strings.HasPrefix(name, currentInput) {
			matchingChildren = append(matchingChildren, name)
		}
	}

	if len(matchingChildren) == 1 {
		completions = matchingChildren
	} else if len(matchingChildren) > 1 {
		allSamePrefix := true
		firstChild := matchingChildren[0]

		for i := 1; i < len(matchingChildren); i++ {
			if !strings.HasPrefix(matchingChildren[i], firstChild) {
				allSamePrefix = false
				break
			}
		}

		if allSamePrefix {
			completions = []string{firstChild}
		} else {
			completions = matchingChildren
		}
	}

	return completions
}

// GetParameterCompletions 获取参数补全选项
func (c *CommandCompleter) GetParameterCompletions(input string) []string {
	var completions []string

	if c.commandTree == nil {
		return completions
	}

	inputParts := strings.Fields(input)
	node := c.commandTree.Root

	for i := 0; i < len(inputParts); i++ {
		if child, exists := node.Children[inputParts[i]]; exists {
			node = child
		} else {
			return completions
		}
	}

	var lastPart string
	if len(inputParts) > 0 {
		lastPart = inputParts[len(inputParts)-1]
	}

	for name, child := range node.Children {
		if child.Type != NodeTypeCommand && strings.HasPrefix(name, lastPart) {
			completions = append(completions, name)
		}
	}

	return completions
}

// GetRootCommands 获取根命令列表
func (c *CommandCompleter) GetRootCommands() []string {
	var commands []string

	if c.commandTree == nil {
		return commands
	}

	for name, child := range c.commandTree.Root.Children {
		if child.Type == NodeTypeCommand {
			commands = append(commands, name)
		}
	}

	return commands
}
