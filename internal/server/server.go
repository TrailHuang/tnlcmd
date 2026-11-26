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

	// 使用服务器中的上下文（如果可用）
	var context *mode.CommandContext
	if ts.context != nil {
		// 每个连接使用独立的上下文副本，避免会话间共享可变状态
		context = ts.context.Clone()
	} else {
		// 向后兼容：从 RootMode 创建新的上下文（使用安全类型断言）
		rootMode, ok := ts.config.RootMode.(*mode.CommandMode)
		if !ok || rootMode == nil {
			slog.Warn("invalid RootMode type, closing connection", "type", fmt.Sprintf("%T", ts.config.RootMode))
			conn.Close()
			return
		}
		context = &mode.CommandContext{
			CurrentMode: rootMode,
			Path:        []string{},
		}
	}

	// 创建会话
	session := session.NewSessionWithContext(conn, ts.config, context)

	// 注册会话
	ts.mu.Lock()
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
