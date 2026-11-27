package cmdline

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/auditlog"
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

	// auditWriter 审计日志文件写入器（AuditLogFile 非空且 Start 成功时非 nil）；
	// auditLogger 是审计专用 logger，不替换进程全局默认 logger。
	auditWriter *auditlog.Writer
	auditLogger *slog.Logger
}

// DefaultConfig 返回默认配置（单一来源：公共 facade 与 NewCmdLine(nil) 共用）。
// 提示符由模式层规范化为 "cmdline> "，此处保留未规范化的基础值。
func DefaultConfig() *Config {
	return &Config{
		Prompt:     "cmdline",
		Port:       2323,
		WelcomeMsg: "Welcome to Command Line Interface!\r\nType '?' for available commands.\r\n",
		MaxHistory: 100,
		PageSize:   24,

		// 安全与审计能力默认关闭（零值）
		MaxConnections:  0,
		IdleTimeout:     0,
		Password:        "",
		MaxAuthAttempts: 0,
		AuditLog:        false,
	}
}

// validateConfig 在 Start 前校验直接构造的配置（不经过 SetConfig 的路径）。
// MaxHistory <= 0 表示禁用历史，属于合法值。
func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535 (0 = ephemeral), got %d", cfg.Port)
	}
	if cfg.PageSize < 0 {
		return fmt.Errorf("pagesize must be non-negative, got %d", cfg.PageSize)
	}
	if cfg.MaxConnections < 0 {
		return fmt.Errorf("maxconnections must be non-negative, got %d", cfg.MaxConnections)
	}
	if cfg.MaxAuthAttempts < 0 {
		return fmt.Errorf("maxauthattempts must be non-negative, got %d", cfg.MaxAuthAttempts)
	}
	if cfg.IdleTimeout < 0 {
		return fmt.Errorf("idletimeout must be non-negative, got %s", cfg.IdleTimeout)
	}
	return nil
}

// NewCmdLine 创建新的命令行接口
func NewCmdLine(config *Config) *CmdLine {
	if config == nil {
		config = DefaultConfig()
	}

	// 创建根模式
	rootMode := mode.NewCommandMode("root", config.Prompt, "privileged EXEC mode")

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

	// 添加到会话命令树：非法参数语法必须向调用方返回错误
	if err := c.commandTree.AddCommand(name, description, handler, detailedDescription...); err != nil {
		return fmt.Errorf("register command %q: %w", name, err)
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
		return fmt.Errorf("register context command %q: %w", name, err)
	}
	if err := c.commandTree.AddContextCommand(name, description, handler, detailedDescription...); err != nil {
		return fmt.Errorf("register context command %q: %w", name, err)
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

// SetConfig 动态设置配置参数。
// 运行期语义（工单 02/03）：已运行的服务与会话持有启动时的配置快照；除
// idletimeout 会热更新空闲回收周期外，其余修改只作用于模板、下次 Start 生效，
// 因此并发 SetConfig 与活动会话读配置之间不存在数据竞争。
// port/auditlogfile/auditlog 仅在 Start 时生效，运行中修改会返回明确错误。
func (c *CmdLine) SetConfig(key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 启动期专属配置在运行中修改会静默无效，明确拒绝以免误导使用方
	if c.isRunning {
		switch key {
		case "port", "auditlogfile", "auditlog":
			return fmt.Errorf("config key %q can only be changed before Start", key)
		}
	}

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
		if err != nil || v < 0 || v > 65535 {
			return fmt.Errorf("invalid port value %q: must be between 0 and 65535 (0 = ephemeral)", value)
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
		// 空闲回收是唯一热更新的配置项：即使服务已启动，也重建回收 ticker
		if c.isRunning && c.server != nil {
			c.server.UpdateIdleTimeout(v)
		}
	case "password":
		// 任意字符串；置空表示关闭认证
		c.config.Password = value
	case "maxauthattempts":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			return fmt.Errorf("invalid maxauthattempts value %q: must be a non-negative integer", value)
		}
		c.config.MaxAuthAttempts = v
	case "auditlog":
		// 仅接受 "true"/"false"（strconv.ParseBool 兼容 "1"/"0"/"t"/"f" 等）
		v, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid auditlog value %q: must be a boolean (true/false)", value)
		}
		c.config.AuditLog = v
	case "auditlogfile":
		// 任意非空路径；仅在 Start 之前设置有效（Start 时初始化文件写入器）
		c.config.AuditLogFile = value
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

	// 直接构造的配置也要校验，非法值在 Start 前就返回明确错误
	if err := validateConfig(c.config); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("invalid config: %w", err)
	}

	// 持锁对配置取快照：Config 全部为值/字符串字段，浅拷贝即完整副本。
	// server 与 Session 在整个生命周期内只读这份快照，运行期 SetConfig
	// 只修改模板配置（c.config），从而消除并发读写同一 *Config 的数据竞争（工单 02）。
	// 快照语义：本次 Start 生效的配置在启动时固定；SetConfig 的修改对
	// 之后的新 Start 生效。
	snapshot := *c.config
	c.isRunning = true
	c.mu.Unlock() // 释放锁，避免死锁

	// 注册内置命令（在锁外执行，避免死锁）
	c.registerBuiltinCommands()

	// 审计初始化：AuditLog 开启时构建专用 logger（文件审计写入按日期滚动的文件，
	// 否则写 stderr），绝不替换进程全局默认 logger
	if err := c.setupAuditLogFile(&snapshot); err != nil {
		slog.Warn("failed to initialize audit log file, falling back to stderr", "error", err)
	}

	// 打印命令树结构（debug级别）
	if c.commandTree != nil {
		slog.Debug("command tree", "structure", "\n"+c.commandTree.PrintTree())
	}

	// 创建telnet服务器（传入配置快照），注入审计专用 logger
	c.server = server.NewTelnetServerWithContext(&snapshot, c.context)
	c.mu.RLock()
	auditLogger := c.auditLogger
	c.mu.RUnlock()
	c.server.SetAuditLogger(auditLogger)

	// 启动服务器
	err := c.server.Start()
	if err != nil {
		// 启动失败必须回收审计资源（文件句柄与后台清理 goroutine）：
		// 此时 isRunning 即将复位，Stop 会因"未运行"直接返回而不做清理，
		// 不回收将导致句柄与 goroutine 泄漏
		c.teardownAuditLogFile()
		c.mu.Lock()
		c.isRunning = false
		c.mu.Unlock()
		return err
	}
	slog.Info("command line interface started", "port", snapshot.Port)

	return nil
}

