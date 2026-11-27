package cmdline

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
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

// TestSetConfig_AuthKeys 验证 SetConfig 支持 password 与 maxauthattempts 两个认证 key。
// 回归背景：与 maxconnections 相同，新字段若漏接 SetConfig，示例程序用 "_ =" 吞掉
// unknown key 错误后配置会静默失效。
func TestSetConfig_AuthKeys(t *testing.T) {
	c := NewCmdLine(nil)

	if err := c.SetConfig("password", "secret"); err != nil {
		t.Fatalf("SetConfig(password) failed: %v", err)
	}
	if c.config.Password != "secret" {
		t.Errorf("Password = %q, want %q", c.config.Password, "secret")
	}

	// 置空应关闭认证
	if err := c.SetConfig("password", ""); err != nil {
		t.Fatalf("SetConfig(password, empty) failed: %v", err)
	}
	if c.config.Password != "" {
		t.Errorf("Password = %q, want empty (auth disabled)", c.config.Password)
	}

	if err := c.SetConfig("maxauthattempts", "5"); err != nil {
		t.Fatalf("SetConfig(maxauthattempts) failed: %v", err)
	}
	if c.config.MaxAuthAttempts != 5 {
		t.Errorf("MaxAuthAttempts = %d, want 5", c.config.MaxAuthAttempts)
	}

	// 负数与非数字必须被拒绝
	if err := c.SetConfig("maxauthattempts", "-1"); err == nil {
		t.Error("SetConfig(maxauthattempts, -1) should fail")
	}
	if err := c.SetConfig("maxauthattempts", "abc"); err == nil {
		t.Error("SetConfig(maxauthattempts, abc) should fail")
	}
}

// TestSetConfig_AuditLogKey 验证 auditlog 开关的设置与非法值拒绝。
func TestSetConfig_AuditLogKey(t *testing.T) {
	c := NewCmdLine(nil)

	if err := c.SetConfig("auditlog", "true"); err != nil {
		t.Fatalf("SetConfig(auditlog) failed: %v", err)
	}
	if !c.config.AuditLog {
		t.Error("AuditLog = false, want true")
	}

	if err := c.SetConfig("auditlog", "false"); err != nil {
		t.Fatalf("SetConfig(auditlog, false) failed: %v", err)
	}
	if c.config.AuditLog {
		t.Error("AuditLog = true, want false")
	}

	// 非布尔值必须被拒绝
	if err := c.SetConfig("auditlog", "yes"); err == nil {
		t.Error("SetConfig(auditlog, yes) should fail")
	}
}

// TestSetConfig_AuditLogFileKey 验证 auditlogfile 路径的设置。
func TestSetConfig_AuditLogFileKey(t *testing.T) {
	c := NewCmdLine(nil)

	if err := c.SetConfig("auditlogfile", "/var/log/tnlcmd/audit.log"); err != nil {
		t.Fatalf("SetConfig(auditlogfile) failed: %v", err)
	}
	if c.config.AuditLogFile != "/var/log/tnlcmd/audit.log" {
		t.Errorf("AuditLogFile = %q, want /var/log/tnlcmd/audit.log", c.config.AuditLogFile)
	}
}

// TestAuditLogFileInitializedOnStart 验证 Start 后审计日志写入配置的文件而非 stderr，
// Stop 后 slog 默认 logger 还原（工单：审计日志落盘）。
func TestAuditLogFileInitializedOnStart(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	c := NewCmdLine(nil)
	c.config.Port = 0
	c.config.AuditLog = true
	c.config.AuditLogFile = logPath

	prev := slog.Default()
	defer slog.SetDefault(prev)

	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Start 后 slog 默认 logger 已切换为文件写入器：写入一条日志验证落盘
	if slog.Default() == prev {
		t.Fatal("slog default logger was not switched to audit file handler after Start")
	}
	slog.Info("audit", "event", "test_event", "command", "show version")
	_ = c.Stop()

	// Stop 后 logger 还原
	if slog.Default() != prev {
		t.Error("slog default logger was not restored after Stop")
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("audit log file %s not created: %v", logPath, err)
	}
	for _, key := range []string{"audit", "event=test_event", `command="show version"`} {
		if !strings.Contains(string(got), key) {
			t.Errorf("audit log file missing %q, got: %s", key, got)
		}
	}
}

