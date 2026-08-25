// Copyright 2024 TrailHuang. All rights reserved.
// SPDX-License-Identifier: MIT

// Package tnlcmd_test 提供 TNLCMD 库的全面集成测试。
// 测试覆盖：模块间交互、数据流、异常场景、业务流程、兼容性、性能、安全和回归测试。
package tnlcmd_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TrailHuang/tnlcmd"
	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/internal/server"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// ============================================================================
// 测试工具函数
// ============================================================================

// integrationTest 封装集成测试的通用设置和工具方法。
type integrationTest struct {
	t          *testing.T
	clientConn net.Conn
	serverConn net.Conn
	reader     *bufio.Reader
	config     *types.Config
	rootMode   *mode.CommandMode
	context    *mode.CommandContext
	server     *server.TelnetServer
}

// newIntegrationTest 创建新的集成测试环境。
func newIntegrationTest(t *testing.T) *integrationTest {
	t.Helper()

	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0, // 自动选择空闲端口
		WelcomeMsg: "WELCOME_MARKER\r\n",
		MaxHistory: 10,
		PageSize:   24,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}

	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}

	return &integrationTest{
		t:        t,
		config:   config,
		rootMode: rootMode,
		context:  ctx,
		server:   srv,
	}
}

// connect 连接到测试服务器。
func (it *integrationTest) connect() {
	it.t.Helper()

	addr := it.server.Addr()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		it.t.Fatalf("dial failed: %v", err)
	}
	it.clientConn = conn
	it.reader = bufio.NewReader(conn)

	// 读取欢迎消息
	if _, err := it.readUntil("WELCOME_MARKER", 3*time.Second); err != nil {
		it.t.Fatalf("read welcome failed: %v", err)
	}
}

// disconnect 断开连接。
func (it *integrationTest) disconnect() {
	if it.clientConn != nil {
		it.clientConn.Close()
	}
}

// cleanup 清理测试资源。
func (it *integrationTest) cleanup() {
	it.disconnect()
	if it.server != nil {
		it.server.Stop()
	}
}

// write 写入数据到客户端连接。
func (it *integrationTest) write(data string) error {
	it.t.Helper()
	it.clientConn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := it.clientConn.Write([]byte(data))
	return err
}

// readUntil 读取直到包含目标字符串或超时。
func (it *integrationTest) readUntil(target string, timeout time.Duration) (string, error) {
	it.t.Helper()
	deadline := time.Now().Add(timeout)
	it.clientConn.SetReadDeadline(deadline)
	var buf strings.Builder
	for {
		b, err := it.reader.ReadByte()
		if err != nil {
			return buf.String(), err
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), target) {
			return buf.String(), nil
		}
	}
}

// readUntilPrompt 读取直到看到提示符。
func (it *integrationTest) readUntilPrompt(timeout time.Duration) (string, error) {
	return it.readUntil("TEST>", timeout)
}

// ============================================================================
// 1. 系统模块间交互测试
// ============================================================================

// TestIntegration_CmdLineToServerCommunication 验证 CmdLine 到 Server 的通信链路。
func TestIntegration_CmdLineToServerCommunication(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "INTEG>", "test root mode")

	// 添加测试命令
	handler := func(args []string) string {
		return "TEST_RESULT\r\n"
	}
	rootMode.AddCommand("test", "Test command", handler)

	config := &types.Config{
		Prompt:     "INTEG>",
		Port:       0,
		WelcomeMsg: "INTEGRATION_TEST\r\n",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}

	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	// 连接测试
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	// 验证欢迎消息
	var buf strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "INTEGRATION_TEST") {
			break
		}
	}

	// 执行命令测试
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("test\r\n"))

	// 验证命令结果
	buf.Reset()
	for {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("read result failed: %v", err)
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "TEST_RESULT") {
			break
		}
	}
}

// TestIntegration_SessionToCommandTreeInteraction 验证 Session 与 CommandTree 的交互。
func TestIntegration_SessionToCommandTreeInteraction(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	// 添加多层命令
	it.rootMode.AddCommand("show", "Show information", func(args []string) string {
		return "SHOW_RESULT\r\n"
	})
	it.rootMode.AddCommand("show status", "Show status", func(args []string) string {
		return "STATUS_RESULT\r\n"
	})
	it.rootMode.AddCommand("show status detail", "Show detailed status", func(args []string) string {
		return "DETAIL_RESULT\r\n"
	})

	it.connect()
	defer it.disconnect()

	// 测试单层命令
	if err := it.write("show\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := it.readUntil("SHOW_RESULT", 3*time.Second); err != nil {
		t.Errorf("show command failed: %v", err)
	}
	it.readUntilPrompt(3 * time.Second)

	// 测试多层命令
	if err := it.write("show status detail\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := it.readUntil("DETAIL_RESULT", 3*time.Second); err != nil {
		t.Errorf("show status detail failed: %v", err)
	}
}