// setupAuditLogFile 在 Start 时构建审计专用 logger：
// AuditLogFile 非空时创建目录、打开按日期滚动的文件并启动后台清理 goroutine；
// 未设置文件时审计输出到 stderr。无论哪种情况都不替换进程全局默认 logger。
func (c *CmdLine) setupAuditLogFile(cfg *Config) error {
	if !cfg.AuditLog {
		return nil
	}

	var aw *auditlog.Writer
	var w io.Writer = os.Stderr
	if cfg.AuditLogFile != "" {
		if dir := filepath.Dir(cfg.AuditLogFile); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create audit log dir %s: %w", dir, err)
			}
		}
		var err error
		aw, err = auditlog.NewWriter(cfg.AuditLogFile)
		if err != nil {
			return err
		}
		if aw == nil {
			return nil
		}
		w = aw
	}

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 与 teardownAuditLogFile 共用 c.mu，避免 Start/Stop 并发时互相覆盖
	c.mu.Lock()
	if c.auditWriter != nil {
		c.mu.Unlock()
		if aw != nil {
			_ = aw.Close()
			aw.Wait()
		}
		return nil
	}
	c.auditWriter = aw
	c.auditLogger = logger
	c.mu.Unlock()
	return nil
}

// teardownAuditLogFile 在 Stop 时关闭审计专用通道：清空 logger 引用，
// 关闭审计文件并等待后台清理 goroutine 退出。不触碰进程全局默认 logger。
func (c *CmdLine) teardownAuditLogFile() {
	c.mu.Lock()
	w := c.auditWriter
	c.auditWriter = nil
	c.auditLogger = nil
	c.mu.Unlock()

	if w == nil {
		return
	}
	_ = w.Close()
	w.Wait() // 等待清理 goroutine 退出后再关闭文件，避免竞争
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

	// 会话全部退出后再关闭审计文件，确保最后的审计记录落盘
	c.teardownAuditLogFile()
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
