package commandtree

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// 占位符语法与类型名称的映射表。
// 注册命令时使用下列写法声明参数占位符，命令树在解析阶段将其识别为
// 独立的参数节点类型，并在执行前对参数值做格式校验。
//
//	占位符写法   类型名称        含义        匹配示例                argv[i] 拿到什么
//	A.B.C.D      IPV4            IPv4 地址   192.168.1.1            点分十进制字符串
//	A.B.C.D/M    IPV4_PREFIX     IPv4 前缀   10.0.0.0/8             含掩码的前缀字符串
//	X:X::X:X     IPV6            IPv6 地址   2001:db8::1            标准 IPv6 文本
//	X:X::X:X/M   IPV6_PREFIX     IPv6 前缀   2001:db8::/32          含长度前缀字符串
//	M:A:C        MAC             MAC 地址    aa:bb:cc:dd:ee:ff      六段十六进制字符串
//	M:A:C/M      MAC_PREFIX      MAC 前缀    aa:bb:cc:00:00:00/24    带掩码的 MAC 字符串
//
// 所有占位符均为必填参数（IsRequired = true）。
var placeholderTypes = []struct {
	// pattern 占位符模板，X/M/C/D 为通配段位
	pattern string
	// nodeType 解析出的节点类型
	nodeType types.CommandNodeType
	// typeName 类型名称，用于节点描述
	typeName string
	// description 节点描述文案
	description string
}{
	{"X:X::X:X/M", types.NodeTypeIPV6Prefix, "IPV6_PREFIX", "IPv6 prefix parameter"},
	{"X:X::X:X", types.NodeTypeIPV6, "IPV6", "IPv6 address parameter"},
	{"A.B.C.D/M", types.NodeTypeIPV4Prefix, "IPV4_PREFIX", "IPv4 prefix parameter"},
	{"A.B.C.D", types.NodeTypeIPV4, "IPV4", "IPv4 address parameter"},
	{"M:A:C/M", types.NodeTypeMACPrefix, "MAC_PREFIX", "MAC prefix parameter"},
	{"M:A:C", types.NodeTypeMAC, "MAC", "MAC address parameter"},
}

// parsePlaceholderParam 解析占位符参数。
// 命中返回对应类型的参数节点；未命中返回 (nil, false)，
// 调用方需继续走其他参数类型（可选/枚举/范围/字符串）的解析。
func parsePlaceholderParam(part string) (*CommandNode, bool) {
	for _, pt := range placeholderTypes {
		if !matchPlaceholderPattern(pt.pattern, part) {
			continue
		}
		node := NewCommandNode(part, pt.nodeType, pt.description)
		node.IsRequired = true
		return node, true
	}
	return nil, false
}

// matchPlaceholderPattern 判断占位符文本 part 是否符合给定模板 pattern。
// 占位符语法是固定写法（如 "A.B.C.D/M"），比对为大小写不敏感的整体相等；
// 前缀类模板（含 "/"）要求 part 同样带 "/" 掩码段，反之亦然。
// 注意：该函数仅用于注册期的占位符文本识别，参数值的格式校验由
// isPlaceholderValue 按节点类型负责。
func matchPlaceholderPattern(pattern, part string) bool {
	if strings.Contains(pattern, "/") {
		patAddr, patMask, _ := strings.Cut(pattern, "/")
		partAddr, partMask, found := strings.Cut(part, "/")
		if !found {
			return false
		}
		return strings.EqualFold(patAddr, partAddr) && strings.EqualFold(patMask, partMask)
	}
	// 非前缀模板不得带掩码段（避免 "A.B.C.D" 误吞 "A.B.C.D/M"）
	if strings.Contains(part, "/") {
		return false
	}
	return strings.EqualFold(pattern, part)
}

// isIPV4 校验输入是否为合法 IPv4 地址
func isIPV4(input string) bool {
	ip := net.ParseIP(input)
	if ip == nil {
		return false
	}
	return ip.To4() != nil
}

// isIPV4Prefix 校验输入是否为合法 IPv4 前缀（形如 10.0.0.0/8）
func isIPV4Prefix(input string) bool {
	ip, _, err := net.ParseCIDR(input)
	if err != nil {
		return false
	}
	return ip.To4() != nil
}

// isIPV6 校验输入是否为合法 IPv6 地址
func isIPV6(input string) bool {
	ip := net.ParseIP(input)
	if ip == nil {
		return false
	}
	return ip.To4() == nil && ip.To16() != nil
}

