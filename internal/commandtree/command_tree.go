package commandtree

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandNodeType 命令节点类型
type CommandNodeType = types.CommandNodeType

const (
	NodeTypeCommand    CommandNodeType = types.NodeTypeCommand    // 命令节点
	NodeTypeOptional                   = types.NodeTypeOptional   // 可选参数节点 []
	NodeTypeEnum                       = types.NodeTypeEnum       // 枚举参数节点 ()
	NodeTypeNum                        = types.NodeTypeNum        // 数值范围节点 <>
	NodeTypeString                     = types.NodeTypeString     // 字符串参数节点（大写字母）
	NodeTypeModeSwitch                 = types.NodeTypeModeSwitch // 视图切换命令节点
	NodeTypeIPV4                       = types.NodeTypeIPV4       // IPv4 地址节点 A.B.C.D
	NodeTypeIPV4Prefix                 = types.NodeTypeIPV4Prefix // IPv4 前缀节点 A.B.C.D/M
	NodeTypeIPV6                       = types.NodeTypeIPV6       // IPv6 地址节点 X:X::X:X
	NodeTypeIPV6Prefix                 = types.NodeTypeIPV6Prefix // IPv6 前缀节点 X:X::X:X/M
	NodeTypeMAC                        = types.NodeTypeMAC        // MAC 地址节点 M:A:C
	NodeTypeMACPrefix                  = types.NodeTypeMACPrefix  // MAC 前缀节点 M:A:C/M
)

// CommandNode 命令树节点
type CommandNode struct {
	Name        string
	Type        CommandNodeType
	Description string
	Handler     types.ContextHandler // 统一以 ctx 版签名存储，旧签名经 types.Adapt 包装
	Children    map[string]*CommandNode
	Parent      *CommandNode

	// 参数特定字段
	EnumValues []string    // 枚举值列表
	EnumRanges []EnumRange // 枚举中嵌入的数值范围，与 EnumValues 按顺序对应
	RangeMin   int         // 范围最小值
	RangeMax   int         // 范围最大值
	IsRequired bool        // 是否必需参数

	// 视图切换特定字段
	ModeName string // 要切换到的视图名称
}

// EnumRange 枚举中嵌入的数值范围
type EnumRange struct {
	Min int
	Max int
}

// PathNode 路径节点，包含节点名称和类型信息
type PathNode struct {
	Name string          // 节点名称
	Type CommandNodeType // 节点类型
}

// CommandTree 命令树
type CommandTree struct {
	Root         *CommandNode
	ModeCommands map[string]*CommandNode // 视图切换命令存储（实例级别）
}

// NewCommandTree 创建新的命令树
func NewCommandTree() *CommandTree {
	return &CommandTree{
		Root: &CommandNode{
			Name:     "root",
			Type:     NodeTypeCommand,
			Children: make(map[string]*CommandNode),
		},
		ModeCommands: make(map[string]*CommandNode),
	}
}

// NewCommandNode 创建新的命令节点
func NewCommandNode(name string, nodeType CommandNodeType, description string) *CommandNode {
	return &CommandNode{
		Name:        name,
		Type:        nodeType,
		Description: description,
		Children:    make(map[string]*CommandNode),
	}
}

// AddCommand 添加命令到命令树（旧签名 handler，经 Adapt 包装为 ContextHandler 存储）
func (t *CommandTree) AddCommand(command string, description string, handler types.CommandHandler, detailedDescription ...string) error {
	return t.addCommand(command, description, types.Adapt(handler), detailedDescription...)
}

// AddContextCommand 添加支持上下文的命令到命令树
func (t *CommandTree) AddContextCommand(command string, description string, handler types.ContextHandler, detailedDescription ...string) error {
	return t.addCommand(command, description, handler, detailedDescription...)
}