// TestStartFailureReclaimsAuditResources 验证启动失败（端口被占用）后审计资源被完整回收：
// 写入器关闭、后台清理 goroutine 退出、全局 logger 还原，且服务可在端口释放后重新启动
// 并完成审计初始化（工单 01）。
func TestStartFailureReclaimsAuditResources(t *testing.T) {
	// 占用一个端口制造 listen 冲突
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port failed: %v", err)
	}
	port := blocker.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	c := NewCmdLine(nil)
	c.config.Port = port
	c.config.AuditLog = true
	c.config.AuditLogFile = logPath

	prev := slog.Default()
	defer slog.SetDefault(prev)

	if startErr := c.Start(); startErr == nil {
		t.Fatalf("Start should fail while port %d is occupied", port)
	}

	// 失败后资源必须已回收
	c.mu.Lock()
	leaked := c.auditWriter != nil
	c.mu.Unlock()
	if leaked {
		t.Error("audit writer leaked after failed Start")
	}
	if slog.Default() != prev {
		t.Error("slog default logger must be restored after failed Start")
	}

	// 释放端口后同一实例可重新启动，审计文件正常初始化
	if err := blocker.Close(); err != nil {
		t.Fatalf("release port failed: %v", err)
	}
	c.config.Port = 0
	if err := c.Start(); err != nil {
		t.Fatalf("Start after failed attempt should succeed: %v", err)
	}
	if slog.Default() == prev {
		t.Error("slog default logger should point to audit file after successful restart")
	}
	_ = c.Stop()
	if slog.Default() != prev {
		t.Error("slog default logger must be restored after Stop")
	}
}

// TestAuditLogFileNotInitializedWhenDisabled 验证 AuditLog 关闭时不创建文件、不切换 logger。
func TestAuditLogFileNotInitializedWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	c := NewCmdLine(nil)
	c.config.Port = 0
	c.config.AuditLog = false
	c.config.AuditLogFile = logPath

	prev := slog.Default()
	defer slog.SetDefault(prev)

	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	_ = c.Stop()

	if slog.Default() != prev {
		t.Error("slog default logger must not change when AuditLog is disabled")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("audit log file must not be created when AuditLog is disabled (err=%v)", err)
	}
}

// TestSetConfigConcurrentWithLiveSession 回归测试（工单 02）：并发 SetConfig 与
// 真实连接执行命令不得产生数据竞争。会话持有启动时的配置快照，SetConfig 只写模板，
// 该测试在 `-race` 下运行——若实现回退为共享同一 *Config 指针将稳定复现 DATA RACE
// （Session 写结果路径读 PageSize vs SetConfig 写 PageSize）。
func TestSetConfigConcurrentWithLiveSession(t *testing.T) {
	c := NewCmdLine(nil)
	c.config.Port = 0
	c.config.PageSize = 4
	if err := c.RegisterCommand("echo", "echo args", func(args []string) string {
		return fmt.Sprintf("got %d args: %v\r\n", len(args), args)
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Stop() }()

	conn, err := net.Dial("tcp", c.server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// 排空服务端输出，模拟真实客户端持续接收
	go func() {
		r := make([]byte, 1)
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Read(r); err != nil {
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3000; i++ {
			_ = c.SetConfig("pagesize", strconv.Itoa(4+i%3))
			_ = c.SetConfig("maxhistory", strconv.Itoa(100+i%5))
		}
	}()
	for i := 0; i < 200; i++ {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(conn, "echo\r\n"); err != nil {
			t.Fatalf("write echo failed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	<-done
}

// TestSetConfigAfterStartSnapshotSemantics 验证快照语义（工单 02）：
// Start 后 SetConfig 修改模板成功，但运行中的会话不受影响——
// 将 PageSize 调为 1 后执行多行输出命令，已建立的会话不出现分页提示。
func TestSetConfigAfterStartSnapshotSemantics(t *testing.T) {
	c := NewCmdLine(nil)
	c.config.Port = 0
	if err := c.RegisterCommand("three", "three lines", func(args []string) string {
		return "L1\r\nL2\r\nL3\r\n"
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Stop() }()

	addr := c.server.Addr()

	// 建立两个会话（一个在 SetConfig 之前、一个之后），都不应看到分页
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
		defer conn.Close()

		if i == 1 {
			// 会话建立后再修改模板配置：对该会话与前一会话均不应生效
			if err := c.SetConfig("pagesize", "1"); err != nil {
				t.Fatalf("SetConfig(pagesize) after Start should succeed: %v", err)
			}
			if c.config.PageSize != 1 {
				t.Errorf("template PageSize = %d, want 1", c.config.PageSize)
			}
		}
	}

	for j, conn := range conns {
		// 排空欢迎消息与提示符（读到第二个提示符出现即可视为就绪）
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		tmp := make([]byte, 1)
		var seen strings.Builder
		for {
			if _, err := conn.Read(tmp); err != nil {
				break
			}
			seen.WriteByte(tmp[0])
			// 等待欢迎消息出现，避免把欢迎内容误判为命令输出
			if strings.Contains(seen.String(), "Welcome to Command Line Interface") {
				break
			}
		}

		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(conn, "three\r\n"); err != nil {
			t.Fatalf("conn %d: write command failed: %v", j, err)
		}

		var out strings.Builder
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, err := conn.Read(tmp); err != nil {
				t.Fatalf("conn %d: did not see L3: %v (buf=%q)", j, err, out.String())
			}
			out.WriteByte(tmp[0])
			if strings.Contains(out.String(), "L3") {
				break
			}
		}
		if strings.Contains(out.String(), "--More--") {
			t.Errorf("conn %d: live session must not see pagination after SetConfig(pagesize=1), got %q", j, out.String())
		}
	}
}