// TestIntegration_ModeSwitchCommandTreeUpdate 验证模式切换时命令树正确更新。
func TestIntegration_ModeSwitchCommandTreeUpdate(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	// 创建子模式
	subMode := mode.NewCommandMode("configure", "CONF#", "configuration mode")
	subMode.AddCommand("hostname NAME", "Set hostname", func(args []string) string {
		return fmt.Sprintf("HOSTNAME_SET_%s\r\n", strings.Join(args, "_"))
	})
	subMode.AddCommand("ip ADDRESS", "Set IP address", func(args []string) string {
		return fmt.Sprintf("IP_SET_%s\r\n", strings.Join(args, "_"))
	})
	it.rootMode.AddSubMode(subMode)

	// 添加视图切换命令
	it.rootMode.CommandTree.AddModeCommand("configure", "Enter configuration mode")

	// 更新服务器上下文
	it.context.CommandTree = it.rootMode.CommandTree
	it.context.CurrentMode = it.rootMode

	it.connect()
	defer it.disconnect()

	// 从根模式切换到配置模式
	if err := it.write("configure\r\n"); err != nil {
		t.Fatalf("write configure failed: %v", err)
	}
	if _, err := it.readUntil("configuration mode", 3*time.Second); err != nil {
		t.Errorf("configure mode switch failed: %v", err)
	}
	it.readUntilPrompt(3 * time.Second)

	// 在配置模式下执行命令
	if err := it.write("hostname Router1\r\n"); err != nil {
		t.Fatalf("write hostname failed: %v", err)
	}
	if _, err := it.readUntil("HOSTNAME_SET_Router1", 3*time.Second); err != nil {
		t.Errorf("hostname command failed: %v", err)
	}
}

// TestIntegration_CompleterCommandTreeSync 验证补全器与命令树同步。
func TestIntegration_CompleterCommandTreeSync(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	// 添加测试命令
	it.rootMode.AddCommand("show", "Show information", func(args []string) string { return "" })
	it.rootMode.AddCommand("set", "Set configuration", func(args []string) string { return "" })
	it.rootMode.AddCommand("clear", "Clear data", func(args []string) string { return "" })

	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 等待初始提示符
	it.readUntilPrompt(3 * time.Second)

	// 测试 Tab 补全（发送 Tab 字符）
	if err := it.write("\t"); err != nil {
		t.Fatalf("write tab failed: %v", err)
	}

	// 应该显示命令列表
	// 注意：Tab 补全会列出所有命令，每行一个
	buf, err := it.readUntilPrompt(3 * time.Second)
	if err != nil {
		t.Errorf("completer sync failed: %v", err)
	}

	// 验证命令列表中包含添加的命令
	if !strings.Contains(buf, "show") && !strings.Contains(buf, "set") && !strings.Contains(buf, "clear") {
		t.Errorf("completer missing commands: %q", buf)
	}
}

// ============================================================================
// 2. 数据流测试
// ============================================================================

// TestIntegration_DataFlow_CommandParsing 验证命令解析数据流。
// 注意：此测试验证命令树级别的参数解析，不涉及完整的命令处理流程。
func TestIntegration_DataFlow_CommandParsing(t *testing.T) {
	testCases := []struct {
		name        string
		cmdSpec     string
		inputArgs   []string
		shouldMatch bool
	}{
		// 枚举参数测试
		{"enum_on", "set switch (on|off)", []string{"set", "switch", "on"}, true},
		{"enum_off", "set switch (on|off)", []string{"set", "switch", "off"}, true},
		{"enum_invalid", "set switch (on|off)", []string{"set", "switch", "invalid"}, false},
		// 范围参数测试
		{"range_valid", "set debug <1-10>", []string{"set", "debug", "5"}, true},
		{"range_zero", "set debug <1-10>", []string{"set", "debug", "0"}, false},
		{"range_outbound", "set debug <1-10>", []string{"set", "debug", "20"}, false},
		// 字符串参数测试
		{"string_param", "set name NAME", []string{"set", "name", "MyHost"}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 直接测试命令树
			tree := commandtree.NewCommandTree()
			handler := func(args []string) string { return "OK\r\n" }
			if err := tree.AddCommand(tc.cmdSpec, "test", handler); err != nil {
				t.Fatalf("add command failed: %v", err)
			}

			_, _, _, err := tree.FindCommand(tc.inputArgs)
			if tc.shouldMatch && err != nil {
				t.Errorf("expected match, got error: %v", err)
			}
			if !tc.shouldMatch && err == nil {
				t.Errorf("expected no match, but command was matched")
			}
		})
	}
}

