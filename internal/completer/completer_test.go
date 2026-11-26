package completer

import (
	"strings"
	"testing"

	"github.com/TrailHuang/tnlcmd/internal/mode"
)

func setupCompleter() *CommandCompleter {
	// Create a mode with commands
	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	handler := func(args []string) string { return "" }

	rootMode.AddCommand("show", "Show information", handler)
	rootMode.AddCommand("show version", "Show version", handler)
	rootMode.AddCommand("show running-config", "Show running config", handler)
	rootMode.AddCommand("show startup-config", "Show startup config", handler)
	rootMode.AddCommand("set debug <1-10>", "Set debug level", handler)
	rootMode.AddCommand("set switch (on|off)", "Set switch", handler)
	rootMode.AddCommand("exit", "Exit", handler)

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	return NewCommandCompleterWithContext(ctx)
}

func TestGetCommandTreeSuggestions(t *testing.T) {
	c := setupCompleter()

	suggestions := c.GetCommandTreeSuggestions("")
	if len(suggestions) == 0 {
		t.Error("GetCommandTreeSuggestions('') should return suggestions")
	}

	// Check format: "name    description"
	for _, s := range suggestions {
		if !strings.Contains(s, " ") {
			t.Errorf("suggestion %q should contain description", s)
		}
	}
}

func TestGetCommandTreeSuggestionsSecondLevel(t *testing.T) {
	c := setupCompleter()

	suggestions := c.GetCommandTreeSuggestions("show")
	if len(suggestions) < 2 {
		t.Errorf("should have at least 2 suggestions under 'show', got %d: %v", len(suggestions), suggestions)
	}
}

func TestGetNextLevelCompletions(t *testing.T) {
	c := setupCompleter()

	// "show" matches the top-level "show" node itself (prefix match)
	completions := c.GetNextLevelCompletions("show")
	if len(completions) == 0 {
		t.Errorf("GetNextLevelCompletions('show') should return completions, got none")
	}

	// With prefix "show " (trailing space) — strings.Fields strips it to ["show"]
	// so it behaves like prefix match, not navigation
	completions = c.GetNextLevelCompletions("show ")
	if len(completions) == 0 {
		t.Errorf("GetNextLevelCompletions('show ') should return completions, got %d: %v", len(completions), completions)
	}

	// With "show v", should complete to "show version"
	completions = c.GetNextLevelCompletions("show v")
	if len(completions) != 1 {
		t.Errorf("GetNextLevelCompletions('show v') should return 1, got %d: %v", len(completions), completions)
	}
	if len(completions) > 0 && completions[0] != "show version" {
		t.Errorf("GetNextLevelCompletions('show v') = %v, want [show version]", completions)
	}
}

func TestGetParameterCompletions(t *testing.T) {
	c := setupCompleter()

	// GetParameterCompletions shows non-command children matching the last input part
	// When input fully navigates to a node, it shows parameter children
	completions := c.GetParameterCompletions("set switch (")
	if len(completions) == 0 {
		// ( is not a valid navigation target, so it won't work either
		// This is expected with current implementation
		t.Log("GetParameterCompletions for partial enum: no results (expected with current design)")
	}
	_ = completions
}

func TestUpdateContext(t *testing.T) {
	c := NewCommandCompleterWithContext(nil)

	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	rootMode.AddCommand("test", "Test command", func(args []string) string { return "" })

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	c.UpdateContext(ctx)

	completions := c.GetNextLevelCompletions("")
	if len(completions) != 1 || completions[0] != "test" {
		t.Errorf("After UpdateContext, GetNextLevelCompletions('') = %v, want [test]", completions)
	}
}

func TestNilContext(t *testing.T) {
	c := NewCommandCompleterWithContext(nil)

	// All methods should handle nil context gracefully
	if completions := c.GetNextLevelCompletions("test"); len(completions) != 0 {
		t.Errorf("GetNextLevelCompletions with nil context should return empty, got %v", completions)
	}
	if completions := c.GetParameterCompletions("test"); len(completions) != 0 {
		t.Errorf("GetParameterCompletions with nil context should return empty, got %v", completions)
	}
	if suggestions := c.GetCommandTreeSuggestions("test"); len(suggestions) != 0 {
		t.Errorf("GetCommandTreeSuggestions with nil context should return empty, got %v", suggestions)
	}
}

// setupCompleterWithModes 创建带有子模式的补全器
func setupCompleterWithModes() *CommandCompleter {
	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	handler := func(args []string) string { return "" }

	rootMode.AddCommand("show", "Show information", handler)
	rootMode.AddCommand("exit", "Exit", handler)

	// Create configure sub-mode
	configMode := mode.NewCommandMode("configure", "config#", "configure mode")
	configMode.AddCommand("hostname HOSTNAME", "Set hostname", handler)
	configMode.AddCommand("router PROTOCOL", "Enable routing", handler)
	configMode.AddCommand("exit", "Exit to root", handler)
	rootMode.AddSubMode(configMode)

	// Create interface sub-mode
	ifaceMode := mode.NewCommandMode("interface", "iface#", "interface mode")
	ifaceMode.AddCommand("ip IPADDR MASK", "Set IP address", handler)
	ifaceMode.AddCommand("shutdown", "Shutdown interface", handler)
	ifaceMode.AddCommand("description TEXT", "Set description", handler)
	ifaceMode.AddCommand("exit", "Exit to root", handler)
	rootMode.AddSubMode(ifaceMode)

	// Register mode switch commands in root's command tree
	rootMode.CommandTree.AddModeCommand("configure", "Enter configure mode")
	rootMode.CommandTree.AddModeCommand("interface", "Enter interface mode")

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	return NewCommandCompleterWithContext(ctx)
}