// addCommand 添加命令到命令树的公共实现
func (t *CommandTree) addCommand(command string, description string, handler types.ContextHandler, detailedDescription ...string) error {
	// 解析完整的命令字符串，包括参数
	nodes, err := t.parseCommandString(command)
	if err != nil {
		return err
	}

	current := t.Root
	for _, node := range nodes {
		if existing, exists := current.Children[node.Name]; exists {
			current = existing
		} else {
			node.Parent = current
			current.Children[node.Name] = node
			current = node
		}
	}

	// 设置叶子节点的处理函数和描述（叶子节点包含完整的命令信息）
	current.Handler = handler
	current.Description = description

	// 处理多行详细描述
	if len(detailedDescription) > 0 && detailedDescription[0] != "" {
		// 将多行描述按\n分割，并保存到每个对应的命令节点
		lines := strings.Split(detailedDescription[0], "\n")

		// 从根节点开始，为路径上的每个节点设置对应的描述行
		pathNodes := t.getCommandPathNodes(command)
		for i, node := range pathNodes {
			if i < len(lines) && lines[i] != "" {
				node.Description = lines[i]
			}
		}
	}

	return nil
}

// getCommandPathNodes 获取命令路径上的所有节点
func (t *CommandTree) getCommandPathNodes(command string) []*CommandNode {
	var pathNodes []*CommandNode

	// 解析命令字符串
	nodes, err := t.parseCommandString(command)
	if err != nil {
		return pathNodes
	}

	// 从根节点的子节点开始遍历路径（不包含根节点）
	current := t.Root

	for _, node := range nodes {
		if existing, exists := current.Children[node.Name]; exists {
			current = existing
			pathNodes = append(pathNodes, current)
		} else {
			// 如果节点不存在，停止遍历
			break
		}
	}

	return pathNodes
}

// AddModeCommand 添加视图切换命令到命令树
func (t *CommandTree) AddModeCommand(modeName string, description string) error {
	// 创建视图切换命令节点
	node := NewCommandNode(modeName, NodeTypeModeSwitch, description)
	node.ModeName = modeName
	node.IsRequired = true
	node.Type = NodeTypeModeSwitch

	// 添加到根节点
	t.Root.Children[modeName] = node
	node.Parent = t.Root

	// 添加到实例级别的视图切换命令存储
	t.ModeCommands[modeName] = node

	return nil
}