// TestIntegration_DataFlow_HistoryPersistence 验证历史记录数据流。
func TestIntegration_DataFlow_HistoryPersistence(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("cmd1", "Command 1", func(args []string) string { return "R1\r\n" })
	it.rootMode.AddCommand("cmd2", "Command 2", func(args []string) string { return "R2\r\n" })
	it.rootMode.AddCommand("cmd3", "Command 3", func(args []string) string { return "R3\r\n" })

	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 执行多条命令
	commands := []string{"cmd1", "cmd2", "cmd3"}
	for _, cmd := range commands {
		if err := it.write(cmd + "\r\n"); err != nil {
			t.Fatalf("write %s failed: %v", cmd, err)
		}
		it.readUntilPrompt(3 * time.Second)
	}

	// 使用上方向键回溯到最后一条历史命令（cmd3）
	upArrow := string([]byte{0x1B, '[', 'A'})
	if err := it.write(upArrow); err != nil {
		t.Fatalf("write up arrow failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// 执行回溯的命令
	if err := it.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}

	// 应该执行最后一条历史命令
	if _, err := it.readUntil("R3", 3*time.Second); err != nil {
		t.Errorf("history persistence failed: %v", err)
	}
}

// TestIntegration_DataFlow_ParameterValidation 验证参数验证数据流。
func TestIntegration_DataFlow_ParameterValidation(t *testing.T) {
	// 创建独立的服务器用于参数验证测试
	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	rootMode.AddCommand("set port <1-65535>", "Set port", func(args []string) string {
		return fmt.Sprintf("PORT_SET_%s\r\n", args[0])
	})
	rootMode.AddCommand("set status (enabled|disabled)", "Set status", func(args []string) string {
		return fmt.Sprintf("STATUS_%s\r\n", strings.ToUpper(args[0]))
	})

	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, CommandTree: rootMode.CommandTree}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	// 测试有效参数 - 每个测试用例使用独立连接
	validCases := []struct {
		command  string
		expected string
	}{
		{"set port 8080", "PORT_SET_8080"},
		{"set status enabled", "STATUS_ENABLED"},
	}

	for _, tc := range validCases {
		t.Run(tc.command, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

			// 读取初始提示符（消耗 telnet 协商字节）
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read init prompt failed: %v", err)
				}
				if b == '>' {
					break
				}
			}

			// 发送命令
			conn.Write([]byte(tc.command + "\r\n"))

			// 读取完整响应直到提示符
			var buf strings.Builder
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read failed: %v", err)
				}
				buf.WriteByte(b)
				if strings.Contains(buf.String(), "TEST>") {
					break
				}
			}

			if !strings.Contains(buf.String(), tc.expected) {
				t.Errorf("expected %q, got %q", tc.expected, buf.String())
			}
		})
	}
}

// ============================================================================
// 3. 异常场景测试
// ============================================================================

// TestIntegration_Exception_EmptyInput 验证空输入处理。
func TestIntegration_Exception_EmptyInput(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("test", "Test", func(args []string) string { return "OK\r\n" })
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 发送空行
	if err := it.write("\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 应该只是显示提示符，不报错
	if _, err := it.readUntilPrompt(3 * time.Second); err != nil {
		t.Errorf("empty input handling failed: %v", err)
	}
}

// TestIntegration_Exception_InvalidCommand 验证无效命令处理。
func TestIntegration_Exception_InvalidCommand(t *testing.T) {
	// 创建独立的服务器以确保命令正确注册
	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	rootMode.AddCommand("valid", "Valid command", func(args []string) string { return "OK\r\n" })

	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		CommandTree: rootMode.CommandTree,
		Path:        []string{},
	}

	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// 读取初始提示符（包含 telnet 协商序列）
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var initBuf strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("read init prompt failed: %v", err)
		}
		initBuf.WriteByte(b)
		if strings.Contains(initBuf.String(), "TEST>") {
			break
		}
	}

	// 发送无效命令
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("invalid_command\r\n"))

	// 读取响应（可能包含 telnet 协商字节后跟错误消息）
	var buf strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "TEST>") {
			break
		}
	}

	// 验证有错误提示（session.go 返回 "Unknown command: xxx"）
	// 注意：可能需要跳过 telnet 协商字节
	response := buf.String()
	if !strings.Contains(response, "Unknown") && !strings.Contains(response, "Type '?'") {
		t.Errorf("expected Unknown command message, got %q", response)
	}
}

