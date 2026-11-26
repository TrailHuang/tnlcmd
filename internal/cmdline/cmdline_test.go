package cmdline

import (
	"strings"
	"testing"
	"time"
)

// TestRegisterAfterStartRejected 验证 Start 后调用注册 API 返回错误（S2 修复的回归测试）。
// Start 后命令树被各会话 goroutine 并发只读遍历，注册会引发并发 map 读写 panic。
func TestRegisterAfterStartRejected(t *testing.T) {
	c := NewCmdLine(nil)

	// Start 前注册必须成功
	if err := c.RegisterCommand("pre", "Pre-start command", func(args []string) string { return "" }); err != nil {
		t.Fatalf("RegisterCommand before Start should succeed, got error: %v", err)
	}
	if err := c.CreateMode("config", "configuration mode"); err != nil {
		t.Fatalf("CreateMode before Start should succeed, got error: %v", err)
	}
	if err := c.RegisterModeCommand("config", "host NAME", "Set host", func(args []string) string { return "" }); err != nil {
		t.Fatalf("RegisterModeCommand before Start should succeed, got error: %v", err)
	}

	// 使用端口 0 启动（随机端口，避免冲突）
	c.config.Port = 0
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = c.Stop() }()

	// Start 后注册必须返回错误且命令不得写入树
	if err := c.RegisterCommand("post", "Post-start command", func(args []string) string { return "" }); err == nil {
		t.Error("RegisterCommand after Start should return an error")
	}
	if err := c.CreateMode("config2", "another mode"); err == nil {
		t.Error("CreateMode after Start should return an error")
	}
	if err := c.RegisterModeCommand("config", "banner TEXT", "Set banner", func(args []string) string { return "" }); err == nil {
		t.Error("RegisterModeCommand after Start should return an error")
	}

	// 确认未注册成功
	if _, exists := c.rootMode.Commands["post"]; exists {
		t.Error("command 'post' must not be registered after Start")
	}
	if _, exists := c.rootMode.Children["config2"]; exists {
		t.Error("mode 'config2' must not be created after Start")
	}
	if _, exists := c.rootMode.Children["config"].Commands["banner TEXT"]; exists {
		t.Error("mode command 'banner TEXT' must not be registered after Start")
	}
}

// TestBuiltinCommandsRegisteredAfterStart 验证 Start 后内置命令 exit/quit 已注册。
// 回归测试：isRunning 置位先于 registerBuiltinCommands 且后者走守卫路径时，
// 内置命令注册会静默失败，导致会话中 exit 显示 Unknown command 无法退出。
func TestBuiltinCommandsRegisteredAfterStart(t *testing.T) {
	c := NewCmdLine(nil)
	c.config.Port = 0
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = c.Stop() }()

	for _, name := range []string{"exit", "quit"} {
		node, _, _, err := c.rootMode.CommandTree.FindCommand([]string{name})
		if err != nil || node == nil || node.Handler == nil {
			t.Errorf("builtin command %q must be registered with a handler after Start, got node=%v err=%v", name, node, err)
		}
		if _, exists := c.rootMode.Commands[name]; !exists {
			t.Errorf("builtin command %q must exist in rootMode.Commands after Start", name)
		}
	}
}

// TestSetConfigAfterStart 验证 Start 后 SetConfig 仍然可用（非树结构修改）。
func TestSetConfigAfterStart(t *testing.T) {
	c := NewCmdLine(nil)
	c.config.Port = 0
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = c.Stop() }()

	if err := c.SetConfig("welcome", "New welcome\r\n"); err != nil {
		t.Errorf("SetConfig(welcome) after Start should succeed, got: %v", err)
	}
	if !strings.Contains(c.config.WelcomeMsg, "New welcome") {
		t.Errorf("WelcomeMsg not updated: %q", c.config.WelcomeMsg)
	}
	if err := c.SetConfig("pagesize", "10"); err != nil {
		t.Errorf("SetConfig(pagesize) after Start should succeed, got: %v", err)
	}
}

// TestSetConfig_SecurityKeys 验证 SetConfig 支持 maxconnections 与 idletimeout 两个安全 key。
// 回归背景：工单 05 新增字段后未接入 SetConfig，示例程序以 "_ =" 吞掉 unknown key 错误，
// 导致配置静默失效（maxconnections=1 时仍能建立多个 telnet 连接）。
func TestSetConfig_SecurityKeys(t *testing.T) {
	c := NewCmdLine(nil)

	if err := c.SetConfig("maxconnections", "3"); err != nil {
		t.Fatalf("SetConfig(maxconnections) failed: %v", err)
	}
	if c.config.MaxConnections != 3 {
		t.Errorf("MaxConnections = %d, want 3", c.config.MaxConnections)
	}

	// 负数与非数字必须被拒绝
	if err := c.SetConfig("maxconnections", "-1"); err == nil {
		t.Error("SetConfig(maxconnections, -1) should fail")
	}
	if err := c.SetConfig("maxconnections", "abc"); err == nil {
		t.Error("SetConfig(maxconnections, abc) should fail")
	}

	// idletimeout 接受 Go duration 语法
	if err := c.SetConfig("idletimeout", "5m"); err != nil {
		t.Fatalf("SetConfig(idletimeout, 5m) failed: %v", err)
	}
	if c.config.IdleTimeout != 5*time.Minute {
		t.Errorf("IdleTimeout = %v, want 5m", c.config.IdleTimeout)
	}
	if err := c.SetConfig("idletimeout", "30s"); err != nil {
		t.Fatalf("SetConfig(idletimeout, 30s) failed: %v", err)
	}
	if c.config.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v, want 30s", c.config.IdleTimeout)
	}

	// 纯数字（会被 ParseDuration 解释为纳秒级歧义写法）必须被拒绝
	if err := c.SetConfig("idletimeout", "5"); err == nil {
		t.Error("SetConfig(idletimeout, 5) should fail (ambiguous unit)")
	}
}
