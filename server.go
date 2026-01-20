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
	config   *Config
	commands map[string]CommandInfo
	listener net.Listener
	sessions map[net.Conn]*Session
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
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

// Start 启动telnet服务器
func (ts *TelnetServer) Start() error {
	var err error
	ts.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", ts.config.Port))
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

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
	// 创建会话
	session := NewSession(conn, ts.config, ts.commands)

	// 注册会话
	ts.mu.Lock()
	ts.sessions[conn] = session
	ts.mu.Unlock()

	// 处理会话
	err := session.Handle(ts.ctx)
	if err != nil && err != io.EOF {
		fmt.Printf("Session error: %v\n", err)
	}

	// 清理会话
	ts.mu.Lock()
	delete(ts.sessions, conn)
	ts.mu.Unlock()

	conn.Close()
}