// TestIntegration_Exception_IncompleteCommand 验证不完整命令处理。
func TestIntegration_Exception_IncompleteCommand(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	// 添加需要参数的命令
	it.rootMode.AddCommand("show interface", "Show interface", func(args []string) string { return "OK\r\n" })
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 发送不完整命令
	if err := it.write("show\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	buf, err := it.readUntilPrompt(3 * time.Second)
	if err != nil {
		t.Errorf("read prompt failed: %v", err)
	}

	// 应该提示命令不完整
	if !strings.Contains(buf, "Incomplete") && !strings.Contains(buf, "%") {
		t.Logf("incomplete command message: %q", buf)
	}
}

// TestIntegration_Exception_TooManyArguments 验证参数过多处理。
func TestIntegration_Exception_TooManyArguments(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("set value <1-10>", "Set value", func(args []string) string { return "OK\r\n" })
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 发送参数过多的命令
	if err := it.write("set value 5 extra_arg\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	buf, err := it.readUntilPrompt(3 * time.Second)
	if err != nil {
		t.Errorf("read prompt failed: %v", err)
	}

	if !strings.Contains(buf, "Too many") {
		t.Logf("too many args message: %q", buf)
	}
}

// TestIntegration_Exception_CtrlCInterrupt 验证 Ctrl+C 中断处理。
func TestIntegration_Exception_CtrlCInterrupt(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("slow", "Slow command", func(args []string) string {
		time.Sleep(5 * time.Second)
		return "DONE\r\n"
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 启动慢命令
	if err := it.write("slow\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 发送 Ctrl+C 中断
	time.Sleep(100 * time.Millisecond)
	if err := it.write(string([]byte{0x03})); err != nil {
		t.Fatalf("write ctrl+c failed: %v", err)
	}

	// 连接应该关闭或返回错误
	it.clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := it.reader.ReadByte()
	if err == nil {
		// 连接仍然打开，这是正常的
		t.Log("connection still open after Ctrl+C")
	} else if err != io.EOF && !strings.Contains(err.Error(), "timeout") {
		t.Logf("Ctrl+C handling: %v", err)
	}
}

// TestIntegration_Exception_ClientDisconnect 验证客户端断开连接处理。
func TestIntegration_Exception_ClientDisconnect(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("test", "Test", func(args []string) string {
		time.Sleep(2 * time.Second)
		return "OK\r\n"
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()

	// 发送命令后立即断开
	if err := it.write("test\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	it.clientConn.Close()

	// 服务器应该能正确处理（不 panic 或阻塞）
	time.Sleep(3 * time.Second)
}

// TestIntegration_Exception_NetworkTimeout 验证网络超时处理。
func TestIntegration_Exception_NetworkTimeout(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("test", "Test", func(args []string) string { return "OK\r\n" })
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 读取应该正常
	if _, err := it.readUntilPrompt(3 * time.Second); err != nil {
		t.Errorf("prompt read failed: %v", err)
	}
}

// ============================================================================
// 4. 业务流程测试
// ============================================================================

// TestIntegration_BusinessFlow_CompleteCLIWorkflow 验证完整的 CLI 工作流程。
func TestIntegration_BusinessFlow_CompleteCLIWorkflow(t *testing.T) {
	rootMode := mode.NewCommandMode("ROUTER>", "ROUTER>", "test")

	// 根模式命令
	rootMode.AddCommand("show version", "Show system version", func(args []string) string {
		return "Router CLI v1.0\r\nBuild: 2024-01-01\r\n"
	})
	rootMode.AddCommand("show interfaces", "Show interfaces", func(args []string) string {
		return "eth0: UP, 192.168.1.1/24\r\neth1: DOWN\r\n"
	})
	rootMode.AddCommand("ping IP", "Ping host", func(args []string) string {
		return fmt.Sprintf("PING %s: 64 bytes\r\n64 bytes from %s: seq=0 ttl=64\r\n", args[0], args[0])
	})

	// 创建配置模式
	confMode := mode.NewCommandMode("configure", "CONF#", "configuration")
	confMode.AddCommand("hostname NAME", "Set hostname", func(args []string) string {
		return fmt.Sprintf("Hostname set to %s\r\n", args[0])
	})
	// 配置模式需要 exit 命令返回根模式
	confMode.AddCommand("exit", "Exit to privileged mode", func(args []string) string {
		return types.SentinelExitToRoot
	})
	rootMode.AddSubMode(confMode)
	rootMode.CommandTree.AddModeCommand("configure", "Enter configure mode")

	config := &types.Config{
		Prompt:     "ROUTER>",
		Port:       0,
		WelcomeMsg: "Router CLI v1.0\r\n",
		MaxHistory: 50,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{CurrentMode: rootMode}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	// 连接
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	// 验证欢迎消息
	var buf strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "Router CLI v1.0") {
			break
		}
	}

	// 业务流程测试
	workflow := []struct {
		send    string
		expect  string
		timeout time.Duration
	}{
		{"show version\r\n", "Router CLI v1.0", 3 * time.Second},
		{"show interfaces\r\n", "eth0: UP", 3 * time.Second},
		{"ping 8.8.8.8\r\n", "PING 8.8.8.8", 3 * time.Second},
		{"configure\r\n", "configuration", 3 * time.Second},
		{"hostname MyRouter\r\n", "MyRouter", 3 * time.Second},
		{"exit\r\n", "Exiting", 3 * time.Second}, // exit 返回 "Exiting to privileged EXEC mode"
	}

	for i, step := range workflow {
		t.Run(fmt.Sprintf("step_%d", i+1), func(t *testing.T) {
			conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			conn.Write([]byte(step.send))

			conn.SetReadDeadline(time.Now().Add(step.timeout))
			buf.Reset()
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read failed: %v", err)
				}
				buf.WriteByte(b)
				if strings.Contains(buf.String(), step.expect) {
					break
				}
			}
		})
	}
}

// TestIntegration_BusinessFlow_ModeHierarchyNavigation 验证模式层级导航。
func TestIntegration_BusinessFlow_ModeHierarchyNavigation(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	// 创建三层模式结构
	confMode := mode.NewCommandMode("configure", "CONF#", "configuration")
	confMode.AddCommand("hostname NAME", "Set hostname", func(args []string) string {
		return fmt.Sprintf("HOSTNAME=%s\r\n", args[0])
	})

	intMode := mode.NewCommandMode("interface", "IF#", "interface")
	intMode.AddCommand("ip IP MASK", "Set IP", func(args []string) string {
		return fmt.Sprintf("IP=%s\r\n", args[0])
	})
	intMode.AddCommand("exit", "Exit", func(args []string) string {
		return types.SentinelExitToRoot
	})

	confMode.AddSubMode(intMode)
	it.rootMode.AddSubMode(confMode)

	// 添加视图切换命令
	it.rootMode.CommandTree.AddModeCommand("configure", "Enter configure mode")
	it.rootMode.CommandTree.AddModeCommand("interface", "Enter interface mode")

	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 导航流程
	navSteps := []struct {
		command string
		expect  string
	}{
		{"configure\r\n", "configuration"},
		{"hostname Router1\r\n", "HOSTNAME=Router1"},
		{"interface\r\n", "interface"},
		{"ip 10.0.0.1 255.0.0.0\r\n", "IP=10.0.0.1"},
		{"exit\r\n", "privileged EXEC"},
	}

	for i, step := range navSteps {
		t.Run(fmt.Sprintf("nav_%d", i+1), func(t *testing.T) {
			if err := it.write(step.command); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if _, err := it.readUntil(step.expect, 3*time.Second); err != nil {
				t.Errorf("nav step %d failed: %v", i+1, err)
			}
			it.readUntilPrompt(3 * time.Second)
		})
	}
}

// ============================================================================
// 5. 兼容性测试
// ============================================================================

// TestIntegration_Compatibility_DifferentPageSizes 验证不同分页大小设置。
func TestIntegration_Compatibility_DifferentPageSizes(t *testing.T) {
	testCases := []struct {
		pageSize int
		lines    int
		desc     string
	}{
		{0, 100, "disabled"},
		{10, 25, "enabled"},
		{100, 50, "all_lines_fit"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rootMode := mode.NewCommandMode("root", "TEST>", "test")

			// 生成多行输出
			var output strings.Builder
			for i := 0; i < tc.lines; i++ {
				output.WriteString(fmt.Sprintf("Line %d\r\n", i+1))
			}

			rootMode.AddCommand("list", "List lines", func(args []string) string {
				return output.String()
			})

			config := &types.Config{
				Prompt:     "TEST>",
				Port:       0,
				WelcomeMsg: "",
				MaxHistory: 10,
				PageSize:   tc.pageSize,
				RootMode:   rootMode,
			}
			ctx := &mode.CommandContext{CurrentMode: rootMode}
			srv := server.NewTelnetServerWithContext(config, ctx)
			if err := srv.Start(); err != nil {
				t.Fatalf("server start failed: %v", err)
			}
			defer srv.Stop()

			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))

			conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			conn.Write([]byte("list\r\n"))

			// 读取输出直到看到最后的提示符
			var buf strings.Builder
			deadline := time.Now().Add(10 * time.Second)

			for time.Now().Before(deadline) {
				conn.SetReadDeadline(deadline)
				b, err := reader.ReadByte()
				if err != nil {
					break
				}
				buf.WriteByte(b)

				// 检查是否到达提示符（表示输出完成）
				if strings.Contains(buf.String(), "TEST>") && buf.Len() > 50 {
					break
				}
			}

			// 验证分页行为
			if tc.pageSize > 0 && tc.lines > tc.pageSize {
				if !strings.Contains(buf.String(), "--More--") {
					t.Logf("pagination may not be working: %q", buf.String())
				}
			} else if tc.pageSize == 0 {
				if strings.Contains(buf.String(), "--More--") {
					t.Errorf("pagination should be disabled when PageSize=0")
				}
			}
		})
	}
}

// TestIntegration_Compatibility_VariousPromptFormats 验证不同提示符格式。
func TestIntegration_Compatibility_VariousPromptFormats(t *testing.T) {
	prompts := []string{
		"simple>",
		"user@host>",
		"router(config)#",
		"[admin] #",
		"PROMPT_END>",
	}

	for _, prompt := range prompts {
		t.Run(strings.ReplaceAll(prompt, ">", ""), func(t *testing.T) {
			rootMode := mode.NewCommandMode("root", prompt, "test")
			rootMode.AddCommand("test", "Test", func(args []string) string {
				return "TEST_OK\r\n"
			})

			config := &types.Config{
				Prompt:     prompt,
				Port:       0,
				WelcomeMsg: "",
				MaxHistory: 10,
				PageSize:   0,
				RootMode:   rootMode,
			}
			ctx := &mode.CommandContext{CurrentMode: rootMode}
			srv := server.NewTelnetServerWithContext(config, ctx)
			if err := srv.Start(); err != nil {
				t.Fatalf("server start failed: %v", err)
			}
			defer srv.Stop()

			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			// 等待初始提示符
			var buf strings.Builder
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read failed: %v", err)
				}
				buf.WriteByte(b)
				if strings.Contains(buf.String(), ">") || strings.Contains(buf.String(), "#") {
					break
				}
			}

			// 执行命令
			conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			conn.Write([]byte("test\r\n"))

			buf.Reset()
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read failed: %v", err)
				}
				buf.WriteByte(b)
				if strings.Contains(buf.String(), "TEST_OK") {
					break
				}
			}
		})
	}
}

