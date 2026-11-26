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
	context *mode.CommandContext // 命令上下文，用于访问当前视图的独立命令树
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

// walkExact 从 node 起按精确名连续下钻前 count 个 token；任一层不存在即返回 nil。
// count 为 0 时不移动（返回 node 本身），为 -1 时同样不移动。
// GetNextLevelCompletions 与 GetParameterCompletions 共用此导航实现，避免重复遍历逻辑。
func walkExact(node *commandtree.CommandNode, tokens []string, count int) *commandtree.CommandNode {
	for i := 0; i < count; i++ {
		child, exists := node.Children[tokens[i]]
		if !exists {
			return nil
		}
		node = child
	}
	return node
}

// formatCandidate 生成候选的统一展示文本：命令名定宽 32 字符 + 描述。
// ? 帮助（GetCommandTreeSuggestions）与下钻候选（describeChildrenWithCR）共用，
// 保证三条生产入口的展示格式一致。
func formatCandidate(name string, child *commandtree.CommandNode) string {
	return fmt.Sprintf("%-32s %s", name, child.Description)
}

// resolveNodeForInput 沿输入路径精确匹配遍历到最后一个 token 之前的节点。
// 返回该节点、输入拆分后的全部 token 以及最后一个 token（可能为空字符串）。
// 当上下文不可用或路径无法遍历到时返回 (nil, inputParts, "")。
func (c *CommandCompleter) resolveNodeForInput(input string) (*commandtree.CommandNode, []string, string) {
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return nil, nil, ""
	}
	inputParts := strings.Fields(input)
	node := walkExact(c.context.CurrentMode.CommandTree.Root, inputParts, len(inputParts)-1)
	if node == nil {
		return nil, inputParts, ""
	}
	lastPart := ""
	if len(inputParts) > 0 {
		lastPart = inputParts[len(inputParts)-1]
	}
	return node, inputParts, lastPart
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

// GetParameterCompletions 获取参数补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetParameterCompletions(input string) []string {
	var completions []string

	// 使用当前视图的命令树
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return completions
	}

	inputParts := strings.Fields(input)
	node := walkExact(c.context.CurrentMode.CommandTree.Root, inputParts, len(inputParts))
	if node == nil {
		return completions
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
			// 视图切换命令：输入完整视图名即可回车执行切换，提示 <cr> 而非展开目标视图子命令
			if child.Type == types.NodeTypeModeSwitch {
				return []string{"<cr>"}
			}
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
			suggestions = append(suggestions, formatCandidate(name, child))
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

// describeChildrenWithCR 生成节点下一级候选的展示文本并按需追加 <cr> 提示。
// 对 ModeSwitch 节点会解析到目标视图的命令；对普通命令节点返回其子命令/参数节点
// （名称定宽 + 描述）；节点存在可选参数子节点时追加 <cr> 提示。
func (c *CommandCompleter) describeChildrenWithCR(node *commandtree.CommandNode) []string {
	children := c.resolveChildren(node)
	out := make([]string, 0, len(children)+1)
	hasOptional := false
	for name, child := range children {
		out = append(out, formatCandidate(name, child))
		if child.Type == types.NodeTypeOptional {
			hasOptional = true
		}
	}
	if hasOptional {
		out = append(out, "<cr>")
	}
	return out
}
