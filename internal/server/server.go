package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/debuglog"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/internal/session"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// TelnetServer telnet服务器
type TelnetServer struct {
	config   *types.Config
	context  *mode.CommandContext
	listener net.Listener
	sessions map[net.Conn]*session.Session
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup // 跟踪活跃的 session goroutine
}

// NewTelnetServerWithContext 创建带上下文的telnet服务器
func NewTelnetServerWithContext(config *types.Config, commandctx *mode.CommandContext) *TelnetServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &TelnetServer{
		config:   config,
		context:  commandctx,
		sessions: make(map[net.Conn]*session.Session),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start 启动telnet服务器
func (ts *TelnetServer) Start() error {
	var err error
	slog.Debug("attempting to listen", "port", ts.config.Port)
	ts.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", ts.config.Port))
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	slog.Debug("listener established, accepting connections", "port", ts.config.Port)
	go ts.acceptConnections()

	// 启用空闲会话回收（IdleTimeout > 0 时）
	if ts.config.IdleTimeout > 0 {
		go ts.reapIdleSessions()
	}

	return nil
}

// stopTimeout 等待会话 goroutine 退出的兜底超时。
// 慢 Handler（阻塞 I/O、长循环）无法被强制中断，超时后放弃等待以避免 Stop 永久阻塞。
const stopTimeout = 5 * time.Second

// Stop 停止telnet服务器
func (ts *TelnetServer) Stop() {
	if ts.cancel != nil {
		ts.cancel()
	}

	if ts.listener != nil {
		ts.listener.Close()
	}

	// 关闭所有会话
	ts.mu.Lock()
	for conn, session := range ts.sessions {
		session.Close()
		delete(ts.sessions, conn)
	}
	ts.mu.Unlock()

	// 等待所有 session goroutine 退出，超时后放弃等待（Handler 无法被强制中断）
	done := make(chan struct{})
	go func() {
		ts.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopTimeout):
		slog.Warn("server stop timeout: some session handlers are still running", "timeout", stopTimeout)
	}
}

// acceptConnections 接受连接
func (ts *TelnetServer) acceptConnections() {
	for {
		select {
		case <-ts.ctx.Done():
			return
		default:
		}

		conn, err := ts.listener.Accept()
		if err != nil {
			if ts.ctx.Err() != nil {
				if debuglog.Enabled() {
					debuglog.Debug("server", "accept_stopped", "err", err)
				}
				return
			}
			if debuglog.Enabled() {
				debuglog.Debug("server", "accept_error", "err", err)
			}
			continue
		}

		go ts.handleConnection(conn)
	}
}

// handleConnection 处理连接
func (ts *TelnetServer) handleConnection(conn net.Conn) {
	ts.wg.Add(1)
	defer ts.wg.Done()

	if debuglog.Enabled() {
		debuglog.Debug("server", "conn_accepted",
			"remote", conn.RemoteAddr().String(),
			"local", conn.LocalAddr().String())
	}

	// 命令上下文由构造方（cmdline/测试）显式传入；缺失说明初始化不完整，
	// 直接拒绝连接而非降级猜测根模式
	if ts.context == nil {
		slog.Warn("no command context available, closing connection")
		conn.Close()
		return
	}

	// 每个连接使用独立的上下文副本，避免会话间共享可变状态
	context := ts.context.Clone()

	// 创建会话
	session := session.NewSessionWithContext(conn, ts.config, context)

	// 注册会话，并在同一临界区内执行连接数上限检查，
	// 保证"检查 + 注册"原子性，避免并发连接竞争上限时超发
	ts.mu.Lock()
	if ts.config.MaxConnections > 0 && len(ts.sessions) >= ts.config.MaxConnections {
		ts.mu.Unlock()
		ts.rejectConnection(conn)
		return
	}
	ts.sessions[conn] = session
	ts.mu.Unlock()

	// 处理会话
	err := session.Handle(ts.ctx)
	if err != nil && err != io.EOF {
		slog.Warn("session error", "error", err)
		if debuglog.Enabled() {
			debuglog.Debug("server", "conn_session_error",
				"remote", conn.RemoteAddr().String(),
				"err", err)
		}
	}
	if debuglog.Enabled() {
		debuglog.Debug("server", "conn_closed",
			"remote", conn.RemoteAddr().String(),
			"err", err)
	}

	// 会话结束，清理
	ts.mu.Lock()
	delete(ts.sessions, conn)
	ts.mu.Unlock()
	conn.Close()
}

// connLimitMsg 超过最大连接数时向客户端发送的拒绝提示（Cisco 风格 % 前缀）
const connLimitMsg = "\r\n% Too many connections. Please try again later.\r\n"

// rejectTimeout 向超限连接写入拒绝提示的兜底超时，防止慢客户端拖住 accept 路径
const rejectTimeout = 3 * time.Second

// rejectConnection 礼貌拒绝超限连接：发送提示后立即关闭
func (ts *TelnetServer) rejectConnection(conn net.Conn) {
	slog.Info("connection rejected: max connections reached",
		"remote", conn.RemoteAddr().String(),
		"max", ts.config.MaxConnections)
	if debuglog.Enabled() {
		debuglog.Debug("server", "conn_rejected",
			"remote", conn.RemoteAddr().String(),
			"max", ts.config.MaxConnections)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(rejectTimeout))
	if _, err := conn.Write([]byte(connLimitMsg)); err != nil {
		slog.Debug("failed to send rejection message", "remote", conn.RemoteAddr().String(), "err", err)
	}
	conn.Close()
}

// reapIdleSessions 周期性回收空闲超过 IdleTimeout 的会话。
// 由 Start 在 IdleTimeout > 0 时启动，服务器 ctx 取消时退出。
func (ts *TelnetServer) reapIdleSessions() {
	// 扫描间隔取阈值的一半，下限 100ms 保证小阈值场景下回收及时
	interval := ts.config.IdleTimeout / 2
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ts.ctx.Done():
			return
		case <-ticker.C:
			ts.closeStaleSessions()
		}
	}
}

// closeStaleSessions 关闭所有空闲超时的会话连接。
// 先在读锁下收集过期会话，再在锁外逐个关闭：Close 会解除会话 readLine 的阻塞，
// 会话 goroutine 自行完成退出与 map 清理。
func (ts *TelnetServer) closeStaleSessions() {
	ts.mu.RLock()
	var stale []*session.Session
	for _, s := range ts.sessions {
		if s.IsStale(ts.config.IdleTimeout) {
			stale = append(stale, s)
		}
	}
	ts.mu.RUnlock()

	for _, s := range stale {
		slog.Info("reaping idle session", "idle_timeout", ts.config.IdleTimeout)
		s.Close()
	}
}

// UpdateAllSessionsPrompt 更新所有活动会话的提示符
func (ts *TelnetServer) UpdateAllSessionsPrompt(prompt string) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	for _, session := range ts.sessions {
		session.UpdatePrompt(prompt)
	}
}

// Addr 返回服务器的监听地址。
func (ts *TelnetServer) Addr() string {
	if ts.listener != nil {
		return ts.listener.Addr().String()
	}
	return ""
}

// Listener 返回监听器。
func (ts *TelnetServer) Listener() net.Listener {
	return ts.listener
}