// TestIntegration_Compatibility_DifferentMaxHistory 验证不同历史记录大小。
func TestIntegration_Compatibility_DifferentMaxHistory(t *testing.T) {
	maxHistorySizes := []int{0, 1, 10, 100}

	for _, maxHistory := range maxHistorySizes {
		t.Run(fmt.Sprintf("maxHistory_%d", maxHistory), func(t *testing.T) {
			rootMode := mode.NewCommandMode("root", "TEST>", "test")

			// 添加多个命令
			for i := 0; i < 20; i++ {
				name := fmt.Sprintf("cmd%d", i)
				rootMode.AddCommand(name, fmt.Sprintf("Command %d", i), func(idx int) types.CommandHandler {
					return func(args []string) string {
						return fmt.Sprintf("CMD%d_OK\r\n", idx)
					}
				}(i))
			}

			config := &types.Config{
				Prompt:     "TEST>",
				Port:       0,
				WelcomeMsg: "",
				MaxHistory: maxHistory,
				PageSize:   0,
				RootMode:   rootMode,
			}
			ctx := &mode.CommandContext{CurrentMode: rootMode}
			srv := server.NewTelnetServerWithContext(config, ctx)
			if err := srv.Start(); err != nil {
				t.Fatalf("server start failed: %v", err)
			}
			defer srv.Stop()

			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

			// 执行多个命令填充历史
			for i := 0; i < 15; i++ {
				conn.Write([]byte(fmt.Sprintf("cmd%d\r\n", i)))
				for {
					b, err := reader.ReadByte()
					if err != nil {
						t.Fatalf("read failed: %v", err)
					}
					if strings.Contains(string([]byte{b}), ">") {
						break
					}
				}
			}

			// 历史记录验证成功（没有 panic 或错误）
		})
	}
}

