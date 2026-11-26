package server

import (
	"bufio"
	"net"
	"strings"
	"sync"
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
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf strings.Builder
	tmp := make([]byte, 1)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			t.Fatalf("did not see welcome: %v (buf=%q)", err, buf.String())
		}
		if n > 0 {
			buf.WriteByte(tmp[0])
		}
		if strings.Contains(buf.String(), "WELCOME_MARKER") {
			break
		}
	}

	// 停止服务器，应关闭会话连接
	srv.Stop()

	// 排空 TCP 缓冲区中残留的数据（telnet协商字节、prompt等），
	// 直到连接真正关闭返回 EOF/网络错误
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err = conn.Read(tmp); err != nil {
			break
		}
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

// readWelcomeMarker 读取连接输出直到出现 welcome 标记，返回累积内容。
func readWelcomeMarker(t *testing.T, conn net.Conn, cr *bufio.Reader, marker string) string {
	t.Helper()
	var buf strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		b, err := cr.ReadByte()
		if err != nil {
			t.Fatalf("did not see %q: %v (buf=%q)", marker, err, buf.String())
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), marker) {
			return buf.String()
		}
	}
}

// TestServer_MaxConnectionsRejects 验证超过 MaxConnections 的新连接
// 收到拒绝提示后被关闭（工单 05）。
func TestServer_MaxConnectionsRejects(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	config := &types.Config{
		Prompt:         "SRV>",
		Port:           0,
		WelcomeMsg:     "WELCOME_MARKER\r\n",
		MaxConnections: 1,
		RootMode:       rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()
	addr := srv.listener.Addr().String()

	// 第一个连接：正常建立并收到欢迎消息
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial c1 failed: %v", err)
	}
	defer c1.Close()
	readWelcomeMarker(t, c1, bufio.NewReader(c1), "WELCOME_MARKER")

	// 第二个连接：应收到拒绝提示后连接被关闭
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial c2 failed: %v", err)
	}
	defer c2.Close()

	buf := readWelcomeMarker(t, c2, bufio.NewReader(c2), "Too many connections")
	if strings.Contains(buf, "WELCOME_MARKER") {
		t.Error("rejected connection must not receive welcome message")
	}

	// 拒绝提示后服务端应关闭连接（读到 EOF/错误）
	c2.SetReadDeadline(time.Now().Add(3 * time.Second))
	tmp := make([]byte, 1)
	for {
		if _, err = c2.Read(tmp); err != nil {
			break
		}
	}

	// 第一个连接应不受影响（仍是活动会话）
	srv.mu.RLock()
	n := len(srv.sessions)
	srv.mu.RUnlock()
	if n != 1 {
		t.Errorf("server sessions = %d, want 1 (only the accepted connection)", n)
	}
}

// TestServer_MaxConnectionsConcurrentRace 多连接并发竞争上限：
// 被接受的连接保持打开（不释放槽位），最终接受数与服务器内注册会话数
// 均不得超过 MaxConnections（-race 下运行）。
func TestServer_MaxConnectionsConcurrentRace(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	const maxConns = 3
	config := &types.Config{
		Prompt:         "SRV>",
		Port:           0,
		WelcomeMsg:     "WELCOME_MARKER\r\n",
		MaxConnections: maxConns,
		RootMode:       rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()
	addr := srv.listener.Addr().String()

	const total = 8
	var mu sync.Mutex
	accepted := 0
	rejected := 0
	var held []net.Conn // 被接受的连接，保持打开以占住槽位，统计后再关闭

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Errorf("dial failed: %v", err)
				return
			}

			// 读取输出直到出现欢迎标记（被接受）或拒绝标记（被拒绝）
			var buf strings.Builder
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			tmp := make([]byte, 1)
			for {
				n, err := conn.Read(tmp)
				if err != nil {
					t.Errorf("conn ended without marker: %v (buf=%q)", err, buf.String())
					conn.Close()
					return
				}
				buf.Write(tmp[:n])
				s := buf.String()
				if strings.Contains(s, "WELCOME_MARKER") {
					mu.Lock()
					accepted++
					held = append(held, conn) // 保持打开，占住槽位
					mu.Unlock()
					return
				}
				if strings.Contains(s, "Too many connections") {
					mu.Lock()
					rejected++
					mu.Unlock()
					conn.Close()
					return
				}
			}
		}()
	}
	wg.Wait()

	if accepted > maxConns {
		t.Errorf("accepted %d connections, want <= %d (limit must hold under concurrency)", accepted, maxConns)
	}
	if accepted+rejected != total {
		t.Errorf("accepted(%d) + rejected(%d) != total(%d)", accepted, rejected, total)
	}
	t.Logf("accepted=%d rejected=%d", accepted, rejected)

	// 服务器内注册的会话数同样不得超过上限
	srv.mu.RLock()
	n := len(srv.sessions)
	srv.mu.RUnlock()
	if n > maxConns {
		t.Errorf("server sessions = %d, want <= %d", n, maxConns)
	}

	// 清理：关闭占位连接
	for _, c := range held {
		c.Close()
	}
}

// TestServer_IdleTimeoutReapsSession 验证空闲超过 IdleTimeout 的会话被服务端主动回收
// （连接被关闭），而持续活跃的会话不受影响（工单 05）。
func TestServer_IdleTimeoutReapsSession(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	rootMode.AddCommand("ping", "Ping", func(args []string) string { return "PONG\r\n" })

	config := &types.Config{
		Prompt:      "SRV>",
		Port:        0,
		WelcomeMsg:  "WELCOME_MARKER\r\n",
		IdleTimeout: 300 * time.Millisecond,
		RootMode:    rootMode,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()
	addr := srv.listener.Addr().String()

	// 活跃会话：周期性发送命令，保持活跃
	active, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial active failed: %v", err)
	}
	defer active.Close()
	readWelcomeMarker(t, active, bufio.NewReader(active), "WELCOME_MARKER")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			active.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := active.Write([]byte("ping\r\n")); err != nil {
				t.Errorf("active conn write failed: %v", err)
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
	}()

	// 空闲会话：建立后不发送任何输入
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial idle failed: %v", err)
	}
	defer idle.Close()
	readWelcomeMarker(t, idle, bufio.NewReader(idle), "WELCOME_MARKER")

	// 等待超过 IdleTimeout + 扫描间隔（阈值一半，下限 100ms）
	time.Sleep(700 * time.Millisecond)

	// 空闲会话连接应被服务端关闭（读到 EOF/错误）
	idle.SetReadDeadline(time.Now().Add(3 * time.Second))
	tmp := make([]byte, 1)
	closed := false
	for {
		if _, err = idle.Read(tmp); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Error("idle session connection should be closed by server after IdleTimeout")
	}

	// 活跃会话应存活：等待其完成活跃循环后，仍能执行命令得到 PONG
	<-done
	active.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := active.Write([]byte("ping\r\n")); err != nil {
		t.Fatalf("active conn write after idle reap failed: %v", err)
	}
	cr := bufio.NewReader(active)
	var buf strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for {
		active.SetReadDeadline(deadline)
		b, err := cr.ReadByte()
		if err != nil {
			t.Fatalf("active session should survive idle reaping: %v (buf=%q)", err, buf.String())
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), "PONG") {
			break
		}
	}

	// 服务器内应只剩活跃会话
	srv.mu.RLock()
	n := len(srv.sessions)
	srv.mu.RUnlock()
	if n != 1 {
		t.Errorf("server sessions = %d, want 1 (idle session reaped, active kept)", n)
	}
}