// splitCommandParts 按空白拆分命令，但把 (), [], <> 内的内容保留为单个 token，
// 使 (on | off) 这类带空格的枚举写法可以被正确解析；括号不闭合或多出闭合符时返回错误。
func splitCommandParts(command string) ([]string, error) {
	var parts []string
	var cur strings.Builder
	var open byte

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if open != 0 {
			cur.WriteByte(ch)
			if ch == open {
				open = 0
			}
			continue
		}
		switch ch {
		case '(':
			open = ')'
			cur.WriteByte(ch)
		case '[':
			open = ']'
			cur.WriteByte(ch)
		case '<':
			open = '>'
			cur.WriteByte(ch)
		case ')', ']', '>':
			return nil, fmt.Errorf("unmatched closing %q in command %q", ch, command)
		case ' ', '\t', '\n', '\r':
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if open != 0 {
		return nil, fmt.Errorf("unclosed %q in command %q", open, command)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts, nil
}

// parseCommandString 解析命令字符串，构建完整的树结构
func (t *CommandTree) parseCommandString(command string) ([]*CommandNode, error) {
	parts, err := splitCommandParts(command)
	if err != nil {
		return nil, err
	}

	var nodes []*CommandNode
	for _, part := range parts {
		node, err := t.parseCommandPart(part)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// parseCommandPart 解析命令部分，支持参数语法
func (t *CommandTree) parseCommandPart(part string) (*CommandNode, error) {
	// 定义参数类型解析器
	parsers := []struct {
		prefix, suffix string
		nodeType       CommandNodeType
		description    string
		isRequired     bool
		parser         func(string) (*CommandNode, bool)
	}{
		{"[", "]", NodeTypeOptional, "Optional parameter", false, t.parseOptionalParam},
		{"(", ")", NodeTypeEnum, "Enum parameter", true, t.parseEnumParam},
		{"<", ">", NodeTypeNum, "Range parameter", true, t.parseRangeParam},
	}

	// 尝试匹配参数类型；形似参数但解析失败时明确报错，不静默降级为普通命令
	for _, parser := range parsers {
		if strings.HasPrefix(part, parser.prefix) && strings.HasSuffix(part, parser.suffix) {
			if node, ok := parser.parser(part); ok {
				return node, nil
			}
			return nil, fmt.Errorf("invalid parameter syntax %q", part)
		}
	}

	// 占位符参数（A.B.C.D / X:X::X:X / M:A:C 等）
	if node, ok := parsePlaceholderParam(part); ok {
		return node, nil
	}

	// 字符串参数（全大写字母）
	if isAllUppercase(part) {
		return NewCommandNode(part, NodeTypeString, "String parameter"), nil
	}

	// 普通命令
	return NewCommandNode(part, NodeTypeCommand, "Command"), nil
}

// parseOptionalParam 解析可选参数
func (t *CommandTree) parseOptionalParam(part string) (*CommandNode, bool) {
	param := strings.Trim(part, "[]")
	node := NewCommandNode(param, NodeTypeOptional, "Optional parameter")
	node.IsRequired = false
	return node, true
}

// parseEnumParam 解析枚举参数
func (t *CommandTree) parseEnumParam(part string) (*CommandNode, bool) {
	param := strings.Trim(part, "()")
	values := strings.Split(param, "|")
	for i, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, false
		}
		values[i] = v
	}
	// 统一规范化节点名与值列表，避免带空格写法产生畸形树结构或脏候选
	name := "(" + strings.Join(values, "|") + ")"
	node := NewCommandNode(name, NodeTypeEnum, "Enum parameter")
	node.EnumValues = values
	node.EnumRanges = make([]EnumRange, len(values))
	for i, v := range values {
		if min, max, ok := parseInlineRange(v); ok {
			node.EnumRanges[i] = EnumRange{Min: min, Max: max}
		}
	}
	node.IsRequired = true
	return node, true
}

// parseInlineRange 解析形如 <1-100> 的内联范围，若解析成功返回 true
func parseInlineRange(value string) (int, int, bool) {
	if !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") {
		return 0, 0, false
	}
	inner := strings.Trim(value, "<>")
	if !strings.Contains(inner, "-") {
		return 0, 0, false
	}
	parts := strings.Split(inner, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	min, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	max, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return min, max, true
}

// parseRangeParam 解析数值范围参数
func (t *CommandTree) parseRangeParam(part string) (*CommandNode, bool) {
	param := strings.Trim(part, "<>")
	if !strings.Contains(param, "-") {
		return nil, false
	}

	rangeParts := strings.Split(param, "-")
	if len(rangeParts) != 2 {
		return nil, false
	}

	min, err1 := strconv.Atoi(rangeParts[0])
	max, err2 := strconv.Atoi(rangeParts[1])
	if err1 != nil || err2 != nil {
		return nil, false
	}

	node := NewCommandNode(part, NodeTypeNum, "Range parameter")
	node.RangeMin = min
	node.RangeMax = max
	node.IsRequired = true
	return node, true
}

// isAllUppercase 检查字符串是否全大写字母
func isAllUppercase(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// FindCommand 查找匹配的命令
func (t *CommandTree) FindCommand(args []string) (*CommandNode, []string, []string, error) {
	// 如果只有一个参数，优先在视图切换命令中查找
	if len(args) == 1 {
		modeName := args[0]
		if modeNode, exists := t.ModeCommands[modeName]; exists {
			// 精确匹配到视图切换命令
			return modeNode, []string{modeName}, []string{}, nil
		}
		// 精确匹配失败，尝试唯一前缀匹配视图切换命令
		var prefixMatchKey string
		var prefixMatchNode *CommandNode
		for key, node := range t.ModeCommands {
			if strings.HasPrefix(key, modeName) {
				if prefixMatchNode != nil {
					// 多个匹配，无法唯一确定
					prefixMatchNode = nil
					break
				}
				prefixMatchKey = key
				prefixMatchNode = node
			}
		}
		if prefixMatchNode != nil {
			return prefixMatchNode, []string{prefixMatchKey}, []string{}, nil
		}
	}

	// 否则使用正常的命令查找逻辑
	return t.Root.findCommand(args, nil, nil)
}

// findCommand 递归查找匹配的命令
func (n *CommandNode) findCommand(args []string, path []string, matchArgs []string) (*CommandNode, []string, []string, error) {
	if len(args) == 0 {
		// 到达命令末尾，返回当前节点
		if n.Handler != nil {
			return n, path, matchArgs, nil
		}
		// 如果没有处理函数，继续查找可选参数
		for _, child := range n.Children {
			if child.Type == NodeTypeOptional {
				return child.findCommand(args, path, matchArgs)
			}
		}
		return nil, path, matchArgs, fmt.Errorf("%w: %s", types.ErrIncompleteCommand, strings.Join(path, " "))
	}

	currentArg := args[0]
	remainingArgs := args[1:]

	// 首先尝试精确匹配命令节点
	for _, child := range n.Children {
		if (child.Type == NodeTypeCommand || child.Type == NodeTypeModeSwitch) && child.Name == currentArg {
			newPath := make([]string, len(path)+1)
			copy(newPath, path)
			newPath[len(path)] = currentArg
			return child.findCommand(remainingArgs, newPath, matchArgs)
		}
	}

	// 精确匹配失败，尝试唯一前缀匹配命令节点
	// 如果输入是某个命令的唯一前缀（如 shu -> shutdown），则等价于该命令
	var prefixMatch *CommandNode
	for _, child := range n.Children {
		if (child.Type == NodeTypeCommand || child.Type == NodeTypeModeSwitch) && strings.HasPrefix(child.Name, currentArg) {
			if prefixMatch != nil {
				// 多个命令匹配同一前缀，无法唯一确定，放弃前缀匹配
				prefixMatch = nil
				break
			}
			prefixMatch = child
		}
	}
	if prefixMatch != nil {
		newPath := make([]string, len(path)+1)
		copy(newPath, path)
		newPath[len(path)] = prefixMatch.Name
		return prefixMatch.findCommand(remainingArgs, newPath, matchArgs)
	}

	// 前缀匹配也失败，尝试参数节点匹配
	for _, child := range n.Children {
		// 参数节点匹配：基于参数类型验证值
		// 首先检查节点是否是参数节点（非命令节点）
		if child.Type == types.NodeTypeCommand {
			continue
		}
		// 可选参数需要特殊处理 - 尝试递归匹配
		if child.Type == types.NodeTypeOptional {
			if matchedNode, matchedPath, tmpargs, err := child.findCommand(args, path, matchArgs); err == nil {
				return matchedNode, matchedPath, tmpargs, nil
			}
		} else if IsParameterMatch(child, currentArg) {
			// 参数节点匹配成功，返回当前节点，剩余参数作为处理函数的参数
			return child.findCommand(remainingArgs, append(path, currentArg), append(matchArgs, currentArg))
		}
	}

	// 如果没有匹配的子节点，检查当前节点是否有处理函数
	if n.Handler != nil {
		// 如果当前节点是可选参数节点且还有参数，将当前参数作为可选参数的值
		if n.Type == NodeTypeOptional {
			return n, append(path, currentArg), append(matchArgs, currentArg), nil
		}
		// 当前节点有处理函数，但还有未匹配的参数：
		// 必须一并返回给会话层做数量校验，否则多余参数会被静默丢弃
		allArgs := make([]string, 0, len(matchArgs)+len(args))
		allArgs = append(allArgs, matchArgs...)
		allArgs = append(allArgs, args...)
		return n, path, allArgs, nil
	}

	return nil, path, matchArgs, fmt.Errorf("%w: %s", types.ErrUnknownCommand, currentArg)
}

// PrintTree 打印命令树结构
func (t *CommandTree) PrintTree() string {
	var result strings.Builder
	t.printNode(t.Root, "", true, &result)
	return result.String()
}

// printNode 递归打印节点
func (t *CommandTree) printNode(node *CommandNode, prefix string, isLast bool, result *strings.Builder) {
	// 打印当前节点
	if node.Parent != nil { // 跳过根节点
		result.WriteString(prefix)
		if isLast {
			result.WriteString("└── ")
		} else {
			result.WriteString("├── ")
		}

		// 显示节点信息和处理函数状态
		if node.Handler != nil {
			// 获取处理函数的名称
			handlerName := getFunctionName(node.Handler)
			result.WriteString(fmt.Sprintf("%s [Handler: %s] (%s)", node.Name, handlerName, getNodeTypeString(node.Type)))
		} else {
			result.WriteString(fmt.Sprintf("%s (%s)", node.Name, getNodeTypeString(node.Type)))
		}

		if node.Description != "" && node.Description != "Command" {
			result.WriteString(fmt.Sprintf(" - %s", node.Description))
		}
		result.WriteString("\n")
	}

	// 递归打印子节点
	if len(node.Children) > 0 {
		childPrefix := prefix
		if node.Parent != nil {
			if isLast {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}

		children := make([]*CommandNode, 0, len(node.Children))
		for _, child := range node.Children {
			children = append(children, child)
		}

		// 按名称排序子节点，确保输出一致
		sort.Slice(children, func(i, j int) bool {
			return children[i].Name < children[j].Name
		})

		for i, child := range children {
			isLastChild := i == len(children)-1
			t.printNode(child, childPrefix, isLastChild, result)
		}
	}
}

// getFunctionName 获取函数名称
func getFunctionName(handler types.ContextHandler) string {
	if handler == nil {
		return "nil"
	}

	// 使用反射获取函数指针
	funcValue := reflect.ValueOf(handler)
	if funcValue.Kind() != reflect.Func {
		return "unknown"
	}

	// 获取函数指针
	funcPtr := funcValue.Pointer()

	// 使用runtime获取函数信息
	funcInfo := runtime.FuncForPC(funcPtr)
	if funcInfo == nil {
		return "unknown"
	}

	// 获取完整的函数名
	fullName := funcInfo.Name()

	// 提取简短的函数名（去掉包路径）
	parts := strings.Split(fullName, ".")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return fullName
}

// getNodeTypeString 获取节点类型字符串
func getNodeTypeString(nodeType CommandNodeType) string {
	switch nodeType {
	case NodeTypeCommand:
		return "Command"
	case NodeTypeOptional:
		return "Optional"
	case NodeTypeEnum:
		return "Enum"
	case NodeTypeNum:
		return "Range"
	case NodeTypeString:
		return "String"
	case NodeTypeIPV4:
		return "IPV4"
	case NodeTypeIPV4Prefix:
		return "IPV4_PREFIX"
	case NodeTypeIPV6:
		return "IPV6"
	case NodeTypeIPV6Prefix:
		return "IPV6_PREFIX"
	case NodeTypeMAC:
		return "MAC"
	case NodeTypeMACPrefix:
		return "MAC_PREFIX"
	default:
		return "Unknown"
	}
}

// IsParameterMatch 检查参数是否匹配
func IsParameterMatch(node *CommandNode, input string) bool {
	// 简单的参数匹配逻辑
	// 在实际实现中，应该根据参数类型进行更复杂的验证

	// 检查常见的参数类型模式
	switch node.Type {
	case NodeTypeNum: // 范围参数，如 <1-10>
		return isValidNumberInRange(node, input)
	case NodeTypeEnum: // 枚举参数，如 (on|off)
		return isValidEnumValue(node, input)
	case NodeTypeString:
		if isString(input) {
			return true
		}
	case NodeTypeOptional: // 可选参数，任意非空字符串均匹配
		if isString(input) {
			return true
		}
	case NodeTypeIPV4, NodeTypeIPV4Prefix,
		NodeTypeIPV6, NodeTypeIPV6Prefix,
		NodeTypeMAC, NodeTypeMACPrefix: // 占位符参数，按类型校验格式
		return isPlaceholderValue(node.Type, input)
	default:
		// 默认情况下，如果参数名包含输入，则认为匹配
		return false
	}
	return false
}

// isValidNumberInRange 检查数字参数值是否在指定范围内
func isValidNumberInRange(node *CommandNode, input string) bool {
	// 首先检查是否是有效数字
	num, err := strconv.Atoi(input)
	if err != nil {
		return false
	}

	min, max := resolveNumberRange(node)

	// 如果没有指定范围，接受任何有效数字
	if min == 0 && max == 0 {
		return true
	}

	// 检查数字是否在指定范围内
	return num >= min && num <= max
}

// resolveNumberRange 返回数值参数的有效范围。
// 统一基于节点结构体字段（RangeMin/RangeMax），二者均为 0 视为无范围限制。
func resolveNumberRange(node *CommandNode) (int, int) {
	return node.RangeMin, node.RangeMax
}

// GetNumberValidationError 获取数字参数验证错误信息
func GetNumberValidationError(node *CommandNode, input string) string {
	// 首先检查是否是有效数字
	num, err := strconv.Atoi(input)
	if err != nil {
		return fmt.Sprintf("无效的数字格式: '%s'", input)
	}

	min, max := resolveNumberRange(node)

	// 如果没有指定范围，接受任何有效数字
	if min == 0 && max == 0 {
		return ""
	}

	// 检查数字是否在指定范围内
	if num >= min && num <= max {
		return ""
	}

	// 生成错误消息
	if num < min {
		return fmt.Sprintf("数字太小: %d，有效范围: %d-%d", num, min, max)
	}
	if num > max {
		return fmt.Sprintf("数字太大: %d，有效范围: %d-%d", num, min, max)
	}

	return fmt.Sprintf("数字 %d 超出有效范围: %d-%d", num, min, max)
}

func isString(str string) bool {
	return len(str) > 0
}

// isValidEnumValue 检查枚举参数值是否有效
func isValidEnumValue(node *CommandNode, input string) bool {
	// 统一基于节点结构体字段 EnumValues / EnumRanges
	enumValues := node.EnumValues
	if len(enumValues) == 0 && len(node.EnumRanges) == 0 {
		// 如果没有明确的枚举值定义，接受任何输入
		return true
	}

	// 检查输入是否在枚举值列表中（不区分大小写）
	for _, value := range enumValues {
		if strings.EqualFold(value, input) {
			return true
		}
	}

	// 检查输入是否匹配任一内联数值范围
	for _, r := range node.EnumRanges {
		if r.Min == 0 && r.Max == 0 {
			continue
		}
		if num, err := strconv.Atoi(input); err == nil && num >= r.Min && num <= r.Max {
			return true
		}
	}

	return false
}

// GetEnumValidationError 获取枚举参数验证错误信息
func GetEnumValidationError(node *CommandNode, input string) string {
	// 统一基于节点结构体字段 EnumValues / EnumRanges
	enumValues := node.EnumValues
	if len(enumValues) == 0 && len(node.EnumRanges) == 0 {
		return ""
	}

	// 检查是否为有效值
	if isValidEnumValue(node, input) {
		return ""
	}

	// 生成错误消息
	if len(input) == 0 {
		return fmt.Sprintf("参数不能为空，有效值: %s", strings.Join(enumValues, ", "))
	}

	// 检查部分匹配
	var partialMatches []string
	for _, value := range enumValues {
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(input)) {
			partialMatches = append(partialMatches, value)
		}
	}

	if len(partialMatches) > 0 {
		return fmt.Sprintf("不完整的参数，可能的完整值: %s", strings.Join(partialMatches, ", "))
	}

	// 输入可能是数值：提示内联数值范围
	if _, err := strconv.Atoi(input); err == nil {
		var ranges []string
		for _, r := range node.EnumRanges {
			if r.Min != 0 || r.Max != 0 {
				ranges = append(ranges, fmt.Sprintf("%d-%d", r.Min, r.Max))
			}
		}
		if len(ranges) > 0 {
			return fmt.Sprintf("无效的参数值 '%s'，有效范围: %s", input, strings.Join(ranges, ", "))
		}
	}

	return fmt.Sprintf("无效的参数值 '%s'，有效值: %s", input, strings.Join(enumValues, ", "))
}