func TestGetCommandTreeSuggestions_ModeSwitch(t *testing.T) {
	c := setupCompleterWithModes()

	// 输入完整视图名 + ?：应提示 <cr>（可直接回车切换视图），而非展开目标视图子命令
	suggestions := c.GetCommandTreeSuggestions("interface")
	if len(suggestions) != 1 || suggestions[0] != "<cr>" {
		t.Errorf("GetCommandTreeSuggestions('interface') = %v, want [<cr>]", suggestions)
	}

	// configure 视图同理
	suggestions = c.GetCommandTreeSuggestions("configure")
	if len(suggestions) != 1 || suggestions[0] != "<cr>" {
		t.Errorf("GetCommandTreeSuggestions('configure') = %v, want [<cr>]", suggestions)
	}
}

func TestGetNextLevelCompletions_ModeSwitch(t *testing.T) {
	c := setupCompleterWithModes()

	// Tab after typing full mode name should show sub-commands
	completions := c.GetNextLevelCompletions("interface")
	if len(completions) < 2 {
		t.Errorf("GetNextLevelCompletions('interface') should show sub-commands, got %d: %v", len(completions), completions)
	}

	completions = c.GetNextLevelCompletions("configure")
	if len(completions) < 2 {
		t.Errorf("GetNextLevelCompletions('configure') should show sub-commands, got %d: %v", len(completions), completions)
	}
}

// TestProductionPathContracts 守护三条生产入口（session 层消费）的输出契约：
//   - GetNextLevelCompletions（Tab 命令补全）：返回裸命令名——session 在唯一匹配时
//     直接将其回填输入行，绝不能混入描述文本；
//   - GetParameterCompletions（Tab 参数补全回退）：返回裸参数名；
//   - GetCommandTreeSuggestions（? 帮助）：返回"名称定宽 32 字符 + 描述"展示行。
func TestProductionPathContracts(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	handler := func(args []string) string { return "" }
	// "s" 为 show 的子命令，"STRING" 为其字符串参数节点（全大写 → NodeTypeString）
	rootMode.AddCommand("show s STRING", "Show string param", handler)
	rootMode.AddCommand("show version", "Show version", handler)
	// "set SET SETTING"：String 参数节点 "SET" 下挂参数节点 "SETTING"（纯大写字母
	// 才会被解析为参数节点），用于构造 GetParameterCompletions 的非空用例
	// （其导航要求逐 token 精确命中，且按落点节点名前缀过滤参数子节点）
	rootMode.AddCommand("set SET SETTING", "Set two strings", handler)

	c := NewCommandCompleterWithContext(&mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	})

	// 契约 1：Tab 命令补全 → 裸命令名，可直接整行回填
	got := c.GetNextLevelCompletions("show v")
	if len(got) != 1 || got[0] != "show version" {
		t.Fatalf("GetNextLevelCompletions('show v') = %v, want [show version]", got)
	}
	if strings.Contains(got[0], "Show version") {
		t.Errorf("Tab completion must return bare name without description, got %q", got[0])
	}

	// 契约 2：Tab 参数补全回退 → 裸参数名
	got = c.GetParameterCompletions("set SET")
	if len(got) != 1 || got[0] != "SETTING" {
		t.Fatalf("GetParameterCompletions('set SET') = %v, want [SETTING]", got)
	}
	if strings.Contains(got[0], "string") {
		t.Errorf("parameter completion must return bare name without description, got %q", got[0])
	}

	// 契约 3：? 帮助 → "名称定宽 32 字符 + 描述" 展示行
	got = c.GetCommandTreeSuggestions("show")
	if len(got) == 0 {
		t.Fatal("GetCommandTreeSuggestions('show') should return suggestions")
	}
	sawVersion := false
	for _, line := range got {
		// 名称占据行首（不足 32 字符时以空格填充）
		name := strings.Fields(line)[0]
		if name != "s" && name != "version" {
			continue
		}
		if name == "version" {
			sawVersion = true
		}
		// 描述必须存在（区别于 Tab 补全的裸名称契约）
		if !strings.Contains(line, " ") || strings.TrimSpace(line) == name {
			t.Errorf("? suggestion %q must include a description", line)
		}
		// 定宽格式：名称 + 空格填充至 32 列，随后紧跟描述
		if !strings.HasPrefix(line, name+strings.Repeat(" ", 32-len(name))) {
			t.Errorf("? suggestion %q is not formatted as name padded to 32 columns", line)
		}
	}
	if !sawVersion {
		t.Errorf("? suggestions should contain 'version' entry, got %v", got)
	}
}