// isIPV6Prefix 校验输入是否为合法 IPv6 前缀（形如 2001:db8::/32）
func isIPV6Prefix(input string) bool {
	ip, _, err := net.ParseCIDR(input)
	if err != nil {
		return false
	}
	return ip.To4() == nil && ip.To16() != nil
}

// isMAC 校验输入是否为合法 MAC 地址（六段十六进制，冒号分隔）
func isMAC(input string) bool {
	parts := strings.Split(input, ":")
	if len(parts) != 6 {
		return false
	}
	for _, p := range parts {
		if len(p) != 2 {
			return false
		}
		if _, err := strconv.ParseUint(p, 16, 8); err != nil {
			return false
		}
	}
	return true
}

// isMACPrefix 校验输入是否为合法 MAC 前缀（形如 aa:bb:cc:00:00:00/24）
func isMACPrefix(input string) bool {
	addr, mask, found := strings.Cut(input, "/")
	if !found {
		return false
	}
	bits, err := strconv.Atoi(mask)
	if err != nil || bits < 0 || bits > 48 {
		return false
	}
	return isMAC(addr)
}

// isPlaceholderValue 按节点类型校验占位符参数值
func isPlaceholderValue(nodeType types.CommandNodeType, input string) bool {
	switch nodeType {
	case types.NodeTypeIPV4:
		return isIPV4(input)
	case types.NodeTypeIPV4Prefix:
		return isIPV4Prefix(input)
	case types.NodeTypeIPV6:
		return isIPV6(input)
	case types.NodeTypeIPV6Prefix:
		return isIPV6Prefix(input)
	case types.NodeTypeMAC:
		return isMAC(input)
	case types.NodeTypeMACPrefix:
		return isMACPrefix(input)
	default:
		return false
	}
}

// GetPlaceholderValidationError 生成占位符参数的校验错误信息（导出供 session 层使用）
func GetPlaceholderValidationError(nodeType types.CommandNodeType, input string) string {
	switch nodeType {
	case types.NodeTypeIPV4:
		return fmt.Sprintf("无效的 IPv4 地址: '%s'（期望形如 192.168.1.1）", input)
	case types.NodeTypeIPV4Prefix:
		return fmt.Sprintf("无效的 IPv4 前缀: '%s'（期望形如 10.0.0.0/8）", input)
	case types.NodeTypeIPV6:
		return fmt.Sprintf("无效的 IPv6 地址: '%s'（期望形如 2001:db8::1）", input)
	case types.NodeTypeIPV6Prefix:
		return fmt.Sprintf("无效的 IPv6 前缀: '%s'（期望形如 2001:db8::/32）", input)
	case types.NodeTypeMAC:
		return fmt.Sprintf("无效的 MAC 地址: '%s'（期望形如 aa:bb:cc:dd:ee:ff）", input)
	case types.NodeTypeMACPrefix:
		return fmt.Sprintf("无效的 MAC 前缀: '%s'（期望形如 aa:bb:cc:00:00:00/24）", input)
	default:
		return fmt.Sprintf("无效的参数值: '%s'", input)
	}
}

// getPlaceholderCompletions 生成占位符参数的补全提示
func getPlaceholderCompletions(nodeType types.CommandNodeType, input string) []string {
	if input != "" {
		// 已输入部分内容时不做补全
		return nil
	}
	switch nodeType {
	case types.NodeTypeIPV4:
		return []string{"A.B.C.D"}
	case types.NodeTypeIPV4Prefix:
		return []string{"A.B.C.D/M"}
	case types.NodeTypeIPV6:
		return []string{"X:X::X:X"}
	case types.NodeTypeIPV6Prefix:
		return []string{"X:X::X:X/M"}
	case types.NodeTypeMAC:
		return []string{"M:A:C"}
	case types.NodeTypeMACPrefix:
		return []string{"M:A:C/M"}
	default:
		return nil
	}
}

// isPlaceholderNode 判断节点是否为占位符参数节点
func isPlaceholderNode(nodeType types.CommandNodeType) bool {
	switch nodeType {
	case types.NodeTypeIPV4, types.NodeTypeIPV4Prefix,
		types.NodeTypeIPV6, types.NodeTypeIPV6Prefix,
		types.NodeTypeMAC, types.NodeTypeMACPrefix:
		return true
	default:
		return false
	}
}
