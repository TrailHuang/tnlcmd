package completer

import (
	"fmt"
	"sort"
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

// splitInput 拆分输入并报告末尾是否有空白。尾随空白表示最后一个 token 已输入完成，
// 这是进入参数层补全的关键信号；strings.Fields 会丢失该信息。
func splitInput(input string) ([]string, bool) {
	parts := strings.Fields(input)
	trailing := len(input) > 0 && (input[len(input)-1] == ' ' || input[len(input)-1] == '\t')
	return parts, trailing
}

// nodePriority 参数节点匹配优先级，保证同层多参数节点时导航结果稳定。
func nodePriority(node *commandtree.CommandNode) int {
	switch node.Type {
	case types.NodeTypeCommand:
		return 0
	case types.NodeTypeModeSwitch:
		return 1
	case types.NodeTypeEnum:
		return 2
	case types.NodeTypeNum:
		return 3
	case types.NodeTypeIPV4, types.NodeTypeIPV4Prefix,
		types.NodeTypeIPV6, types.NodeTypeIPV6Prefix,
		types.NodeTypeMAC, types.NodeTypeMACPrefix:
		return 4
	case types.NodeTypeString:
		return 5
	case types.NodeTypeOptional:
		return 6
	default:
		return 7
	}
}

// sortedChildren 按固定优先级与名称返回子节点，消除 map 遍历的随机顺序。
func sortedChildren(node *commandtree.CommandNode) []*commandtree.CommandNode {
	return sortedChildrenFromMap(node.Children)
}

// sortedChildrenFromMap 对任意子节点 map 应用同一稳定排序规则。
func sortedChildrenFromMap(childrenMap map[string]*commandtree.CommandNode) []*commandtree.CommandNode {
	children := make([]*commandtree.CommandNode, 0, len(childrenMap))
	for _, child := range childrenMap {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		pi, pj := nodePriority(children[i]), nodePriority(children[j])
		if pi != pj {
			return pi < pj
		}
		return children[i].Name < children[j].Name
	})
	return children
}

