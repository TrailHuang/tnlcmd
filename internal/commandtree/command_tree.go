package commandtree

import (
	"fmt"
	"reflect"
	"regexp"
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
)

// CommandNode 命令树节点
type CommandNode struct {
	Name        string
	Type        CommandNodeType
	Description string
	Handler     types.CommandHandler
	Children    map[string]*CommandNode
	Parent      *CommandNode

	// 参数特定字段
	EnumValues []string // 枚举值列表
	RangeMin   int      // 范围最小值
	RangeMax   int      // 范围最大值
	IsRequired bool     // 是否必需参数

	// 视图切换特定字段
	ModeName string // 要切换到的视图名称
}

// PathNode 路径节点，包含节点名称和类型信息
type PathNode struct {
	Name string          // 节点名称
	Type CommandNodeType // 节点类型
}

// CommandTree 命令树
type CommandTree struct {
	Root *CommandNode
}

var ModeCommands = make(map[string]*CommandNode) // 全局视图切换命令存储

// NewCommandTree 创建新的命令树
func NewCommandTree() *CommandTree {
	return &CommandTree{
		Root: &CommandNode{
			Name:     "root",
			Type:     NodeTypeCommand,
			Children: make(map[string]*CommandNode),
		},
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

// GetModeCommandKeys 获取ModeCommands中的所有key，按数组返回
func (t *CommandTree) GetModeCommandKeys() []string {
	keys := make([]string, 0, len(ModeCommands))
	for key := range ModeCommands {
		keys = append(keys, key)
	}
	return keys
}

// AddCommand 添加命令到命令树
func (t *CommandTree) AddCommand(command string, description string, handler types.CommandHandler, detailedDescription ...string) error {
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

	// 从根节点开始遍历路径
	current := t.Root
	pathNodes = append(pathNodes, current)

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

	// 同时添加到全局视图切换命令存储
	ModeCommands[modeName] = node

	return nil
}

// parseCommandString 解析命令字符串，构建完整的树结构
func (t *CommandTree) parseCommandString(command string) ([]*CommandNode, error) {
	var nodes []*CommandNode

	// 按空格分割命令
	parts := strings.Fields(command)

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

	// 尝试匹配参数类型
	for _, parser := range parsers {
		if strings.HasPrefix(part, parser.prefix) && strings.HasSuffix(part, parser.suffix) {
			if node, ok := parser.parser(part); ok {
				return node, nil
			}
		}
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
	node := NewCommandNode(part, NodeTypeEnum, "Enum parameter")
	node.EnumValues = values
	node.IsRequired = true
	return node, true
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
	// 如果只有一个参数，优先在全局视图切换命令中查找
	if len(args) == 1 {
		modeName := args[0]
		if modeNode, exists := ModeCommands[modeName]; exists {
			// 找到匹配的视图切换命令
			return modeNode, []string{modeName}, []string{}, nil
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
		return nil, path, matchArgs, fmt.Errorf("incomplete command")
	}

	currentArg := args[0]
	remainingArgs := args[1:]

	// 首先尝试精确匹配命令节点
	for _, child := range n.Children {
		if (child.Type == NodeTypeCommand || child.Type == NodeTypeModeSwitch) && child.Name == currentArg {
			return child.findCommand(remainingArgs, append(path, currentArg), matchArgs)
		}
	}

	// 如果没有精确匹配，尝试参数节点匹配
	for _, child := range n.Children {
		// 参数节点匹配：基于参数类型验证值
		// 首先检查节点是否是参数节点（非命令节点）
		if child.Type != types.NodeTypeCommand {
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
	}

	// 如果没有匹配的子节点，检查当前节点是否有处理函数
	if n.Handler != nil {
		// 当前节点有处理函数，但还有未匹配的参数
		// 将这些参数传递给处理函数
		return n, path, matchArgs, nil
	}

	return nil, path, matchArgs, fmt.Errorf("unknown command: %s", currentArg)
}

// GetCompletions 获取补全建议
func (n *CommandNode) GetCompletions(args []string) []string {
	var completions []string

	if len(args) == 0 {
		// 返回所有子节点的名称
		for name := range n.Children {
			completions = append(completions, name)
		}
		return completions
	}

	currentArg := args[0]
	remainingArgs := args[1:]

	// 查找匹配的子节点
	for _, child := range n.Children {
		switch child.Type {
		case NodeTypeCommand, NodeTypeModeSwitch:
			if strings.HasPrefix(child.Name, currentArg) {
				if len(remainingArgs) == 0 {
					completions = append(completions, child.Name)
				} else {
					completions = append(completions, child.GetCompletions(remainingArgs)...)
				}
			}
		case NodeTypeEnum:
			if len(remainingArgs) == 0 {
				for _, enumValue := range child.EnumValues {
					if strings.HasPrefix(enumValue, currentArg) {
						completions = append(completions, enumValue)
					}
				}
			}
		case NodeTypeNum:
			if len(remainingArgs) == 0 {
				// 返回范围提示
				completions = append(completions, fmt.Sprintf("<%d-%d>", child.RangeMin, child.RangeMax))
			}
		case NodeTypeString:
			if len(remainingArgs) == 0 {
				completions = append(completions, child.Name)
			}
		case NodeTypeOptional:
			// 可选参数：同时考虑包含和不包含的情况
			completions = append(completions, child.GetCompletions(args)...)
			completions = append(completions, child.GetCompletions(remainingArgs)...)
		}
	}

	return completions
}

// ValidateCommand 验证命令参数
func (n *CommandNode) ValidateCommand(args []string) error {
	if len(args) == 0 {
		// 检查必需参数
		for _, child := range n.Children {
			if child.IsRequired && child.Type != NodeTypeOptional {
				return fmt.Errorf("missing required parameter: %s", child.Name)
			}
		}
		return nil
	}

	currentArg := args[0]
	remainingArgs := args[1:]

	// 验证当前参数
	for _, child := range n.Children {
		switch child.Type {
		case NodeTypeCommand:
			if child.Name == currentArg {
				return child.ValidateCommand(remainingArgs)
			}
		case NodeTypeEnum:
			matched := false
			for _, enumValue := range child.EnumValues {
				if enumValue == currentArg {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("invalid enum value: %s, expected one of: %v", currentArg, child.EnumValues)
			}
			return child.ValidateCommand(remainingArgs)
		case NodeTypeNum:
			if num, err := strconv.Atoi(currentArg); err != nil {
				return fmt.Errorf("invalid number: %s", currentArg)
			} else if num < child.RangeMin || num > child.RangeMax {
				return fmt.Errorf("number out of range: %d, expected %d-%d", num, child.RangeMin, child.RangeMax)
			}
			return child.ValidateCommand(remainingArgs)
		case NodeTypeString:
			return child.ValidateCommand(remainingArgs)
		case NodeTypeOptional:
			// 可选参数：尝试验证，如果失败则跳过
			if err := child.ValidateCommand(args); err == nil {
				return nil
			}
			return child.ValidateCommand(remainingArgs)
		}
	}

	return fmt.Errorf("unknown parameter: %s", currentArg)
}

// GetCommandDescription 获取命令描述
func (n *CommandNode) GetCommandDescription() string {
	if n.Description != "" {
		return n.Description
	}

	var parts []string
	current := n
	for current != nil && current.Parent != nil {
		parts = append([]string{current.Name}, parts...)
		current = current.Parent
	}

	return strings.Join(parts, " ")
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
func getFunctionName(handler types.CommandHandler) string {
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
		if isNumber(input) {
			return true
		}
	case NodeTypeEnum: // 枚举参数，如 (on|off)
		return isValidEnumValue(node, input)
	case NodeTypeString:
		if isString(input) {
			return true
		}
	default:
		// 默认情况下，如果参数名包含输入，则认为匹配
		return false
	}
	return false
}

func isNumber(str string) bool {
	_, err := strconv.Atoi(str)
	return err == nil
}

func isString(str string) bool {
	return len(str) > 0
}

// isValidEnumValue 检查枚举参数值是否有效
func isValidEnumValue(node *CommandNode, input string) bool {
	// 从节点描述中提取枚举值
	// 枚举参数格式如: (on|off|enable|disable)
	enumValues := extractEnumValues(node.Description)
	if len(enumValues) == 0 {
		// 如果没有明确的枚举值定义，接受任何输入
		return true
	}

	// 检查输入是否在枚举值列表中（不区分大小写）
	for _, value := range enumValues {
		if strings.EqualFold(value, input) {
			return true
		}
	}

	// 如果输入不在枚举值列表中，检查是否为部分匹配（用于补全）
	if len(input) > 0 {
		for _, value := range enumValues {
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(input)) {
				return true
			}
		}
	}

	return false
}

// extractEnumValues 从描述中提取枚举值
func extractEnumValues(description string) []string {
	// 枚举值通常用括号括起来，如 (on|off) 或 [enable|disable]
	var enumValues []string

	// 匹配括号内的枚举值
	re := regexp.MustCompile(`[\(\[](.*?)[\)\]]`)
	matches := re.FindStringSubmatch(description)
	if len(matches) > 1 {
		// 分割枚举值
		values := strings.Split(matches[1], "|")
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				enumValues = append(enumValues, value)
			}
		}
	}

	return enumValues
}

// GetEnumValidationError 获取枚举参数验证错误信息
func GetEnumValidationError(node *CommandNode, input string) string {
	enumValues := extractEnumValues(node.Description)
	if len(enumValues) == 0 {
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

	return fmt.Sprintf("无效的参数值 '%s'，有效值: %s", input, strings.Join(enumValues, ", "))
}

// GetEnumCompletions 获取枚举参数的补全选项
func GetEnumCompletions(node *CommandNode, input string) []string {
	enumValues := extractEnumValues(node.Description)
	if len(enumValues) == 0 {
		return nil
	}

	var completions []string
	for _, value := range enumValues {
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(input)) {
			completions = append(completions, value)
		}
	}

	return completions
}

// isValidIPAddress 检查是否为有效的IP地址
func isValidIPAddress(ip string) bool {
	// 简单的IP地址验证
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if num, err := strconv.Atoi(part); err != nil || num < 0 || num > 255 {
			return false
		}
	}
	return true
}

func isValidIPv6Address(ip string) bool {
	// 简单的IPv6地址验证
	parts := strings.Split(ip, ":")
	if len(parts) != 8 {
		return false
	}
	for _, part := range parts {
		if num, err := strconv.ParseUint(part, 16, 16); err != nil || num < 0 || num > 0xFFFF {
			return false
		}
	}
	return true
}
