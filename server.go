package tnlcmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
)

// TelnetServer telnet服务器
type TelnetServer struct {
	config      *Config
	commands    map[string]CommandInfo
	commandTree *CommandTree
	context     *CommandContext
	listener    net.Listener
	sessions    map[net.Conn]*Session
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewTelnetServer 创建新的telnet服务器
func NewTelnetServer(config *Config, commands map[string]CommandInfo) *TelnetServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &TelnetServer{
		config:   config,
		commands: commands,
		sessions: make(map[net.Conn]*Session),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// NewTelnetServerWithContext 创建带上下文的telnet服务器
func NewTelnetServerWithContext(config *Config, cmdContext *CommandContext) *TelnetServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &TelnetServer{
		config:      config,
		commands:    cmdContext.GetAvailableCommands(),
		commandTree: cmdContext.commandTree,
		context:     cmdContext,
		sessions:    make(map[net.Conn]*Session),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start 启动telnet服务器
func (ts *TelnetServer) Start() error {
	var err error
	fmt.Printf("Attempting to listen on port %d...\n", ts.config.Port)
	ts.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", ts.config.Port))
	if err != nil {
		fmt.Printf("Failed to listen on port %d: %v\n", ts.config.Port, err)
		return fmt.Errorf("failed to start server: %w", err)
	}

	fmt.Printf("Successfully listening on port %d, starting accept connections...\n", ts.config.Port)
	go ts.acceptConnections()

	fmt.Printf("Telnet server started on port %d\n", ts.config.Port)
	return nil
}

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
				return
			}
			continue
		}

		go ts.handleConnection(conn)
	}
}

// handleConnection 处理连接
func (ts *TelnetServer) handleConnection(conn net.Conn) {
	// 使用服务器中的上下文（如果可用）
	var context *CommandContext
	if ts.context != nil {
		// 复制上下文，但每个连接使用独立的实例
		context = &CommandContext{
			CurrentMode: ts.context.CurrentMode,
			Path:        make([]string, len(ts.context.Path)),
			Variables:   make(map[string]string),
			commandTree: ts.context.commandTree,
		}
		copy(context.Path, ts.context.Path)
		// 复制变量（如果需要）
		for k, v := range ts.context.Variables {
			context.Variables[k] = v
		}
	} else {
		// 向后兼容：创建新的上下文
		context = &CommandContext{
			CurrentMode: ts.config.RootMode,
			Path:        []string{},
			Variables:   make(map[string]string),
		}
	}

	// 创建会话
	session := NewSessionWithContext(conn, ts.config, context)

	// 注册会话
	ts.mu.Lock()
	ts.sessions[conn] = session
	ts.mu.Unlock()

	// 处理会话
	err := session.Handle(ts.ctx)
	if err != nil && err != io.EOF {
		fmt.Printf("Session error: %v\n", err)
	}

	// 会话结束，清理
	ts.mu.Lock()
	delete(ts.sessions, conn)
	ts.mu.Unlock()
	conn.Close()
}
