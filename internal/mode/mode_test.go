package mode

import (
	"strings"
	"testing"
)

// TestNewCommandMode_PromptFormatting 验证根模式与子模式的提示符自动规范化。
func TestNewCommandMode_PromptFormatting(t *testing.T) {
	// 根模式：不以 > 或 # 结尾，应追加 "> "
	root := NewCommandMode("root", "cmdline", "root mode")
	if root.Prompt != "cmdline> " {
		t.Errorf("root prompt = %q, want %q", root.Prompt, "cmdline> ")
	}

	// 子模式：不以 > 或 # 结尾，应追加 "# "
	sub := NewCommandMode("configure", "config", "configure mode")
	if sub.Prompt != "config# " {
		t.Errorf("sub prompt = %q, want %q", sub.Prompt, "config# ")
	}

	// 已带 > 结尾：保持原样
	root2 := NewCommandMode("root", "router>", "router")
	if root2.Prompt != "router>" {
		t.Errorf("prompt with > suffix = %q, want %q", root2.Prompt, "router>")
	}

	// 已带 # 结尾：保持原样
	sub2 := NewCommandMode("iface", "if0#", "iface")
	if sub2.Prompt != "if0#" {
		t.Errorf("prompt with # suffix = %q, want %q", sub2.Prompt, "if0#")
	}
}

// TestSetPrompt 验证运行时修改提示符同样遵循规范化规则。
func TestSetPrompt(t *testing.T) {
	m := NewCommandMode("root", "old", "root")
	m.SetPrompt("new")
	if m.Prompt != "new> " {
		t.Errorf("SetPrompt root = %q, want %q", m.Prompt, "new> ")
	}

	sub := NewCommandMode("configure", "c", "configure")
	sub.SetPrompt("cfg")
	if sub.Prompt != "cfg# " {
		t.Errorf("SetPrompt sub = %q, want %q", sub.Prompt, "cfg# ")
	}
}

// TestAddCommand_UpdatesBothStorages 验证 AddCommand 同时写入平面 Commands 与独立 CommandTree。
func TestAddCommand_UpdatesBothStorages(t *testing.T) {
	m := NewCommandMode("root", "t>", "test")
	handler := func(args []string) string { return "ok" }
	m.AddCommand("show version", "Show version", handler)

	// 平面存储
	info, ok := m.Commands["show version"]
	if !ok {
		t.Fatal("command not found in flat Commands map")
	}
	if info.Description != "Show version" {
		t.Errorf("description = %q, want %q", info.Description, "Show version")
	}

	// 命令树存储
	node, _, _, err := m.CommandTree.FindCommand([]string{"show", "version"})
	if err != nil || node == nil {
		t.Fatalf("FindCommand in tree failed: %v", err)
	}
	if node.Handler == nil {
		t.Error("tree node handler is nil")
	}
}

// TestAddSubMode 验证父子关系建立。
func TestAddSubMode(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	sub := NewCommandMode("configure", "c#", "configure")
	root.AddSubMode(sub)

	if sub.Parent != root {
		t.Error("sub.Parent not set to root")
	}
	if root.Children["configure"] != sub {
		t.Error("sub not added to root.Children")
	}
}

// TestChangeMode_UpdatesPath 验证切换模式后路径链被正确重建。
func TestChangeMode_UpdatesPath(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	sub := NewCommandMode("configure", "c#", "configure")
	root.AddSubMode(sub)

	ctx := &CommandContext{CurrentMode: root}
	ctx.ChangeMode(sub)

	// 期望路径为 [root, configure]
	wantPath := []string{"root", "configure"}
	if len(ctx.Path) != len(wantPath) {
		t.Fatalf("path len = %d, want %d (%v)", len(ctx.Path), len(wantPath), ctx.Path)
	}
	for i, p := range wantPath {
		if ctx.Path[i] != p {
			t.Errorf("path[%d] = %q, want %q", i, ctx.Path[i], p)
		}
	}
	if ctx.CurrentMode != sub {
		t.Error("CurrentMode not switched to sub")
	}
}

// TestGetRootMode 验证从任意层级回溯到根模式。
func TestGetRootMode(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	sub := NewCommandMode("configure", "c#", "configure")
	subsub := NewCommandMode("sub", "s#", "sub")
	root.AddSubMode(sub)
	sub.AddSubMode(subsub)

	ctx := &CommandContext{CurrentMode: subsub}
	if ctx.GetRootMode() != root {
		t.Error("GetRootMode did not return the root from a nested sub-mode")
	}

	ctx.CurrentMode = root
	if ctx.GetRootMode() != root {
		t.Error("GetRootMode did not return root when already at root")
	}
}

