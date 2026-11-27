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

// readUntilMarker 读取连接输出直到出现指定标记或超时，返回累积内容。
func readUntilMarker(t *testing.T, conn net.Conn, cr *bufio.Reader, marker string) string {
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

// TestServer_PasswordAuthSuccess 验证密码认证全流程（工单 06）：
// 先错一次（收到失败提示并再次提示），再输入正确密码后正常进入 CLI 并可执行命令。
func TestServer_PasswordAuthSuccess(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	rootMode.AddCommand("ping", "Ping", func(args []string) string { return "PONG\r\n" })

	config := &types.Config{
		Prompt:          "SRV>",
		Port:            0,
		WelcomeMsg:      "WELCOME_MARKER\r\n",
		Password:        "secret",
		MaxAuthAttempts: 3,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 应先收到认证提示而非欢迎消息
	first := readUntilMarker(t, conn, bufio.NewReader(conn), "Password: ")
	if strings.Contains(first, "WELCOME_MARKER") {
		t.Error("welcome message must not be sent before authentication succeeds")
	}

	// 第一次输错密码：收到失败提示后重新提示
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("wrong\r\n")); err != nil {
		t.Fatalf("write wrong password failed: %v", err)
	}
	second := readUntilMarker(t, conn, bufio.NewReader(conn), "Password: ")
	if !strings.Contains(second, "% Password incorrect") {
		t.Errorf("expected failure notice, got %q", second)
	}

	// 输入正确密码：进入 CLI，收到欢迎消息
	if _, err := conn.Write([]byte("secret\r\n")); err != nil {
		t.Fatalf("write correct password failed: %v", err)
	}
	readUntilMarker(t, conn, bufio.NewReader(conn), "WELCOME_MARKER")

	// CLI 可正常执行命令
	if _, err := conn.Write([]byte("ping\r\n")); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	readUntilMarker(t, conn, bufio.NewReader(conn), "PONG")
}

// TestServer_PasswordNotEchoed 验证认证阶段密码字符不回显（工单 06）。
// 服务端已协商 WILL ECHO（由服务端控制回显），密码输入不得出现在输出流中。
func TestServer_PasswordNotEchoed(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	config := &types.Config{
		Prompt:     "SRV>",
		Port:       0,
		WelcomeMsg: "WELCOME_MARKER\r\n",
		Password:   "topsecret",
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	readUntilMarker(t, conn, bufio.NewReader(conn), "Password: ")

	if _, err := conn.Write([]byte("topsecret\r\n")); err != nil {
		t.Fatalf("write password failed: %v", err)
	}
	out := readUntilMarker(t, conn, bufio.NewReader(conn), "WELCOME_MARKER")
	if strings.Contains(out, "topsecret") {
		t.Errorf("password characters must not be echoed, got %q", out)
	}
}

// TestServer_PasswordAuthMaxAttemptsExceeded 验证密码错误次数达到上限后
// 收到断开提示且连接被服务端关闭（工单 06）。
func TestServer_PasswordAuthMaxAttemptsExceeded(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	config := &types.Config{
		Prompt:          "SRV>",
		Port:            0,
		WelcomeMsg:      "WELCOME_MARKER\r\n",
		Password:        "secret",
		MaxAuthAttempts: 2,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	cr := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		readUntilMarker(t, conn, cr, "Password: ")
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err := conn.Write([]byte("badpass\r\n")); err != nil {
			t.Fatalf("write bad password #%d failed: %v", i+1, err)
		}
	}

	// 达到上限：收到断开提示
	readUntilMarker(t, conn, cr, "Too many failed authentication attempts")

	// 服务端应关闭连接（读到 EOF/错误）
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	tmp := make([]byte, 1)
	for {
		if _, err := conn.Read(tmp); err != nil {
			break
		}
	}
}

// TestServer_NoPasswordBackwardCompat 回归用例：未配置 Password 时连接行为与现状一致
// —— 不出现认证提示，直接收到欢迎消息并可执行命令（工单 06）。
func TestServer_NoPasswordBackwardCompat(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	rootMode.AddCommand("ping", "Ping", func(args []string) string { return "PONG\r\n" })

	srv, addr := freeServer(t, "WELCOME_MARKER\r\n", rootMode)
	defer srv.Stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	out := readUntilMarker(t, conn, bufio.NewReader(conn), "WELCOME_MARKER")
	if strings.Contains(out, "Password:") {
		t.Error("no auth prompt should appear when Password is not configured")
	}

	if _, err := conn.Write([]byte("ping\r\n")); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	readUntilMarker(t, conn, bufio.NewReader(conn), "PONG")
}

// TestServer_StopDuringAcceptStorm 验证连接风暴中立即 Stop 的正确性（工单 01）：
// wg 登记先于 goroutine 启动并受停止态守卫，因此 Stop 返回后每个已被 Accept 的
// 连接都会被关闭（读到 EOF/错误），服务器内不残留会话，且 -race 下无 WaitGroup 误用。
func TestServer_StopDuringAcceptStorm(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	srv, addr := freeServer(t, "WELCOME_MARKER\r\n", rootMode)

	const total = 20
	connsCh := make(chan net.Conn, total)
	var dialWG sync.WaitGroup
	for i := 0; i < total; i++ {
		dialWG.Add(1)
		go func() {
			defer dialWG.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Errorf("dial failed: %v", err)
				return
			}
			connsCh <- c
		}()
	}
	dialWG.Wait()

	// 所有拨号完成后立即停止：部分连接可能刚被 Accept、尚未完成会话注册
	srv.Stop()

	// Stop 返回后，所有已建立的连接都应被关闭
	close(connsCh)
	for c := range connsCh {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		tmp := make([]byte, 1)
		for {
			if _, err := c.Read(tmp); err != nil {
				break
			}
		}
		c.Close()
	}

	// 服务器内不应残留会话
	srv.mu.RLock()
	n := len(srv.sessions)
	srv.mu.RUnlock()
	if n != 0 {
		t.Errorf("server sessions = %d after Stop, want 0", n)
	}
}

// TestServer_UpdateIdleTimeoutRebuildsReaper 验证服务器运行期更新空闲阈值会重建
// 回收周期：以极长阈值启动的空闲会话在调小阈值后被回收（工单 03）。
func TestServer_UpdateIdleTimeoutRebuildsReaper(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "SRV>", "test")
	config := &types.Config{
		Prompt:      "SRV>",
		Port:        0,
		WelcomeMsg:  "WELCOME_MARKER\r\n",
		IdleTimeout: time.Hour,
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	srv := NewTelnetServerWithContext(config, ctx)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readWelcomeMarker(t, conn, bufio.NewReader(conn), "WELCOME_MARKER")

	srv.UpdateIdleTimeout(200 * time.Millisecond)

	// 读超时说明阈值更新没有触发回收，不能当作通过
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	tmp := make([]byte, 1)
	for {
		if _, err := conn.Read(tmp); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Error("idle session was not reaped after UpdateIdleTimeout")
			}
			return
		}
	}
}

// TestAcceptBackoffDelay 验证 accept 临时错误的退避曲线：10ms 起步逐次翻倍，封顶 200ms。
func TestAcceptBackoffDelay(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 10 * time.Millisecond},
		{1, 10 * time.Millisecond},
		{2, 20 * time.Millisecond},
		{3, 40 * time.Millisecond},
		{4, 80 * time.Millisecond},
		{5, 160 * time.Millisecond},
		{6, 200 * time.Millisecond},
		{10, 200 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := acceptBackoffDelay(tc.failures); got != tc.want {
			t.Errorf("acceptBackoffDelay(%d) = %v, want %v", tc.failures, got, tc.want)
		}
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
