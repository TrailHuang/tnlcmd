package commandtree

import (
	"strings"
	"testing"

	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// TestParsePlaceholderParam 验证六种占位符写法的解析与类型映射
func TestParsePlaceholderParam(t *testing.T) {
	for _, tc := range []struct {
		part     string
		wantType types.CommandNodeType
		wantName string
	}{
		{"A.B.C.D", types.NodeTypeIPV4, "A.B.C.D"},
		{"A.B.C.D/M", types.NodeTypeIPV4Prefix, "A.B.C.D/M"},
		{"X:X::X:X", types.NodeTypeIPV6, "X:X::X:X"},
		{"X:X::X:X/M", types.NodeTypeIPV6Prefix, "X:X::X:X/M"},
		{"M:A:C", types.NodeTypeMAC, "M:A:C"},
		{"M:A:C/M", types.NodeTypeMACPrefix, "M:A:C/M"},
	} {
		node, ok := parsePlaceholderParam(tc.part)
		if !ok {
			t.Errorf("parsePlaceholderParam(%q) not matched", tc.part)
			continue
		}
		if node.Type != tc.wantType {
			t.Errorf("parsePlaceholderParam(%q) type = %v, want %v", tc.part, node.Type, tc.wantType)
		}
		if node.Name != tc.wantName {
			t.Errorf("parsePlaceholderParam(%q) name = %q, want %q", tc.part, node.Name, tc.wantName)
		}
		if !node.IsRequired {
			t.Errorf("parsePlaceholderParam(%q) should be required", tc.part)
		}
	}

	// 非占位符写法不命中
	for _, p := range []string{"STRING", "<1-10>", "(on|off)", "[OPT]", "show", "192.168.1.1"} {
		if _, ok := parsePlaceholderParam(p); ok {
			t.Errorf("parsePlaceholderParam(%q) should not match", p)
		}
	}
}

// TestPlaceholderValueValidation 验证各占位符类型的值校验
func TestPlaceholderValueValidation(t *testing.T) {
	for _, tc := range []struct {
		nodeType types.CommandNodeType
		input    string
		want     bool
	}{
		// IPV4
		{types.NodeTypeIPV4, "192.168.1.1", true},
		{types.NodeTypeIPV4, "10.0.0.0", true},
		{types.NodeTypeIPV4, "255.255.255.255", true},
		{types.NodeTypeIPV4, "256.1.1.1", false},
		{types.NodeTypeIPV4, "192.168.1", false},
		{types.NodeTypeIPV4, "192.168.1.1.1", false},
		{types.NodeTypeIPV4, "abc", false},
		{types.NodeTypeIPV4, "10.0.0.0/8", false},
		{types.NodeTypeIPV4, "::1", false},
		// IPV4_PREFIX
		{types.NodeTypeIPV4Prefix, "10.0.0.0/8", true},
		{types.NodeTypeIPV4Prefix, "192.168.1.0/24", true},
		{types.NodeTypeIPV4Prefix, "10.0.0.0", false},
		{types.NodeTypeIPV4Prefix, "10.0.0.0/33", false},
		{types.NodeTypeIPV4Prefix, "2001:db8::/32", false}, // IPv6 前缀不算 IPv4 前缀
		// IPV6
		{types.NodeTypeIPV6, "2001:db8::1", true},
		{types.NodeTypeIPV6, "::1", true},
		{types.NodeTypeIPV6, "fe80::1%eth0", false}, // zone 不接受
		{types.NodeTypeIPV6, "192.168.1.1", false},  // IPv4 映射形式不算纯 IPv6
		{types.NodeTypeIPV6, "2001:db8::zzzz", false},
		// IPV6_PREFIX
		{types.NodeTypeIPV6Prefix, "2001:db8::/32", true},
		{types.NodeTypeIPV6Prefix, "::/0", true},
		{types.NodeTypeIPV6Prefix, "2001:db8::/129", false},
		{types.NodeTypeIPV6Prefix, "2001:db8::1", false},
		// MAC
		{types.NodeTypeMAC, "aa:bb:cc:dd:ee:ff", true},
		{types.NodeTypeMAC, "00:11:22:33:44:55", true},
		{types.NodeTypeMAC, "AA:BB:CC:DD:EE:FF", true},
		{types.NodeTypeMAC, "aa:bb:cc:dd:ee", false},
		{types.NodeTypeMAC, "aa:bb:cc:dd:ee:ff:00", false},
		{types.NodeTypeMAC, "aa:bb:cc:dd:ee:fg", false},
		{types.NodeTypeMAC, "aabb.ccdd.eeff", false}, // 点分格式不接受
		{types.NodeTypeMAC, "a:b:c:d:e:f", false},    // 段必须两位
		// MAC_PREFIX
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00/24", true},
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00/48", true},
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00/49", false},
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00", false},
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00/xx", false},
	} {
		if got := isPlaceholderValue(tc.nodeType, tc.input); got != tc.want {
			t.Errorf("isPlaceholderValue(%v, %q) = %v, want %v", tc.nodeType, tc.input, got, tc.want)
		}
	}
}

// TestAddCommandWithPlaceholders 验证注册含占位符的命令后树结构与查找
func TestAddCommandWithPlaceholders(t *testing.T) {
	tree := NewCommandTree()
	called := false
	if err := tree.AddCommand("ip route A.B.C.D/M X:X::X:X", "Add IPv4 route via IPv6 nexthop", func(args []string) string {
		called = true
		return "ok"
	}); err != nil {
		t.Fatalf("AddCommand: %v", err)
	}

	// 合法参数应命中命令并收到原始字符串
	node, _, args, err := tree.FindCommand([]string{"ip", "route", "10.0.0.0/8", "2001:db8::1"})
	if err != nil {
		t.Fatalf("FindCommand valid: %v", err)
	}
	if node == nil || node.Handler == nil {
		t.Fatal("FindCommand valid: handler node not found")
	}
	if len(args) != 2 || args[0] != "10.0.0.0/8" || args[1] != "2001:db8::1" {
		t.Errorf("args = %v, want [10.0.0.0/8 2001:db8::1]", args)
	}
	_ = node.Handler(nil, args)
	if !called {
		t.Error("handler not called")
	}

	// 非法 IPv4 前缀不应命中
	if _, _, _, err := tree.FindCommand([]string{"ip", "route", "not-a-prefix", "2001:db8::1"}); err == nil {
		t.Error("FindCommand with invalid prefix should fail")
	}
	// 非法 IPv6 地址不应命中
	if _, _, _, err := tree.FindCommand([]string{"ip", "route", "10.0.0.0/8", "nothex::zz"}); err == nil {
		t.Error("FindCommand with invalid IPv6 should fail")
	}
}

// TestPlaceholderValidationErrorMessages 验证错误信息包含期望格式提示
func TestPlaceholderValidationErrorMessages(t *testing.T) {
	cases := []struct {
		nodeType types.CommandNodeType
		input    string
		wantSub  string
	}{
		{types.NodeTypeIPV4, "999.1.1.1", "192.168.1.1"},
		{types.NodeTypeIPV4Prefix, "10.0.0.0", "10.0.0.0/8"},
		{types.NodeTypeIPV6, "zz::1", "2001:db8::1"},
		{types.NodeTypeIPV6Prefix, "2001:db8::", "/32"},
		{types.NodeTypeMAC, "aa:bb:cc", "aa:bb:cc:dd:ee:ff"},
		{types.NodeTypeMACPrefix, "aa:bb:cc:00:00:00", "/24"},
	}
	for _, tc := range cases {
		msg := GetPlaceholderValidationError(tc.nodeType, tc.input)
		if !strings.Contains(msg, tc.wantSub) {
			t.Errorf("GetPlaceholderValidationError(%v, %q) = %q, want contains %q", tc.nodeType, tc.input, msg, tc.wantSub)
		}
		if !strings.Contains(msg, tc.input) {
			t.Errorf("error message should echo input %q, got %q", tc.input, msg)
		}
	}
}

// TestFindCommandEachPlaceholderType 验证每种占位符类型在完整命令查找中的
// 命中（原始字符串传给 handler）与非法值拒绝
func TestFindCommandEachPlaceholderType(t *testing.T) {
	for _, tc := range []struct {
		cmd     string // 注册的命令定义
		valid   []string
		invalid []string
	}{
		{"ping A.B.C.D",
			[]string{"ping", "192.168.1.1"},
			[]string{"ping", "999.1.1.1"}},
		{"network A.B.C.D/M",
			[]string{"network", "10.0.0.0/8"},
			[]string{"network", "10.0.0.0"}},
		{"route6 X:X::X:X",
			[]string{"route6", "2001:db8::1"},
			[]string{"route6", "2001:db8::zz"}},
		{"prefix6 X:X::X:X/M",
			[]string{"prefix6", "2001:db8::/32"},
			[]string{"prefix6", "2001:db8::1"}},
		{"neighbor M:A:C",
			[]string{"neighbor", "aa:bb:cc:dd:ee:ff"},
			[]string{"neighbor", "aa:bb:cc:dd:ee"}},
		{"vlan-mac M:A:C/M",
			[]string{"vlan-mac", "aa:bb:cc:00:00:00/24"},
			[]string{"vlan-mac", "aa:bb:cc:00:00:00"}},
	} {
		tree := NewCommandTree()
		called := false
		if err := tree.AddCommand(tc.cmd, "test", func(args []string) string {
			called = true
			return "ok"
		}); err != nil {
			t.Fatalf("AddCommand(%q): %v", tc.cmd, err)
		}

		// 合法值命中，args 为原始字符串
		node, _, args, err := tree.FindCommand(tc.valid)
		if err != nil {
			t.Errorf("cmd %q valid %v: %v", tc.cmd, tc.valid, err)
			continue
		}
		if node == nil || node.Handler == nil {
			t.Errorf("cmd %q valid: handler node not found", tc.cmd)
			continue
		}
		if len(args) != 1 || args[0] != tc.valid[len(tc.valid)-1] {
			t.Errorf("cmd %q valid: args = %v, want [%s]", tc.cmd, args, tc.valid[len(tc.valid)-1])
		}
		_ = node.Handler(nil, args)
		if !called {
			t.Errorf("cmd %q valid: handler not called", tc.cmd)
		}

		// 非法值拒绝
		if _, _, _, err := tree.FindCommand(tc.invalid); err == nil {
			t.Errorf("cmd %q invalid %v should fail", tc.cmd, tc.invalid)
		}
	}
}

// TestMatchPlaceholderPattern 验证占位符模板比对的边界行为：
// 大小写不敏感、前缀模板互斥、非占位符文本不命中
func TestMatchPlaceholderPattern(t *testing.T) {
	for _, tc := range []struct {
		pattern, part string
		want          bool
	}{
		// 大小写不敏感
		{"A.B.C.D", "a.b.c.d", true},
		{"M:A:C", "m:a:c", true},
		// 前缀模板互斥
		{"A.B.C.D", "A.B.C.D/M", false}, // 无掩码模板不匹配带掩码文本
		{"A.B.C.D/M", "A.B.C.D", false}, // 前缀模板不匹配无掩码文本
		{"M:A:C", "M:A:C/M", false},
		{"M:A:C/M", "M:A:C", false},
		// 掩码段必须一致
		{"A.B.C.D/M", "A.B.C.D/M", true},
		{"A.B.C.D/M", "A.B.C.D/N", false},
		// 地址部分不一致
		{"A.B.C.D", "A.B.C", false},
		{"M:A:C", "M:A:B", false},
		{"X:X::X:X", "X:X::X", false},
		// 点分与冒号写法互不匹配
		{"A.B.C.D", "X:X::X:X", false},
		{"M:A:C", "A.B.C.D", false},
	} {
		if got := matchPlaceholderPattern(tc.pattern, tc.part); got != tc.want {
			t.Errorf("matchPlaceholderPattern(%q, %q) = %v, want %v", tc.pattern, tc.part, got, tc.want)
		}
	}
}

// TestGetNodeTypeStringPlaceholderTypes 验证新节点类型的展示名称
func TestGetNodeTypeStringPlaceholderTypes(t *testing.T) {
	for _, tc := range []struct {
		nodeType types.CommandNodeType
		want     string
	}{
		{types.NodeTypeIPV4, "IPV4"},
		{types.NodeTypeIPV4Prefix, "IPV4_PREFIX"},
		{types.NodeTypeIPV6, "IPV6"},
		{types.NodeTypeIPV6Prefix, "IPV6_PREFIX"},
		{types.NodeTypeMAC, "MAC"},
		{types.NodeTypeMACPrefix, "MAC_PREFIX"},
	} {
		if got := getNodeTypeString(tc.nodeType); got != tc.want {
			t.Errorf("getNodeTypeString(%v) = %q, want %q", tc.nodeType, got, tc.want)
		}
	}
}

// TestPlaceholderValidationErrorUnknownType 验证未知类型的兜底错误信息
func TestPlaceholderValidationErrorUnknownType(t *testing.T) {
	msg := GetPlaceholderValidationError(types.NodeTypeCommand, "anything")
	if !strings.Contains(msg, "anything") {
		t.Errorf("unknown-type error should echo input, got %q", msg)
	}
}

// TestPlaceholderCompletionsUnknownType 验证未知类型补全返回空
func TestPlaceholderCompletionsUnknownType(t *testing.T) {
	if got := getPlaceholderCompletions(types.NodeTypeCommand, ""); got != nil {
		t.Errorf("unknown-type completions = %v, want nil", got)
	}
}
