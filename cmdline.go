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
	RootMode   *CommandMode
}

// CmdLine 命令行接口
type CmdLine struct {
	config      *Config
	commands    map[string]CommandInfo // 向后兼容的平面命令存储
	commandTree *CommandTree           // 新的树形命令存储
	mu          sync.RWMutex
	server      *TelnetServer
	isRunning   bool
	rootMode    *CommandMode
	context     *CommandContext
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

	// 创建根模式
	rootMode := NewCommandMode("root", config.Prompt, "privileged EXEC mode")

	// 设置配置的根模式
	config.RootMode = rootMode

	// 创建命令树
	commandTree := NewCommandTree()

	// 创建命令上下文
	context := &CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
		Variables:   make(map[string]string),
		commandTree: commandTree,
	}

	return &CmdLine{
		config:      config,
		commands:    make(map[string]CommandInfo),
		commandTree: commandTree,
		rootMode:    rootMode,
		context:     context,
	}
}

// RegisterCommand 注册命令到根模式
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 向后兼容：添加到平面命令存储
	c.rootMode.AddCommand(name, description, handler)

	// 新功能：添加到命令树
	err := c.commandTree.AddCommand(name, description, handler)
	if err != nil {
		fmt.Printf("Warning: Failed to add command to tree: %v\n", err)
	}
}

// RegisterModeCommand 注册命令到指定模式
func (c *CmdLine) RegisterModeCommand(modePath string, name, description string, handler CommandHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 查找或创建模式路径
	currentMode := c.rootMode
	if modePath != "" {
		modeName := modePath
		if subMode, exists := currentMode.Children[modeName]; exists {
			currentMode = subMode
		} else {
			// 创建新的子模式
			subMode = NewCommandMode(modeName, modeName, fmt.Sprintf("%s configuration", modeName))
			currentMode.AddSubMode(subMode)
			currentMode = subMode

			// 同时添加到命令树，使用专门的视图切换命令方法
			_ = c.commandTree.AddModeCommand(modeName, fmt.Sprintf("Enter %s configuration mode", description))
		}
	}

	currentMode.AddCommand(name, description, handler)

	// 不再添加到全局命令树，每个视图有自己的独立命令树
}

// CreateMode 创建新的命令模式
func (c *CmdLine) CreateMode(modePath string, description string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentMode := c.rootMode
	if modePath != "" {
		modeName := modePath
		if subMode, exists := currentMode.Children[modeName]; exists {
			currentMode = subMode
		} else {
			// 创建新的子模式
			// 子视图Prompt只包含子模式名称，以'#'结束
			prompt := modeName

			subMode = NewCommandMode(modeName, prompt, description)
			currentMode.AddSubMode(subMode)
			currentMode = subMode

			// 同时添加到命令树，使用专门的视图切换命令方法
			_ = c.commandTree.AddModeCommand(modeName, fmt.Sprintf("Enter %s configuration mode", description))
		}
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

	// 打印命令树结构
	if c.commandTree != nil {
		fmt.Printf("\n=== Command Tree Structure ===\n")
		fmt.Printf("%s\n", c.commandTree.PrintTree())
		fmt.Printf("=== End of Command Tree ===\n\n")
	}

	// 创建telnet服务器
	c.server = NewTelnetServerWithContext(c.config, c.context)
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

	// 使用命令上下文获取当前模式下的所有可用命令
	commands := c.context.GetAvailableCommands()

	writer.Write([]byte("Available commands:\r\n"))
	for name, cmd := range commands {
		// 跳过内置命令的重复显示
		if name == "help" || name == "?" {
			continue
		}
		writer.Write([]byte(fmt.Sprintf("  %-15s - %s\r\n", name, cmd.Description)))
	}

	// 显示内置命令
	writer.Write([]byte("  help/?          - Show this help message\r\n"))

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