// walkInput 从 node 起按 parts 下钻：命令节点按名称精确匹配，参数节点按类型匹配取值。
// Tab 与 ? 共用此导航实现，保证两条路径看到的层级一致。
func walkInput(node *commandtree.CommandNode, parts []string) *commandtree.CommandNode {
	for _, token := range parts {
		matched := false
		for _, child := range sortedChildren(node) {
			if child.Type == types.NodeTypeCommand || child.Type == types.NodeTypeModeSwitch {
				if child.Name == token {
					node = child
					matched = true
					break
				}
			} else if commandtree.IsParameterMatch(child, token) {
				node = child
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
	}
	return node
}

// formatCandidate 生成候选的统一展示文本：命令名定宽 32 字符 + 描述。
// ? 帮助（GetCommandTreeSuggestions）与下钻候选（describeChildrenWithCR）共用，
// 保证三条生产入口的展示格式一致。
func formatCandidate(name string, child *commandtree.CommandNode) string {
	return fmt.Sprintf("%-32s %s", name, child.Description)
}

// resolveNodeForInput 定位当前输入所在的导航节点。
// 有尾随空白时全部 token 均已输入完成，下钻到最后一个 token 所在节点；
// 否则最后一个 token 视为正在输入的前缀，下钻到它的父节点。
// 返回值：节点、全部 token、当前前缀（可能为空）、是否有尾随空白。
func (c *CommandCompleter) resolveNodeForInput(input string) (*commandtree.CommandNode, []string, string, bool) {
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return nil, nil, "", false
	}
	parts, trailing := splitInput(input)
	walkParts := parts
	lastPart := ""
	if !trailing && len(parts) > 0 {
		walkParts = parts[:len(parts)-1]
		lastPart = parts[len(parts)-1]
	}
	node := walkInput(c.context.CurrentMode.CommandTree.Root, walkParts)
	if node == nil {
		return nil, parts, "", trailing
	}
	return node, parts, lastPart, trailing
}

// buildFullCommand 将唯一匹配拼接到已输入路径上；trailing 为 true 时保留
// 已输入 token 后的空格（它们都已完成），否则替换最后一个正在输入的前缀。
func buildFullCommand(inputParts []string, match string, trailing bool) []string {
	if trailing {
		return []string{strings.Join(append(append([]string{}, inputParts...), match), " ")}
	}
	if len(inputParts) <= 1 {
		return []string{match}
	}
	return []string{strings.Join(inputParts[:len(inputParts)-1], " ") + " " + match}
}

// GetNextLevelCompletions 获取下一级补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetNextLevelCompletions(input string) []string {
	node, parts, lastPart, trailing := c.resolveNodeForInput(input)
	if node == nil {
		return nil
	}

	// 落在参数节点：枚举补全为具体取值，其他参数给出格式提示（如 <1-10>、A.B.C.D）
	if node.Type != types.NodeTypeCommand && node.Type != types.NodeTypeModeSwitch {
		// 尾随空白表示本参数值已输入完成：优先给出后续串联参数，
		// 否则给出同层尚未使用的参数（排除刚消费的这个）
		if trailing {
			var next []string
			for _, child := range sortedChildren(node) {
				if child.Type != types.NodeTypeCommand && child.Type != types.NodeTypeModeSwitch {
					next = append(next, child.Name)
				}
			}
			if len(next) == 0 && node.Parent != nil {
				for _, child := range sortedChildren(node.Parent) {
					if child != node && child.Type != types.NodeTypeCommand && child.Type != types.NodeTypeModeSwitch {
						next = append(next, child.Name)
					}
				}
			}
			if len(next) == 1 {
				return buildFullCommand(parts, next[0], true)
			}
			return next
		}
		if node.Type == types.NodeTypeEnum {
			var values []string
			for _, v := range node.EnumValues {
				if strings.HasPrefix(v, lastPart) {
					values = append(values, v)
				}
			}
			sort.Strings(values)
			if len(values) == 1 {
				return buildFullCommand(parts, values[0], trailing)
			}
			return values
		}
		// 后续串联参数若是枚举，正在输入的前缀直接按取值补全
		var childValues []string
		for _, child := range sortedChildren(node) {
			if child.Type == types.NodeTypeEnum {
				for _, v := range child.EnumValues {
					if strings.HasPrefix(v, lastPart) {
						childValues = append(childValues, v)
					}
				}
			}
		}
		sort.Strings(childValues)
		if len(childValues) == 1 {
			return buildFullCommand(parts, childValues[0], trailing)
		}
		if len(childValues) > 1 {
			return childValues
		}
		if lastPart == "" || trailing || strings.HasPrefix(node.Name, lastPart) {
			return buildFullCommand(parts, node.Name, trailing)
		}
		return nil
	}

	var names []string
	modeSwitch := make(map[string]bool)
	for _, child := range sortedChildren(node) {
		if strings.HasPrefix(child.Name, lastPart) {
			names = append(names, child.Name)
			if child.Type == types.NodeTypeModeSwitch {
				modeSwitch[child.Name] = true
			}
		}
		// 正在输入枚举值时直接给出取值候选
		if child.Type == types.NodeTypeEnum && lastPart != "" {
			for _, v := range child.EnumValues {
				if strings.HasPrefix(v, lastPart) {
					names = append(names, v)
				}
			}
		}
	}

	// 根层附加视图切换命令，与命令树中的 ModeSwitch 节点去重
	if len(parts) <= 1 && c.context != nil && c.context.CurrentMode != nil {
		rootMode := c.context.GetRootMode()
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			seen[name] = true
		}
		for name, subMode := range rootMode.Children {
			if c.context.CurrentMode != subMode && strings.HasPrefix(name, lastPart) && !seen[name] {
				names = append(names, name)
				modeSwitch[name] = true
				seen[name] = true
			}
		}
	}
	sort.Strings(names)

	if len(names) == 1 {
		// 唯一匹配是完整输入的 ModeSwitch 节点时，展示目标模式的子命令
		if msNode, ok := node.Children[names[0]]; ok && modeSwitch[names[0]] && (lastPart == names[0] || trailing) {
			var nextLevel []string
			for name := range c.resolveChildren(msNode) {
				nextLevel = append(nextLevel, name)
			}
			sort.Strings(nextLevel)
			return nextLevel
		}
		return buildFullCommand(parts, names[0], trailing)
	}
	if len(names) > 1 {
		return names
	}
	return nil
}

