package cmdline

import (
	"strings"
	"testing"
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
