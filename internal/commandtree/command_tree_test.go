package commandtree

import (
	"strings"
	"testing"
)

func TestNewCommandTree(t *testing.T) {
	tree := NewCommandTree()
	if tree == nil {
		t.Fatal("NewCommandTree returned nil")
	}
	if tree.Root == nil {
		t.Fatal("tree.Root is nil")
	}
	if tree.Root.Name != "root" {
		t.Errorf("root name = %q, want %q", tree.Root.Name, "root")
	}
	if tree.ModeCommands == nil {
		t.Fatal("tree.ModeCommands is nil")
	}
}

func TestAddSimpleCommand(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "ok" }
	err := tree.AddCommand("show", "Show information", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	node, path, args, err := tree.FindCommand([]string{"show"})
	if err != nil {
		t.Fatalf("FindCommand failed: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if len(path) != 1 || path[0] != "show" {
		t.Errorf("path = %v, want [show]", path)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want []", args)
	}
	if node.Handler == nil {
		t.Fatal("handler is nil")
	}
	if node.Handler(nil, nil) != "ok" {
		t.Errorf("handler returned unexpected value")
	}
}

func TestAddMultiWordCommand(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "running-config" }
	err := tree.AddCommand("show running-config", "Show running config", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	node, path, args, err := tree.FindCommand([]string{"show", "running-config"})
	if err != nil {
		t.Fatalf("FindCommand failed: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if len(path) != 2 {
		t.Errorf("path length = %d, want 2", len(path))
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want []", args)
	}
}

func TestEnumParameter(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string {
		return strings.Join(args, ",")
	}
	err := tree.AddCommand("set switch (on|off)", "Set switch state", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	// Valid enum value
	node, path, args, err := tree.FindCommand([]string{"set", "switch", "on"})
	if err != nil {
		t.Fatalf("FindCommand with 'on' failed: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	_ = path
	if len(args) != 1 || args[0] != "on" {
		t.Errorf("args = %v, want [on]", args)
	}

	// Another valid enum value
	_, _, args, err = tree.FindCommand([]string{"set", "switch", "off"})
	if err != nil {
		t.Errorf("FindCommand with 'off' should succeed: %v", err)
	}
	if len(args) != 1 || args[0] != "off" {
		t.Errorf("args = %v, want [off]", args)
	}
}

func TestRangeParameter(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "ok" }
	err := tree.AddCommand("set debug <1-10>", "Set debug level", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	// Valid range value
	_, _, args, err := tree.FindCommand([]string{"set", "debug", "5"})
	if err != nil {
		t.Errorf("FindCommand with '5' should succeed: %v", err)
	}
	if len(args) != 1 || args[0] != "5" {
		t.Errorf("args = %v, want [5]", args)
	}
}

func TestRangeParameterOutOfBounds(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "ok" }
	err := tree.AddCommand("set debug <1-10>", "Set debug level", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	// Out of range value should not match as range parameter
	_, _, _, err = tree.FindCommand([]string{"set", "debug", "20"})
	if err == nil {
		t.Error("FindCommand with out-of-range value should fail")
	}
}

func TestStringParameter(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "ok" }
	err := tree.AddCommand("set name STRING", "Set name", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	_, _, args, err := tree.FindCommand([]string{"set", "name", "myhost"})
	if err != nil {
		t.Errorf("FindCommand with string param should succeed: %v", err)
	}
	if len(args) != 1 || args[0] != "myhost" {
		t.Errorf("args = %v, want [myhost]", args)
	}
}

func TestOptionalParameter(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string {
		if len(args) > 0 {
			return args[0]
		}
		return "no-arg"
	}
	err := tree.AddCommand("set test [OPTIONAL]", "Set test", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	// With optional parameter — optional values are consumed but not captured as matched args
	_, _, args, err := tree.FindCommand([]string{"set", "test", "value"})
	if err != nil {
		t.Errorf("FindCommand with optional param should succeed: %v", err)
	}
	_ = args // current behavior: optional args are consumed but not returned

	// Without optional parameter
	_, _, args, err = tree.FindCommand([]string{"set", "test"})
	if err != nil {
		t.Errorf("FindCommand without optional param should succeed: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	tree := NewCommandTree()

	_, _, _, err := tree.FindCommand([]string{"nonexistent"})
	if err == nil {
		t.Error("FindCommand with unknown command should fail")
	}
}

func TestIncompleteCommand(t *testing.T) {
	tree := NewCommandTree()

	handler := func(args []string) string { return "ok" }
	err := tree.AddCommand("show running-config", "Show config", handler)
	if err != nil {
		t.Fatalf("AddCommand failed: %v", err)
	}

	// Only "show" without the rest
	_, _, _, err = tree.FindCommand([]string{"show"})
	if err == nil {
		t.Error("FindCommand with incomplete command should fail")
	}
}

func TestMultipleCommandsSharingPrefix(t *testing.T) {
	tree := NewCommandTree()

	h1 := func(args []string) string { return "rc" }
	h2 := func(args []string) string { return "sc" }

	tree.AddCommand("show running-config", "Show running config", h1)
	tree.AddCommand("show startup-config", "Show startup config", h2)

	// Both should be findable
	_, _, _, err := tree.FindCommand([]string{"show", "running-config"})
	if err != nil {
		t.Errorf("show running-config should be found: %v", err)
	}

	_, _, _, err = tree.FindCommand([]string{"show", "startup-config"})
	if err != nil {
		t.Errorf("show startup-config should be found: %v", err)
	}
}

func TestAddModeCommand(t *testing.T) {
	tree := NewCommandTree()

	err := tree.AddModeCommand("configure", "Enter configuration mode")
	if err != nil {
		t.Fatalf("AddModeCommand failed: %v", err)
	}

	// Should be findable via FindCommand (single arg)
	node, path, args, err := tree.FindCommand([]string{"configure"})
	if err != nil {
		t.Fatalf("FindCommand for mode switch failed: %v", err)
	}
	if node.Type != NodeTypeModeSwitch {
		t.Errorf("expected NodeTypeModeSwitch, got %v", node.Type)
	}
	if node.ModeName != "configure" {
		t.Errorf("expected ModeName 'configure', got %q", node.ModeName)
	}
	if len(path) != 1 {
		t.Errorf("path length = %d, want 1", len(path))
	}
	if len(args) != 0 {
		t.Errorf("args length = %d, want 0", len(args))
	}

	// Should be in ModeCommands
	if _, exists := tree.ModeCommands["configure"]; !exists {
		t.Error("configure should be in ModeCommands")
	}
}

func TestPrintTree(t *testing.T) {
	tree := NewCommandTree()

	tree.AddCommand("show running-config", "Show config", func(args []string) string { return "" })
	tree.AddCommand("show version", "Show version", func(args []string) string { return "" })

	output := tree.PrintTree()
	if output == "" {
		t.Error("PrintTree returned empty string")
	}
	// Should contain the command nodes
	if !strings.Contains(output, "show") {
		t.Error("tree output should contain 'show'")
	}
}

func TestIsAllUppercase(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"STRING", true},
		{"HOSTNAME", true},
		{"ABC", true},
		{"abc", false},
		{"Abc", false},
		{"STR1NG", false},
		{"", false},
		{"lowercase", false},
	}

	for _, tt := range tests {
		got := isAllUppercase(tt.input)
		if got != tt.expected {
			t.Errorf("isAllUppercase(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestIsParameterMatch(t *testing.T) {
	// Test enum match
	enumNode := NewCommandNode("(on|off)", NodeTypeEnum, "")
	enumNode.EnumValues = []string{"on", "off"}

	if !IsParameterMatch(enumNode, "on") {
		t.Error("IsParameterMatch for enum 'on' should be true")
	}
	if IsParameterMatch(enumNode, "invalid") {
		t.Error("IsParameterMatch for enum 'invalid' should be false")
	}

	// Test range match
	rangeNode := NewCommandNode("<1-10>", NodeTypeNum, "")
	rangeNode.RangeMin = 1
	rangeNode.RangeMax = 10

	if !IsParameterMatch(rangeNode, "5") {
		t.Error("IsParameterMatch for range '5' in 1-10 should be true")
	}
	if IsParameterMatch(rangeNode, "20") {
		t.Error("IsParameterMatch for range '20' in 1-10 should be false")
	}

	// Test string match — isString requires len > 0
	strNode := NewCommandNode("STRING", NodeTypeString, "")
	if !IsParameterMatch(strNode, "anything") {
		t.Error("IsParameterMatch for string should be true")
	}
	if IsParameterMatch(strNode, "") {
		t.Error("IsParameterMatch for empty string should be false (isString requires len>0)")
	}
}

func TestModeCommandsIsolation(t *testing.T) {
	// Verify ModeCommands is instance-level, not global
	tree1 := NewCommandTree()
	tree2 := NewCommandTree()

	tree1.AddModeCommand("mode1", "Mode 1")
	tree2.AddModeCommand("mode2", "Mode 2")

	// tree1 should have mode1 but not mode2
	if _, exists := tree1.ModeCommands["mode1"]; !exists {
		t.Error("tree1 should have mode1")
	}
	if _, exists := tree1.ModeCommands["mode2"]; exists {
		t.Error("tree1 should NOT have mode2")
	}

	// tree2 should have mode2 but not mode1
	if _, exists := tree2.ModeCommands["mode2"]; !exists {
		t.Error("tree2 should have mode2")
	}
	if _, exists := tree2.ModeCommands["mode1"]; exists {
		t.Error("tree2 should NOT have mode1")
	}
}

// TestAddCommandRejectsInvalidSyntax 验证非法参数语法在注册期返回错误（工单 06）。
func TestAddCommandRejectsInvalidSyntax(t *testing.T) {
	for _, command := range []string{
		"bad <1-",
		"bad (on",
		"bad [OPT",
		"bad <abc>",
		"bad (|)",
		"bad value)",
	} {
		tree := NewCommandTree()
		if err := tree.AddCommand(command, "bad", func(args []string) string { return "" }); err == nil {
			t.Errorf("AddCommand(%q) should return an error", command)
		}
	}
}

// TestSpacedEnumParsing 验证带空格的枚举写法被解析为规范化枚举节点（工单 06）。
func TestSpacedEnumParsing(t *testing.T) {
	tree := NewCommandTree()
	if err := tree.AddCommand("set sw (on | off)", "Set switch", func(args []string) string { return "" }); err != nil {
		t.Fatalf("AddCommand: %v", err)
	}
	node, _, _, err := tree.FindCommand([]string{"set", "sw", "on"})
	if err != nil || node == nil {
		t.Fatalf("FindCommand(on) failed: node=%v err=%v", node, err)
	}
	if node.Name != "(on|off)" {
		t.Errorf("enum node name = %q, want %q", node.Name, "(on|off)")
	}
	if got := strings.Join(node.EnumValues, ","); got != "on,off" {
		t.Errorf("EnumValues = %q, want %q", got, "on,off")
	}
	if _, _, _, err := tree.FindCommand([]string{"set", "sw", "bad"}); err == nil {
		t.Error("invalid enum value should not match")
	}
}

// TestFindCommandReturnsUnmatchedArgs 验证多余参数随查找结果一并返回，
// 交由会话层做数量校验（工单 06）。
func TestFindCommandReturnsUnmatchedArgs(t *testing.T) {
	tree := NewCommandTree()
	if err := tree.AddCommand("set value <1-10>", "Set value", func(args []string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	_, _, args, err := tree.FindCommand([]string{"set", "value", "5", "extra"})
	if err != nil {
		t.Fatalf("FindCommand: %v", err)
	}
	if got := strings.Join(args, ","); got != "5,extra" {
		t.Errorf("args = %q, want %q", got, "5,extra")
	}
}
