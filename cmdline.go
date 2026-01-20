package tnlcmd

import (
	"fmt"
	"io"
	"sync"
)

// CommandHandler 命令处理函数类型
type CommandHandler func(args []string, writer io.Writer) error

// CommandInfo 命令信息
type CommandInfo struct {
	Name        string
	Description string
	Handler     CommandHandler
}

// Config 命令行配置
type Config struct {
	Prompt     string
	Port       int
	WelcomeMsg string
	MaxHistory int
}

// CmdLine 命令行接口
type CmdLine struct {
	config    *Config
	commands  map[string]CommandInfo
	mu        sync.RWMutex
	server    *TelnetServer
	isRunning bool
}

// NewCmdLine 创建新的命令行接口
func NewCmdLine(config *Config) *CmdLine {
	if config == nil {
		config = &Config{
			Prompt:     "cmdline> ",
			Port:       2323,
			WelcomeMsg: "Welcome to Command Line Interface!\r\nType 'help' for available commands.\r\n",
			MaxHistory: 100,
		}
	}

	return &CmdLine{
		config:   config,
		commands: make(map[string]CommandInfo),
	}
}

// RegisterCommand 注册命令
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.commands[name] = CommandInfo{
		Name:        name,
		Description: description,
		Handler:     handler,
	}
}

// SetConfig 动态设置配置参数
func (c *CmdLine) SetConfig(key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch key {
	case "prompt":
		c.config.Prompt = value
	case "welcome":
		c.config.WelcomeMsg = value
	case "maxhistory":
		// 这里可以添加类型转换逻辑
		return nil
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	return nil
}

// Start 启动命令行服务
func (c *CmdLine) Start() error {
	c.mu.Lock()

	if c.isRunning {
		c.mu.Unlock()
		return fmt.Errorf("cmdline is already running")
	}
	fmt.Printf("Config: %v\n", c.config)

	c.isRunning = true
	c.mu.Unlock() // 释放锁，避免死锁

	// 注册内置命令（在锁外执行，避免死锁）
	c.registerBuiltinCommands()
	fmt.Printf("registered commands: %v\n", c.commands)
	// 创建telnet服务器
	c.server = NewTelnetServer(c.config, c.commands)
	fmt.Printf("Telnet server created, starting...\n")

	// 启动服务器
	err := c.server.Start()
	if err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		c.mu.Lock()
		c.isRunning = false
		c.mu.Unlock()
		return err
	}
	fmt.Printf("Command line interface started on port %d\n", c.config.Port)
	fmt.Println("Connect with: telnet localhost 2323")

	return nil
}

// Stop 停止命令行服务
func (c *CmdLine) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRunning {
		return fmt.Errorf("cmdline is not running")
	}

	if c.server != nil {
		c.server.Stop()
	}

	c.isRunning = false
	return nil
}

// registerBuiltinCommands 注册内置命令
func (c *CmdLine) registerBuiltinCommands() {
	fmt.Printf("Starting to register builtin commands...\n")
	// help命令
	c.RegisterCommand("help", "Show this help message", c.helpHandler)
	c.RegisterCommand("?", "Show this help message", c.helpHandler)
	fmt.Printf("Registered help commands\n")

	// set命令
	c.RegisterCommand("set", "Set configuration parameters", c.setHandler)
	fmt.Printf("Registered set command\n")

	// exit/quit命令
	c.RegisterCommand("exit", "Exit the session", c.exitHandler)
	c.RegisterCommand("quit", "Exit the session", c.exitHandler)
	fmt.Printf("Registered exit/quit commands\n")
	fmt.Printf("Builtin commands registration completed\n")
}

// helpHandler 帮助命令处理函数
func (c *CmdLine) helpHandler(args []string, writer io.Writer) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	writer.Write([]byte("Available commands:\r\n"))
	for _, cmd := range c.commands {
		writer.Write([]byte(fmt.Sprintf("  %-10s - %s\r\n", cmd.Name, cmd.Description)))
	}
	return nil
}

// setHandler set命令处理函数
func (c *CmdLine) setHandler(args []string, writer io.Writer) error {
	if len(args) < 2 {
		writer.Write([]byte("Usage: set <key> <value>\r\n"))
		writer.Write([]byte("Available keys: prompt, welcome\r\n"))
		return nil
	}

	err := c.SetConfig(args[0], args[1])
	if err != nil {
		writer.Write([]byte(fmt.Sprintf("Error: %s\r\n", err.Error())))
		return err
	}

	writer.Write([]byte("Configuration updated successfully\r\n"))
	return nil
}

// exitHandler 退出命令处理函数
func (c *CmdLine) exitHandler(args []string, writer io.Writer) error {
	writer.Write([]byte("Goodbye!\r\n"))
	return io.EOF
}
