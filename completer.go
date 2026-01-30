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

	// 如果当前视图的命令树不可用，直接返回空结果
	return completions
}

// GetNextLevelCompletions 获取下一级补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetNextLevelCompletions(input string) []string {
	var nextLevel []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.commandTree == nil {
		return nextLevel
	}
	currentNode := c.context.CurrentMode.commandTree.Root

	inputParts := strings.Fields(input)
	node := currentNode

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

	// 补全当前视图命令树中的命令
	for name := range node.Children {
		if strings.HasPrefix(name, lastPart) {
			matchingChildren = append(matchingChildren, name)
		}
	}

	// 补全视图切换命令（从任意视图都可以切换到其他视图）
	if len(inputParts) == 1 && c.context != nil && c.context.CurrentMode != nil {
		rootMode := c.context.getRootMode()
		for name, subMode := range rootMode.Children {
			// 如果当前不是该子模式，则添加切换命令
			if c.context.CurrentMode != subMode && strings.HasPrefix(name, lastPart) {
				matchingChildren = append(matchingChildren, name)
			}
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

// GetCompletions 获取补全选项（统一入口，基于当前视图的命令树）
func (c *CommandCompleter) GetCompletions(input string) []string {
	var completions []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.commandTree == nil {
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

// GetParameterCompletions 获取参数补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetParameterCompletions(input string) []string {
	var completions []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.commandTree == nil {
		return completions
	}

	inputParts := strings.Fields(input)
	node := c.context.CurrentMode.commandTree.Root

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

// GetCurrentViewCommands 获取当前视图的命令列表（包括内置命令）
func (c *CommandCompleter) GetCurrentViewCommands() []string {
	var commands []string

	// 使用当前视图的可用命令
	if c.context != nil && c.context.CurrentMode != nil {
		availableCommands := c.context.GetAvailableCommands()
		for name := range availableCommands {
			// 只显示按空格分割的第一段
			firstPart := strings.Fields(name)[0]
			commands = append(commands, firstPart)
		}
	}

	return commands
}

// GetCommandTreeSuggestions 基于命令树获取当前节点的所有子节点作为建议
func (c *CommandCompleter) GetCommandTreeSuggestions(input string) []string {
	var suggestions []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.commandTree == nil {
		return suggestions
	}

	inputParts := strings.Fields(input)
	node := c.context.CurrentMode.commandTree.Root

	// 遍历到当前层级
	for i := 0; i < len(inputParts); i++ {
		if child, exists := node.Children[inputParts[i]]; exists {
			node = child
		} else {
			// 找不到匹配节点，返回空建议
			return suggestions
		}
	}

	// 显示当前节点的所有子节点（包括参数节点）
	for name := range node.Children {
		suggestions = append(suggestions, name)
	}
	//将视图切换命令也添加到建议中
	if len(inputParts) <= 1 {
		for _, key := range c.context.CurrentMode.commandTree.GetModeCommandKeys() {
			if strings.HasPrefix(key, input) {
				suggestions = append(suggestions, key)
			}
		}
	}
	return suggestions
}