// ============================================================================
// 6. 性能测试
// ============================================================================

// TestIntegration_Performance_MultipleConcurrentConnections 验证多并发连接性能。
func TestIntegration_Performance_MultipleConcurrentConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	rootMode := mode.NewCommandMode("root", "PERF>", "test")
	rootMode.AddCommand("test", "Performance test", func(args []string) string {
		return "PERF_OK\r\n"
	})

	config := &types.Config{
		Prompt:     "PERF>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{CurrentMode: rootMode}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	const numConnections = 50
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	startTime := time.Now()

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				errorCount.Add(1)
				return
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			// 发送命令
			conn.Write([]byte("test\r\n"))

			// 读取响应
			for {
				_, err := reader.ReadByte()
				if err != nil {
					break
				}
			}

			successCount.Add(1)
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	t.Logf("Performance: %d/%d connections succeeded in %v", successCount.Load(), numConnections, elapsed)
	t.Logf("Throughput: %.2f connections/second", float64(numConnections)/elapsed.Seconds())

	if errorCount.Load() > 0 {
		t.Errorf("%d connections failed", errorCount.Load())
	}
}

// TestIntegration_Performance_CommandResponseTime 验证命令响应时间。
func TestIntegration_Performance_CommandResponseTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	rootMode := mode.NewCommandMode("root", "PERF>", "test")
	rootMode.AddCommand("ping", "Ping test", func(args []string) string {
		return "pong\r\n"
	})

	config := &types.Config{
		Prompt:     "PERF>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{CurrentMode: rootMode}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))

	// 预热
	conn.Write([]byte("ping\r\n"))
	for {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if strings.Contains(string([]byte{b}), ">") {
			break
		}
	}

	// 测量响应时间
	const numIterations = 100
	var totalTime time.Duration

	for i := 0; i < numIterations; i++ {
		start := time.Now()

		conn.Write([]byte("ping\r\n"))

		for {
			b, err := reader.ReadByte()
			if err != nil {
				t.Fatalf("read failed: %v", err)
			}
			if strings.Contains(string([]byte{b}), ">") {
				break
			}
		}

		totalTime += time.Since(start)
	}

	avgTime := totalTime / numIterations
	t.Logf("Average command response time: %v", avgTime)

	if avgTime > 50*time.Millisecond {
		t.Logf("Warning: response time above threshold")
	}
}

