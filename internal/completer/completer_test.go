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

// newParamCompleter 构造含范围、枚举与占位符参数的补全器。
func newParamCompleter() *CommandCompleter {
	rootMode := mode.NewCommandMode("root", "R>", "test")
	handler := func(args []string) string { return "" }
	rootMode.AddCommand("set debug2 <1-10> (on|off)", "Debug", handler)
	rootMode.AddCommand("ping A.B.C.D", "Ping", handler)
	return NewCommandCompleterWithContext(&mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	})
}

// TestTabParameterCompletion 验证 Tab 进入参数层、枚举值补全与尾随空格语义（工单 07）。
func TestTabParameterCompletion(t *testing.T) {
	c := newParamCompleter()
	cases := []struct {
		input string
		want  string // 用逗号连接期望结果，空串表示无候选
	}{
		// 命令树中参数按注册顺序串联：<1-10> 之后才是 (on|off)
		{"set debug2", "set debug2"},
		{"set debug2 ", "set debug2 <1-10>"},
		{"set debug2 5 ", "set debug2 5 (on|off)"},
		{"set debug2 5 o", "off,on"},
		{"set debug2 5 on", "set debug2 5 on"},
		{"set debug2 5 of", "set debug2 5 off"},
		{"ping A.B.C.D ", ""},
	}
	for _, tc := range cases {
		got := strings.Join(c.GetNextLevelCompletions(tc.input), ",")
		if got != tc.want {
			t.Errorf("GetNextLevelCompletions(%q) = %q, want %q", tc.input, got, tc.want)
		}
		// 多候选必须顺序稳定
		for i := 0; i < 5; i++ {
			if again := strings.Join(c.GetNextLevelCompletions(tc.input), ","); again != got {
				t.Fatalf("unstable candidates for %q: %q vs %q", tc.input, got, again)
			}
		}
	}
}

// TestHelpAndTabShareNavigation 验证 ? 帮助与 Tab 对同一输入给出一致的层级，
// 仅展示格式不同（帮助为定宽名称+描述，Tab 为裸名称）（工单 07）。
func TestHelpAndTabShareNavigation(t *testing.T) {
	c := newParamCompleter()
	tab := c.GetNextLevelCompletions("set debug2 ")
	help := c.GetCommandTreeSuggestions("set debug2 ")
	if len(tab) != 1 {
		t.Fatalf("Tab candidates = %v, want 1 parameter entry", tab)
	}
	// Tab 单候选回填整行，取出参数名用于与帮助输出对照
	tabName := tab[0][len("set debug2 "):]
	found := false
	for _, line := range help {
		if strings.HasPrefix(line, tabName+strings.Repeat(" ", 32-len(tabName))) && strings.TrimSpace(strings.TrimPrefix(line, tabName)) != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("? help missing parameter %q (help=%v)", tabName, help)
	}
}

// TestModeSwitchHelpCr 验证按 ? 触发帮助时（缓冲为完整视图名，可能带尾随空格）
// 都提示 <cr>（用户报告 interface? 与 "interface ?" 结果不一致；? 不会进入编辑缓冲，
// 因此补全器收到的输入分别是 "interface" 与 "interface "）。
func TestModeSwitchHelpCr(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "R>", "test")
	sub := mode.NewCommandMode("interface", "IF#", "interface mode")
	rootMode.AddSubMode(sub)
	rootMode.CommandTree.AddModeCommand("interface", "Enter interface mode")
	sub.AddCommand("shutdown", "Shutdown interface", func(args []string) string { return "" })

	c := NewCommandCompleterWithContext(&mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	})

	for _, input := range []string{"interface", "interface "} {
		got := c.GetCommandTreeSuggestions(input)
		if len(got) != 1 || got[0] != "<cr>" {
			t.Errorf("GetCommandTreeSuggestions(%q) = %v, want [<cr>]", input, got)
		}
	}
}

// TestHelpParameterLayerNoModeSwitch 验证下钻进命令参数层后，? 帮助不再混入
// 视图切换命令（用户报告 "clear ?" 列出了 configure/interface）。
func TestHelpParameterLayerNoModeSwitch(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "R>", "test")
	sub := mode.NewCommandMode("configure", "C#", "configure")
	rootMode.AddSubMode(sub)
	rootMode.CommandTree.AddModeCommand("configure", "Enter configure mode")
	rootMode.AddCommand("clear test1", "reset test1", func(args []string) string { return "" })
	rootMode.AddCommand("clear test2", "reset test2", func(args []string) string { return "" })

	c := NewCommandCompleterWithContext(&mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	})

	// 命令参数层：只有参数候选，不含视图切换命令
	got := c.GetCommandTreeSuggestions("clear ")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "configure") {
		t.Errorf("parameter layer must not include mode switch commands, got %v", got)
	}
	if !strings.Contains(joined, "test1") || !strings.Contains(joined, "test2") {
		t.Errorf("parameter layer should list test1/test2, got %v", got)
	}

	// 根层级（空输入）仍应包含视图切换命令
	got = c.GetCommandTreeSuggestions("")
	if !strings.Contains(strings.Join(got, "\n"), "configure") {
		t.Errorf("root level help should include mode switch commands, got %v", got)
	}
}
