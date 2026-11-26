package cmdline

import (
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

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
			PageSize:   24,
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

// RegisterCommand 注册命令到根模式。
// Start 之后命令树对各会话只读（会话 goroutine 并发遍历），此时调用将返回错误。
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("cannot register command %q after Start: command trees are shared read-only across active sessions", name)
	}

	// 向后兼容：添加到平面命令存储（包含多行详细描述）
	c.rootMode.AddCommand(name, description, handler, detailedDescription...)

	// 新功能：添加到命令树
	err := c.commandTree.AddCommand(name, description, handler, detailedDescription...)
	if err != nil {
		slog.Warn("failed to add command to tree", "error", err)
	}
	return nil
}

// RegisterContextCommand 注册支持上下文的命令到根模式。
// handler 可通过 ctx 感知连接关闭/服务停止并提前退出。
// Start 之后命令树对各会话只读，此时调用将返回错误。
// 注意：ContextHandler 无法存入旧签名的平面命令表（mode.Commands），仅注册到命令树。
func (c *CmdLine) RegisterContextCommand(name, description string, handler types.ContextHandler, detailedDescription ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("cannot register command %q after Start: command trees are shared read-only across active sessions", name)
	}

	// 添加到根模式命令树与会话命令树（与 RegisterCommand 的双树注册保持一致）
	if err := c.rootMode.CommandTree.AddContextCommand(name, description, handler, detailedDescription...); err != nil {
		slog.Warn("failed to add context command to root mode tree", "error", err)
	}
	if err := c.commandTree.AddContextCommand(name, description, handler, detailedDescription...); err != nil {
		slog.Warn("failed to add context command to tree", "error", err)
	}
	return nil
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

	// 同时添加到全局命令树和根模式命令树（用于视图切换查找）
	_ = c.commandTree.AddModeCommand(modeName, fmt.Sprintf("Enter %s configuration mode", modeName))
	_ = c.rootMode.CommandTree.AddModeCommand(modeName, fmt.Sprintf("Enter %s configuration mode", modeName))

	// 添加退出命令
	subMode.AddCommand("exit", "Exit and close connection", c.CreateCloseConnectionHandler())
	subMode.AddCommand("quit", "Exit to previous mode", c.CreateExitToRootHandler())

	return subMode
}

// RegisterModeCommand 注册命令到指定模式。
// Start 之后命令树对各会话只读，此时调用将返回错误。
func (c *CmdLine) RegisterModeCommand(modePath string, name, description string, handler CommandHandler, detailedDescription ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("cannot register mode command %q after Start: command trees are shared read-only across active sessions", name)
	}

	currentMode := c.findOrCreateMode(modePath, fmt.Sprintf("%s configuration", modePath))
	currentMode.AddCommand(name, description, handler, detailedDescription...)
	return nil
}

// CreateMode 创建新的命令模式。
// Start 之后模式树对各会话只读，此时调用将返回错误。
func (c *CmdLine) CreateMode(modePath string, description string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("cannot create mode %q after Start: mode tree is shared read-only across active sessions", modePath)
	}

	c.findOrCreateMode(modePath, description)
	return nil
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
		v, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid maxhistory value %q: %w", value, err)
		}
		c.config.MaxHistory = v
	case "port":
		v, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value %q: %w", value, err)
		}
		c.config.Port = v
	case "pagesize":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			return fmt.Errorf("invalid pagesize value %q: must be a non-negative integer", value)
		}
		c.config.PageSize = v
	case "maxconnections":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			return fmt.Errorf("invalid maxconnections value %q: must be a non-negative integer", value)
		}
		c.config.MaxConnections = v
	case "idletimeout":
		// 支持 Go duration 语法（"5m"、"30s"、"1m30s"），不支持纯数字（避免误当纳秒/秒）
		v, err := time.ParseDuration(value)
		if err != nil || v < 0 {
			return fmt.Errorf("invalid idletimeout value %q: must be a non-negative duration like \"5m\" or \"30s\"", value)
		}
		c.config.IdleTimeout = v
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

	c.isRunning = true
	c.mu.Unlock() // 释放锁，避免死锁

	// 注册内置命令（在锁外执行，避免死锁）
	c.registerBuiltinCommands()

	// 打印命令树结构（debug级别）
	if c.commandTree != nil {
		slog.Debug("command tree", "structure", "\n"+c.commandTree.PrintTree())
	}

	// 创建telnet服务器
	c.server = server.NewTelnetServerWithContext(c.config, c.context)

	// 启动服务器
	err := c.server.Start()
	if err != nil {
		c.mu.Lock()
		c.isRunning = false
		c.mu.Unlock()
		return err
	}
	slog.Info("command line interface started", "port", c.config.Port)

	return nil
}

// Stop 停止命令行服务
func (c *CmdLine) Stop() error {
	c.mu.Lock()
	if !c.isRunning {
		c.mu.Unlock()
		return fmt.Errorf("cmdline is not running")
	}
	c.isRunning = false
	c.mu.Unlock()

	// 锁外等待会话退出：Stop 内部含超时兜底，持锁调用会阻塞其他操作
	if c.server != nil {
		c.server.Stop()
	}
	return nil
}

// CreateExitToRootHandler 创建退出到根模式处理函数
func (c *CmdLine) CreateExitToRootHandler() types.CommandHandler {
	return func(args []string) string {
		// 返回特殊标记，让会话层知道需要更新模式状态
		return types.SentinelExitToRoot
	}
}

// CreateCloseConnectionHandler 创建关闭连接处理函数
func (c *CmdLine) CreateCloseConnectionHandler() types.CommandHandler {
	return func(args []string) string {
		// 返回特殊标记，让会话层处理退出逻辑
		return types.SentinelExit
	}
}

// registerBuiltinCommands 注册内置命令。
// 直接写入模式与命令树，绕过 RegisterCommand 的 isRunning 守卫：
// 本方法在 Start 流程内、isRunning 置位后调用，走守卫路径会导致注册静默失败
// （回归：exit/quit 显示 Unknown command，无法退出会话）。
func (c *CmdLine) registerBuiltinCommands() {
	const exitDesc = "Exit and close connection"
	exitHandler := c.CreateCloseConnectionHandler()

	c.rootMode.AddCommand("exit", exitDesc, exitHandler)
	c.rootMode.AddCommand("quit", exitDesc, exitHandler)

	_ = c.commandTree.AddCommand("exit", exitDesc, exitHandler)
	_ = c.commandTree.AddCommand("quit", exitDesc, exitHandler)
}