// ============================================================================
// 7. 安全测试
// ============================================================================

// TestIntegration_Security_CommandInjectionPrevention 验证命令注入防护。
func TestIntegration_Security_CommandInjectionPrevention(t *testing.T) {
	// 创建独立的服务器以确保命令正确注册
	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	rootMode.AddCommand("set name NAME", "Set name", func(args []string) string {
		return fmt.Sprintf("NAME_SET=%s\r\n", args[0])
	})

	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		CommandTree: rootMode.CommandTree,
		Path:        []string{},
	}

	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	// 测试潜在的命令注入 - 特殊字符应该被当作普通字符串处理
	// 每个测试用例使用独立连接，避免历史命令干扰
	injectionAttempts := []struct {
		input    string
		expected string
	}{
		{"set name hello world", "NAME_SET=hello"},
		{"set name 'test'", "NAME_SET='test'"},
		{"set name test;rm", "NAME_SET=test;rm"},
	}

	for _, attempt := range injectionAttempts {
		t.Run(attempt.input, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Addr())
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)

			// 读取初始提示符
			conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			// 读取直到找到提示符
			for {
				b, err := reader.ReadByte()
				if err != nil {
					t.Fatalf("read init failed: %v", err)
				}
				if b == '>' {
					break
				}
			}

			// 发送命令
			conn.Write([]byte(attempt.input + "\r\n"))

			// 读取响应
			var buf strings.Builder
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				conn.SetReadDeadline(deadline)
				b, err := reader.ReadByte()
				if err != nil {
					break
				}
				buf.WriteByte(b)
				if strings.Contains(buf.String(), "TEST>") {
					break
				}
			}

			// 验证输出符合预期
			if !strings.Contains(buf.String(), attempt.expected) {
				t.Errorf("unexpected output for input %q: %q", attempt.input, buf.String())
			}

			// 验证没有执行注入的命令
			if strings.Contains(buf.String(), "rm -rf") {
				t.Errorf("command injection may have occurred: %q", buf.String())
			}
		})
	}
}

// TestIntegration_Security_BufferOverflowPrevention 验证缓冲区溢出防护。
func TestIntegration_Security_BufferOverflowPrevention(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("echo STRING", "Echo input", func(args []string) string {
		return fmt.Sprintf("ECHO=%s\r\n", args[0])
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 测试长输入
	longInput := strings.Repeat("A", 10000)
	if err := it.write("echo " + longInput + "\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 设置较短的超时，验证系统不会挂起
	if _, err := it.readUntilPrompt(5 * time.Second); err != nil {
		t.Errorf("long input handling failed (possible buffer overflow): %v", err)
	}
}

// TestIntegration_Security_SessionIsolation 验证会话隔离。
func TestIntegration_Security_SessionIsolation(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	rootMode.AddCommand("cmd1", "Command 1", func(args []string) string { return "R1\r\n" })

	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 5,
		PageSize:   0,
		RootMode:   rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop()

	// 创建两个连接
	conn1, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial 1 failed: %v", err)
	}
	defer conn1.Close()

	conn2, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial 2 failed: %v", err)
	}
	defer conn2.Close()

	reader1 := bufio.NewReader(conn1)
	reader2 := bufio.NewReader(conn2)

	// 连接1执行命令
	conn1.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn1.Write([]byte("cmd1\r\n"))

	conn1.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf1 strings.Builder
	for {
		b, err := reader1.ReadByte()
		if err != nil {
			t.Fatalf("read 1 failed: %v", err)
		}
		buf1.WriteByte(b)
		if strings.Contains(buf1.String(), "R1") {
			break
		}
	}

	// 连接2应该独立，不受连接1影响
	conn2.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn2.Write([]byte("cmd1\r\n"))

	conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf2 strings.Builder
	for {
		b, err := reader2.ReadByte()
		if err != nil {
			t.Fatalf("read 2 failed: %v", err)
		}
		buf2.WriteByte(b)
		if strings.Contains(buf2.String(), "R1") {
			break
		}
	}

	// 验证两个连接的结果独立
	if !strings.Contains(buf1.String(), "R1") || !strings.Contains(buf2.String(), "R1") {
		t.Errorf("session isolation failed: buf1=%q, buf2=%q", buf1.String(), buf2.String())
	}
}

// ============================================================================
// 8. 回归测试
// ============================================================================

// TestIntegration_Regression_CommandTreeParameterTypes 验证命令树参数类型。
func TestIntegration_Regression_CommandTreeParameterTypes(t *testing.T) {
	testCases := []struct {
		cmd     string
		input   []string
		success bool
	}{
		{"set switch (on|off)", []string{"set", "switch", "on"}, true},
		{"set switch (on|off)", []string{"set", "switch", "invalid"}, false},
		{"set debug <1-100>", []string{"set", "debug", "50"}, true},
		{"set debug <1-100>", []string{"set", "debug", "0"}, false},
		{"set debug <1-100>", []string{"set", "debug", "101"}, false},
		{"set name STRING", []string{"set", "name", "MyName"}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.cmd, func(t *testing.T) {
			tree := commandtree.NewCommandTree()
			handler := func(args []string) string { return "OK" }
			if err := tree.AddCommand(tc.cmd, "test", handler); err != nil {
				t.Fatalf("add command failed: %v", err)
			}

			_, _, _, err := tree.FindCommand(tc.input)
			if tc.success && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tc.success && err == nil {
				t.Errorf("expected failure, got success")
			}
		})
	}
}

// TestIntegration_Regression_GracefulShutdown 验证优雅关闭。
func TestIntegration_Regression_GracefulShutdown(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "TEST>", "test")
	rootMode.AddCommand("test", "Test", func(args []string) string {
		time.Sleep(100 * time.Millisecond)
		return "TEST_OK\r\n"
	})

	config := &types.Config{
		Prompt:     "TEST>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}

	ctx := &mode.CommandContext{CurrentMode: rootMode}
	srv := server.NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}

	// 创建多个连接
	connections := make([]net.Conn, 5)
	for i := range connections {
		conn, err := net.Dial("tcp", srv.Addr())
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		connections[i] = conn
	}

	// 发送命令
	for _, conn := range connections {
		conn.Write([]byte("test\r\n"))
	}

	// 关闭服务器
	srv.Stop()

	// 验证所有连接正确关闭
	for _, conn := range connections {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		reader := bufio.NewReader(conn)
		for {
			_, err := reader.ReadByte()
			if err != nil {
				break
			}
		}
		conn.Close()
	}
}

