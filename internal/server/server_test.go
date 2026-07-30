package server

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// freeServer 在端口 0 上启动一个 TelnetServer，返回服务器实例与实际监听地址。
func freeServer(t *testing.T, welcome string, rootMode *mode.CommandMode) (*TelnetServer, string) {
	t.Helper()
	config := &types.Config{
		Prompt:     "SRV>",
		Port:       0,
		WelcomeMsg: welcome,
		MaxHistory: 10,
		PageSize:   0,
		RootMode:   rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	addr := srv.listener.Addr().String()
	return srv, addr
}

// TestServer_StartAcceptAndWelcome 验证服务器启动后能接受连接并发送欢迎消息。
func TestServer_StartAcceptAndWelcome(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	srv, addr := freeServer(t, "WELCOME_MARKER\r\n", rootMode)
	defer srv.Stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	cr := bufio.NewReader(conn)
	var buf strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		b, err := cr.ReadByte()
		if err != nil {
			t.Fatalf("did not see welcome message: %v (buf=%q)", err, buf.String())
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "WELCOME_MARKER") {
			return
		}
	}
}

// TestServer_StopClosesSessions 验证 Stop 关闭所有活动会话的连接。
func TestServer_StopClosesSessions(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	// 欢迎消息含标记，便于确认会话已注册
	srv, addr := freeServer(t, "WELCOME_MARKER\r\n", rootMode)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 读取欢迎消息，确保会话已建立并注册
	cr := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf strings.Builder
	for {
		b, err := cr.ReadByte()
		if err != nil {
			t.Fatalf("did not see welcome: %v (buf=%q)", err, buf.String())
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "WELCOME_MARKER") {
			break
		}
	}

	// 停止服务器，应关闭会话连接
	srv.Stop()

	// 客户端后续读取应收到 EOF 或错误（连接被关闭）
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = cr.ReadByte()
	if err == nil {
		t.Error("expected read error/EOF after server Stop, but read succeeded")
	}
}

// TestServer_PerSessionContextIsolation 验证每个连接获得独立的上下文副本
// （Clone 后模式切换互不影响）。
func TestServer_PerSessionContextIsolation(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	sub := mode.NewCommandMode("configure", "cfg#", "configure")
	rootMode.AddSubMode(sub)
	// 注册视图切换命令
	rootMode.CommandTree.AddModeCommand("configure", "configure mode")

	srv, addr := freeServer(t, "WELCOME_MARKER\r\n", rootMode)
	defer srv.Stop()

	// 两个连接
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial c1 failed: %v", err)
	}
	defer c1.Close()
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial c2 failed: %v", err)
	}
	defer c2.Close()

	// 各自读欢迎消息
	for _, c := range []net.Conn{c1, c2} {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		cr := bufio.NewReader(c)
		var b strings.Builder
		for {
			by, err := cr.ReadByte()
			if err != nil {
				t.Fatalf("read welcome failed: %v (buf=%q)", err, b.String())
			}
			b.WriteByte(by)
			if strings.Contains(b.String(), "WELCOME_MARKER") {
				break
			}
		}
	}

	// 服务器共享的根上下文 CurrentMode 应仍为 root（Clone 隔离）
	srv.mu.RLock()
	rootCurrent := srv.context.CurrentMode
	srv.mu.RUnlock()
	if rootCurrent != rootMode {
		t.Errorf("server root context CurrentMode was mutated by a session: got %v, want root", rootCurrent)
	}
}
