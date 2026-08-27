package completer

import (
	"fmt"
	"strings"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandCompleter 命令补全器
type CommandCompleter struct {
	commandTree *commandtree.CommandTree // 树形命令存储（向后兼容）
	context     *mode.CommandContext     // 命令上下文，用于访问当前视图的独立命令树
}

// NewCommandCompleter 创建新的命令补全器
func NewCommandCompleter() *CommandCompleter {
	return &CommandCompleter{}
}

// NewCommandCompleterWithTree 创建带命令树的补全器
func NewCommandCompleterWithTree(tree *commandtree.CommandTree) *CommandCompleter {
	return &CommandCompleter{
		commandTree: tree,
	}
}

// NewCommandCompleterWithContext 使用上下文创建新的命令补全器
func NewCommandCompleterWithContext(context *mode.CommandContext) *CommandCompleter {
	return &CommandCompleter{
		context: context,
	}
}

// UpdateContext 更新命令上下文
func (c *CommandCompleter) UpdateContext(context *mode.CommandContext) {
	c.context = context
}

// UpdateCommandTree 更新命令树
func (c *CommandCompleter) UpdateCommandTree(tree *commandtree.CommandTree) {
	c.commandTree = tree
}

// resolveChildren 解析节点的子命令：如果是 ModeSwitch 节点，返回目标模式的命令树子节点
func (c *CommandCompleter) resolveChildren(node *commandtree.CommandNode) map[string]*commandtree.CommandNode {
	if node.Type == types.NodeTypeModeSwitch && node.ModeName != "" && c.context != nil {
		rootMode := c.context.GetRootMode()
		if targetMode, exists := rootMode.Children[node.ModeName]; exists && targetMode.CommandTree != nil {
			return targetMode.CommandTree.Root.Children
		}
	}
	return node.Children
}

// resolveNodeForInput 沿输入路径遍历到最后一个 token 之前的节点。
// 返回该节点、输入拆分后的全部 token 以及最后一个 token（可能为空字符串）。
// 当上下文不可用或路径无法遍历到时返回 (nil, inputParts, "")。
func (c *CommandCompleter) resolveNodeForInput(input string) (*commandtree.CommandNode, []string, string) {
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return nil, nil, ""
	}
	inputParts := strings.Fields(input)
	node := c.context.CurrentMode.CommandTree.Root
	for i := 0; i < len(inputParts)-1; i++ {
		if child, exists := node.Children[inputParts[i]]; exists {
			node = child
		} else {
			return nil, inputParts, ""
		}
	}
	lastPart := ""
	if len(inputParts) > 0 {
		lastPart = inputParts[len(inputParts)-1]
	}
	return node, inputParts, lastPart
}

// collectCommandCompletions 收集节点下名称以 lastPart 为前缀的命令类节点。
// includeModeSwitch 控制是否纳入视图切换节点；lastPart 为空时返回全部符合条件的命令。
func (c *CommandCompleter) collectCommandCompletions(node *commandtree.CommandNode, lastPart string, includeModeSwitch bool) []string {
	var matches []string
	for name, child := range node.Children {
		if (child.Type == types.NodeTypeCommand || (includeModeSwitch && child.Type == types.NodeTypeModeSwitch)) && strings.HasPrefix(name, lastPart) {
			matches = append(matches, name)
		}
	}
	return matches
}

// applyCommonPrefix 对候选项应用最长公共前缀压缩：
// 唯一候选直接返回；多个候选若存在比当前输入更长的公共前缀则只返回该前缀，否则返回全部候选。
func applyCommonPrefix(matches []string, currentInput string) []string {
	if len(matches) == 1 {
		return matches
	}
	if len(matches) > 1 {
		lcp := findLongestCommonPrefix(matches)
		if lcp != "" && lcp != currentInput {
			return []string{lcp}
		}
		return matches
	}
	return nil
}

// buildFullCommand 将唯一匹配的命令拼接到已输入路径上，形成完整命令字符串。
func buildFullCommand(inputParts []string, match string) []string {
	if len(inputParts) == 0 {
		return []string{match}
	}
	baseParts := inputParts[:len(inputParts)-1]
	if len(baseParts) > 0 {
		return []string{strings.Join(baseParts, " ") + " " + match}
	}
	return []string{match}
}

// Complete 命令补全
func (c *CommandCompleter) Complete(input string) []string {
	node, inputParts, currentInput := c.resolveNodeForInput(input)
	if node == nil {
		return nil
	}
	matches := c.collectCommandCompletions(node, currentInput, true)
	// 空输入时直接返回全部命令，不应用公共前缀压缩
	if len(inputParts) == 0 {
		return matches
	}
	return applyCommonPrefix(matches, currentInput)
}

// GetNextLevelCompletions 获取下一级补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetNextLevelCompletions(input string) []string {
	node, inputParts, lastPart := c.resolveNodeForInput(input)
	if node == nil {
		return nil
	}

	var matchingChildren []string
	// seen 用于去重：视图切换命令同时存在于命令树 Root.Children
	// 和 rootMode.Children 中，避免重复添加
	seen := make(map[string]bool)

	// 补全当前视图命令树中的命令
	for name := range node.Children {
		if strings.HasPrefix(name, lastPart) {
			matchingChildren = append(matchingChildren, name)
			seen[name] = true
		}
	}

	// 补全视图切换命令（从任意视图都可以切换到其他视图）
	if len(inputParts) == 1 && c.context != nil && c.context.CurrentMode != nil {
		rootMode := c.context.GetRootMode()
		for name, subMode := range rootMode.Children {
			// 如果当前不是该子模式，则添加切换命令
			// 跳过已通过命令树添加过的命令（避免重复）
			if c.context.CurrentMode != subMode && strings.HasPrefix(name, lastPart) && !seen[name] {
				matchingChildren = append(matchingChildren, name)
				seen[name] = true
			}
		}
	}

	if len(matchingChildren) == 1 {
		// 如果唯一匹配是 ModeSwitch 节点，且输入已是完整命令名，展示目标模式的子命令
		if matchedNode, exists := node.Children[matchingChildren[0]]; exists && matchedNode.Type == types.NodeTypeModeSwitch && lastPart == matchingChildren[0] {
			var nextLevel []string
			targetChildren := c.resolveChildren(matchedNode)
			for name := range targetChildren {
				nextLevel = append(nextLevel, name)
			}
			return nextLevel
		}
		// 否则补全为完整命令名
		return buildFullCommand(inputParts, matchingChildren[0])
	} else if len(matchingChildren) > 1 {
		return matchingChildren
	}

	return nil
}