// GetParameterCompletions 获取参数补全选项（基于当前视图的命令树）
func (c *CommandCompleter) GetParameterCompletions(input string) []string {
	if c.context == nil || c.context.CurrentMode == nil || c.context.CurrentMode.CommandTree == nil {
		return nil
	}
	parts, trailing := splitInput(input)
	node := walkInput(c.context.CurrentMode.CommandTree.Root, parts)
	if node == nil {
		return nil
	}
	lastPart := ""
	if !trailing && len(parts) > 0 {
		lastPart = parts[len(parts)-1]
	}

	var completions []string
	for _, child := range sortedChildren(node) {
		if child.Type != types.NodeTypeCommand && child.Type != types.NodeTypeModeSwitch && strings.HasPrefix(child.Name, lastPart) {
			completions = append(completions, child.Name)
		}
	}
	return completions
}

// GetCommandTreeSuggestions 基于命令树获取帮助候选，遵循网络设备分级语义：
//   - 空输入：返回当前视图所有一级命令（含视图切换命令）
//   - 输入完整关键字（如 "show"）：返回该命令的下一级候选（子命令或参数）
//   - 输入部分关键字（如 "sho"）：按前缀匹配返回候选
func (c *CommandCompleter) GetCommandTreeSuggestions(input string) []string {
	node, _, lastPart, trailing := c.resolveNodeForInput(input)
	if node == nil {
		return nil
	}
	tree := c.context.CurrentMode.CommandTree

	// 已输入完整视图名（含尾随空格）时命令即可执行，与精确命中的
	// ModeSwitch 一致返回 <cr>，而不是展开目标视图的子命令
	if trailing && node.Type == types.NodeTypeModeSwitch {
		return []string{"<cr>"}
	}

	// 最后一个 token 精确命中子节点 → 下钻展示其下一级
	if !trailing && lastPart != "" {
		if child, ok := node.Children[lastPart]; ok {
			if child.Type == types.NodeTypeModeSwitch {
				return []string{"<cr>"}
			}
			return c.describeChildrenWithCR(child)
		}
		for _, child := range sortedChildren(node) {
			if child.Type != types.NodeTypeCommand && child.Type != types.NodeTypeModeSwitch && commandtree.IsParameterMatch(child, lastPart) {
				return c.describeChildrenWithCR(child)
			}
		}
	}

	// 已落在参数节点：枚举列出取值，其他参数展示其后续节点或格式提示
	if node.Type != types.NodeTypeCommand && node.Type != types.NodeTypeModeSwitch {
		if node.Type == types.NodeTypeEnum {
			var values []string
			for _, v := range node.EnumValues {
				if strings.HasPrefix(v, lastPart) {
					values = append(values, fmt.Sprintf("%-32s %s", v, "enum value"))
				}
			}
			if len(values) > 0 {
				return values
			}
		}
		return c.describeChildrenWithCR(node)
	}

	// 在当前层级按前缀过滤候选（顺序稳定）
	var suggestions []string
	seen := make(map[string]bool)
	hasOptional := false
	for _, child := range sortedChildrenFromMap(c.resolveChildren(node)) {
		if child.Type == types.NodeTypeOptional {
			hasOptional = true
		}
		if strings.HasPrefix(child.Name, lastPart) {
			suggestions = append(suggestions, formatCandidate(child.Name, child))
			seen[child.Name] = true
		}
		// 正在输入枚举值时直接展示取值候选
		if lastPart != "" && child.Type == types.NodeTypeEnum {
			for _, v := range child.EnumValues {
				if strings.HasPrefix(v, lastPart) {
					suggestions = append(suggestions, fmt.Sprintf("%-32s %s", v, "enum value"))
				}
			}
		}
	}

	// 根层级附加视图切换命令并去重；仅在导航仍在根节点时追加，
	// 已下钻进某条命令（如 "clear "）的参数层不应混入视图切换命令
	if node == tree.Root {
		modeKeys := tree.GetModeCommandKeys()
		sort.Strings(modeKeys)
		for _, key := range modeKeys {
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
	for _, child := range sortedChildrenFromMap(children) {
		out = append(out, formatCandidate(child.Name, child))
		if child.Type == types.NodeTypeOptional {
			hasOptional = true
		}
	}
	if hasOptional {
		out = append(out, "<cr>")
	}
	return out
}