// TestEnterMode_UnifiedPath 验证 EnterMode 是模式切换语义的唯一实现：
// ChangeMode 时机与提示消息格式一致，且切换 handler 与树节点分支共用它。
func TestEnterMode_UnifiedPath(t *testing.T) {
	root := NewCommandMode("root", "r>", "root mode")
	sub := NewCommandMode("configure", "c#", "configuration")
	root.AddSubMode(sub)

	ctx := &CommandContext{CurrentMode: root, Path: []string{"root"}}

	// 直接调用 EnterMode：应切换模式并返回标准提示消息
	msg := ctx.EnterMode(sub)
	if ctx.CurrentMode != sub {
		t.Error("EnterMode did not switch CurrentMode")
	}
	if len(ctx.Path) != 2 || ctx.Path[0] != "root" || ctx.Path[1] != "configure" {
		t.Errorf("EnterMode path = %v, want [root configure]", ctx.Path)
	}
	if msg != "Entering configuration mode\r\n" {
		t.Errorf("EnterMode msg = %q, want %q", msg, "Entering configuration mode\r\n")
	}

	// GetAvailableCommands 生成的切换 handler 应委托同一实现
	ctx.ChangeMode(root)
	handler := ctx.createModeChangeHandler(sub)
	if got := handler(nil); got != "Entering configuration mode\r\n" {
		t.Errorf("mode switch handler msg = %q, want %q", got, "Entering configuration mode\r\n")
	}
	if ctx.CurrentMode != sub {
		t.Error("mode switch handler did not switch CurrentMode")
	}
}

// TestGetAvailableCommands_IncludesModeSwitch 验证可用命令包含视图切换命令。
func TestGetAvailableCommands_IncludesModeSwitch(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	root.AddCommand("show", "Show", func(args []string) string { return "" })
	sub := NewCommandMode("configure", "c#", "configure")
	root.AddSubMode(sub)

	ctx := &CommandContext{CurrentMode: root}
	cmds := ctx.GetAvailableCommands()

	// 应包含根模式自身命令
	if _, ok := cmds["show"]; !ok {
		t.Error("available commands should include 'show'")
	}
	// 应包含 configure 切换命令
	info, ok := cmds["configure"]
	if !ok {
		t.Fatal("available commands should include 'configure' mode switch")
	}
	if info.Handler == nil {
		t.Error("mode switch handler is nil")
	}

	// 切换到子模式后，不应再包含自身切换命令
	ctx.ChangeMode(sub)
	cmds = ctx.GetAvailableCommands()
	if _, ok := cmds["configure"]; ok {
		t.Error("configure should not be listed when already in configure mode")
	}
}

// TestClone_DeepCopiesPath 验证 Clone 深拷贝 Path 切片，修改原始 Path 不影响副本。
func TestClone_DeepCopiesPath(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	sub := NewCommandMode("configure", "c#", "configure")
	root.AddSubMode(sub)

	ctx := &CommandContext{
		CurrentMode: sub,
		Path:        []string{"root", "configure"},
	}
	clone := ctx.Clone()

	// 修改原始 Path
	ctx.Path[0] = "MUTATED"
	ctx.Path = append(ctx.Path, "extra")

	// 副本应保持原值
	if clone.Path[0] != "root" {
		t.Errorf("clone Path[0] = %q, want %q (Path should be deep-copied)", clone.Path[0], "root")
	}
	if len(clone.Path) != 2 {
		t.Errorf("clone Path len = %d, want 2 (should not share underlying array)", len(clone.Path))
	}

	// CurrentMode 保持共享指针（设计如此：模式对象本身在 Start 后只读）
	if clone.CurrentMode != sub {
		t.Error("clone CurrentMode should share the same mode pointer")
	}
}

// TestClone_NilSafety 验证 Clone 对 nil 接收者安全。
func TestClone_NilSafety(t *testing.T) {
	var nilCtx *CommandContext
	if c := nilCtx.Clone(); c != nil {
		t.Error("Clone of nil context should return nil")
	}
}

// TestClone_RoundTrip 验证克隆后通过 ChangeMode 切换不影响原始上下文。
func TestClone_RoundTrip(t *testing.T) {
	root := NewCommandMode("root", "r>", "root")
	sub := NewCommandMode("configure", "c#", "configure")
	root.AddSubMode(sub)

	original := &CommandContext{CurrentMode: root, Path: []string{"root"}}
	clone := original.Clone()

	// 在克隆上切换模式
	clone.ChangeMode(sub)

	// 原始上下文应仍停留在 root
	if original.CurrentMode != root {
		t.Error("original CurrentMode was mutated by clone's ChangeMode")
	}
	if !strings.EqualFold(strings.Join(original.Path, ","), "root") {
		t.Errorf("original Path was mutated: %v", original.Path)
	}
	// 克隆应在 sub
	if clone.CurrentMode != sub {
		t.Error("clone CurrentMode should be sub after ChangeMode")
	}
}