// GetCompletions 获取补全选项（统一入口，基于当前视图的命令树）
func (c *CommandCompleter) GetCompletions(input string) []string {
	node, inputParts, currentInput := c.resolveNodeForInput(input)
	if node == nil {
		return nil
	}
	matches := c.collectCommandCompletions(node, currentInput, false)
	// 空输入时直接返回全部命令，不应用公共前缀压缩
	if len(inputParts) == 0 {
		return matches
	}
	return applyCommonPrefix(matches, currentInput)
}

// GetParameterCompletions 获取参数补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetParameterCompletions(input string) []string {
	var completions []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return completions
	}

	inputParts := strings.Fields(input)
	node := c.context.CurrentMode.CommandTree.Root

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
		if child.Type != types.NodeTypeCommand && strings.HasPrefix(name, lastPart) {
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
			parts := strings.Fields(name)
			if len(parts) == 0 {
				continue
			}
			commands = append(commands, parts[0])
		}
	}

	return commands
}

// describeChildren 生成节点下一级候选的显示文本（名称定宽 + 描述）。
// 对 ModeSwitch 节点会解析到目标视图的命令；对普通命令节点返回其子命令/参数节点。
func (c *CommandCompleter) describeChildren(node *commandtree.CommandNode) []string {
	var out []string
	children := c.resolveChildren(node)
	for name, child := range children {
		out = append(out, fmt.Sprintf("%-32s %s", name, child.Description))
	}
	return out
}

// GetCommandTreeSuggestions 基于命令树获取帮助候选，遵循网络设备分级语义：
//   - 空输入：返回当前视图所有一级命令（含视图切换命令）
//   - 输入完整关键字（如 "show"）：返回该命令的下一级候选（子命令或参数）
//   - 输入部分关键字（如 "sho"）：按前缀匹配返回候选
func (c *CommandCompleter) GetCommandTreeSuggestions(input string) []string {
	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return nil
	}
	tree := c.context.CurrentMode.CommandTree

	inputParts := strings.Fields(input)
	node := tree.Root
	lastPart := ""
	if len(inputParts) > 0 {
		lastPart = inputParts[len(inputParts)-1]
		// 导航到最后一个关键字之前的父节点
		for i := 0; i < len(inputParts)-1; i++ {
			token := inputParts[i]
			if child, ok := node.Children[token]; ok {
				node = child
				continue
			}
			// 尝试参数节点匹配（Enum/Num/String/Optional）
			matched := false
			for _, child := range node.Children {
				if child.Type != types.NodeTypeCommand && commandtree.IsParameterMatch(child, token) {
					node = child
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}
		// 最后一个关键字精确命中命令/模式节点 → 下钻显示其下一级
		if child, ok := node.Children[lastPart]; ok {
			return c.describeChildrenWithCR(child)
		}
		// 最后一个关键字是已输入的参数值 → 导航到该参数节点，显示其下一级候选
		for _, child := range node.Children {
			if child.Type != types.NodeTypeCommand && commandtree.IsParameterMatch(child, lastPart) {
				return c.describeChildrenWithCR(child)
			}
		}
	}

	// 在当前层级按前缀过滤候选
	var suggestions []string
	seen := make(map[string]bool)
	children := c.resolveChildren(node)
	hasOptional := false
	for name, child := range children {
		if child.Type == types.NodeTypeOptional {
			hasOptional = true
		}
		if strings.HasPrefix(name, lastPart) {
			suggestions = append(suggestions, fmt.Sprintf("%-32s %s", name, child.Description))
			seen[name] = true
		}
	}

	// 根层级附加视图切换命令并去重
	if node.Type != types.NodeTypeModeSwitch && len(inputParts) <= 1 {
		for _, key := range tree.GetModeCommandKeys() {
			if seen[key] {
				continue
			}
			if strings.HasPrefix(key, lastPart) {
				suggestions = append(suggestions, fmt.Sprintf("%-32s Switch to %s mode", key, key))
				seen[key] = true
			}
		}
	}

	// 当前层级存在可选参数时，命令已可回车执行，追加 <cr> 提示
	if hasOptional {
		suggestions = append(suggestions, "<cr>")
	}
	return suggestions
}

// describeChildrenWithCR 生成节点下一级候选并追加 <cr> 提示（节点存在可选参数子节点时）。
func (c *CommandCompleter) describeChildrenWithCR(node *commandtree.CommandNode) []string {
	out := c.describeChildren(node)
	for _, child := range c.resolveChildren(node) {
		if child.Type == types.NodeTypeOptional {
			return append(out, "<cr>")
		}
	}
	return out
}

// findLongestCommonPrefix 找到字符串切片中最长的公共前缀
func findLongestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	lcp := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, lcp) {
			lcp = lcp[:len(lcp)-1]
			if lcp == "" {
				return ""
			}
		}
	}
	return lcp
}