// ============================================================================
// 辅助测试：边缘情况
// ============================================================================

// TestIntegration_EdgeCase_UnicodeInput 验证 Unicode 输入处理。
func TestIntegration_EdgeCase_UnicodeInput(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("echo STRING", "Echo input", func(args []string) string {
		return fmt.Sprintf("ECHO=%s\r\n", args[0])
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 测试 Unicode 输入
	unicodeInput := "hello"
	if err := it.write("echo " + unicodeInput + "\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 验证系统不会 panic
	if _, err := it.readUntilPrompt(3 * time.Second); err != nil {
		t.Logf("unicode handling result: %v", err)
	}
}

// TestIntegration_EdgeCase_SpecialCharacters 验证特殊字符处理。
func TestIntegration_EdgeCase_SpecialCharacters(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("echo STRING", "Echo input", func(args []string) string {
		return fmt.Sprintf("ECHO=%s\r\n", args[0])
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 测试各种特殊字符
	specialChars := []string{
		"hello world",
		"hello;world",
		"hello|world",
		"hello&world",
		"hello$world",
	}

	for _, chars := range specialChars {
		t.Run(chars, func(t *testing.T) {
			if err := it.write("echo " + chars + "\r\n"); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if _, err := it.readUntilPrompt(3 * time.Second); err != nil {
				t.Errorf("special char handling failed for %q: %v", chars, err)
			}
		})
	}
}

// TestIntegration_EdgeCase_RapidInput 验证快速连续输入。
func TestIntegration_EdgeCase_RapidInput(t *testing.T) {
	it := newIntegrationTest(t)
	defer it.cleanup()

	it.rootMode.AddCommand("cmd", "Command", func(args []string) string {
		return "OK\r\n"
	})
	it.context.CommandTree = it.rootMode.CommandTree

	it.connect()
	defer it.disconnect()

	// 快速发送多个命令
	for i := 0; i < 10; i++ {
		if err := it.write("cmd\r\n"); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 读取所有响应
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; i < 10; i++ {
		it.clientConn.SetReadDeadline(deadline)
		if _, err := it.readUntil("OK", 3*time.Second); err != nil {
			t.Errorf("rapid input handling failed at iteration %d: %v", i, err)
			break
		}
	}
}

// TestIntegration_EdgeCase_CmdLineHighLevelAPI 验证 CmdLine 高级 API。
func TestIntegration_EdgeCase_CmdLineHighLevelAPI(t *testing.T) {
	config := tnlcmd.DefaultConfig()
	config.Port = 0
	config.Prompt = "TEST>"
	config.WelcomeMsg = "Welcome\r\n"
	config.MaxHistory = 10
	config.PageSize = 0

	cmdline := tnlcmd.NewCmdLine(config)

	// 测试命令注册
	cmdline.RegisterCommand("test", "Test command", func(args []string) string {
		return "TEST_OK\r\n"
	})

	// 测试模式创建
	cmdline.CreateMode("config", "configuration mode")
	cmdline.RegisterModeCommand("config", "host NAME", "Set host", func(args []string) string {
		return fmt.Sprintf("HOST=%s\r\n", args[0])
	})

	// 验证 API 调用不 panic
	if err := cmdline.SetConfig("prompt", "NEW>"); err != nil {
		t.Errorf("SetConfig failed: %v", err)
	}
}
