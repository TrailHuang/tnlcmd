package cmdline

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/internal/server"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandHandler 命令处理函数类型
type CommandHandler = types.CommandHandler

// CommandInfo 命令信息
type CommandInfo = types.CommandInfo

// Config 命令行配置
type Config = types.Config

// CmdLine 命令行接口
type CmdLine struct {
	config      *Config
	commands    map[string]CommandInfo   // 向后兼容的平面命令存储
	commandTree *commandtree.CommandTree // 新的树形命令存储
	mu          sync.RWMutex
	server      *server.TelnetServer
	isRunning   bool
	rootMode    *mode.CommandMode
	context     *mode.CommandContext
}

// NewCmdLine 创建新的命令行接口
func NewCmdLine(config *Config) *CmdLine {
	if config == nil {
		config = &Config{
			Prompt:     "cmdline> ",
			Port:       2323,
			WelcomeMsg: "Welcome to Command Line Interface!\r\nType '?' for available commands.\r\n",
			MaxHistory: 100,
		}
	}

	// 创建根模式
	rootMode := mode.NewCommandMode("root", config.Prompt, "privileged EXEC mode")

	// 设置配置的根模式
	config.RootMode = rootMode

	// 创建命令树
	commandTree := commandtree.NewCommandTree()

	// 创建命令上下文
	context := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
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
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 向后兼容：添加到平面命令存储
	c.rootMode.AddCommand(name, description, handler)

	// 新功能：添加到命令树
	err := c.commandTree.AddCommand(name, description, handler, detailedDescription...)
	if err != nil {
		fmt.Printf("Warning: Failed to add command to tree: %v\n", err)
	}
}

// findOrCreateMode 查找或创建模式路径
func (c *CmdLine) findOrCreateMode(modePath string, description string) *mode.CommandMode {
	currentMode := c.rootMode
	if modePath == "" {
		return currentMode
	}

	modeName := modePath
	if subMode, exists := currentMode.Children[modeName]; exists {
		return subMode
	}

	// 创建新的子模式
	prompt := modeName
	subMode := mode.NewCommandMode(modeName, prompt, description)
	currentMode.AddSubMode(subMode)

	// 同时添加到命令树，使用专门的视图切换命令方法
	_ = c.commandTree.AddModeCommand(modeName, fmt.Sprintf("Enter %s configuration mode B", description))

	// 添加退出命令
	subMode.AddCommand("exit", "Exit and close connection", c.CreateCloseConnectionHandler())
	subMode.AddCommand("quit", "Exit to previous mode", c.CreateExitToRootHandler())

	return subMode
}

// RegisterModeCommand 注册命令到指定模式
func (c *CmdLine) RegisterModeCommand(modePath string, name, description string, handler CommandHandler, detailedDescription ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentMode := c.findOrCreateMode(modePath, fmt.Sprintf("%s configuration", modePath))
	currentMode.AddCommand(name, description, handler, detailedDescription...)
}

// CreateMode 创建新的命令模式
func (c *CmdLine) CreateMode(modePath string, description string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.findOrCreateMode(modePath, description)
}

// SetConfig 动态设置配置参数
func (c *CmdLine) SetConfig(key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch key {
	case "prompt":
		c.config.Prompt = value
		c.rootMode.SetPrompt(value)
	case "welcome":
		c.config.WelcomeMsg = value
	case "maxhistory":
		// 这里可以添加类型转换逻辑
		c.config.MaxHistory, _ = strconv.Atoi(value)
	case "port":
		c.config.Port, _ = strconv.Atoi(value)
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
	c.server = server.NewTelnetServerWithContext(c.config, c.context)
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

// CreateExitToRootHandler 创建退出到根模式处理函数
func (c *CmdLine) CreateExitToRootHandler() types.CommandHandler {
	return func(args []string) string {
		// 返回特殊标记，让会话层知道需要更新模式状态
		return "__EXIT_TO_ROOT__"
	}
}

// CreateCloseConnectionHandler 创建关闭连接处理函数
func (c *CmdLine) CreateCloseConnectionHandler() types.CommandHandler {
	return func(args []string) string {
		// 返回特殊标记，让会话层处理退出逻辑
		return "__EXIT__"
	}
}

// registerBuiltinCommands 注册内置命令
func (c *CmdLine) registerBuiltinCommands() {
	fmt.Printf("Starting to register builtin commands...\n")

	// 添加退出命令
	c.RegisterCommand("exit", "Exit and close connection", c.CreateCloseConnectionHandler())
	c.RegisterCommand("quit", "Exit to previous mode", c.CreateCloseConnectionHandler())
	fmt.Printf("Builtin commands registration completed\n")
}
