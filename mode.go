package tnlcmd

import (
	"fmt"
	"io"
	"strings"
)

// CommandMode 命令模式
type CommandMode struct {
	Name        string
	Prompt      string
	Description string
	Commands    map[string]CommandInfo
	Parent      *CommandMode
	Children    map[string]*CommandMode
	commandTree *CommandTree // 当前视图的独立命令树
}

// CommandContext 命令上下文
type CommandContext struct {
	CurrentMode *CommandMode
	Path        []string
	Variables   map[string]string
	commandTree *CommandTree
}

// NewCommandMode 创建新的命令模式
func NewCommandMode(name, prompt, description string) *CommandMode {
	// 确保Prompt格式符合规范
	formattedPrompt := prompt
	if !strings.HasSuffix(prompt, ">") && !strings.HasSuffix(prompt, "#") {
		// 如果是根模式，添加'>'结束符；否则添加'#'结束符
		if name == "root" {
			formattedPrompt = prompt + "> "
		} else {
			// 移除末尾空格后添加'#'结束符
			formattedPrompt = strings.TrimSpace(prompt) + "# "
		}
	}

	return &CommandMode{
		Name:        name,
		Prompt:      formattedPrompt,
		Description: description,
		Commands:    make(map[string]CommandInfo),
		Children:    make(map[string]*CommandMode),
		commandTree: NewCommandTree(), // 为每个视图创建独立的命令树
	}
}

// AddCommand 添加命令到模式
func (m *CommandMode) AddCommand(name, description string, handler CommandHandler) {
	m.Commands[name] = CommandInfo{
		Name:        name,
		Description: description,
		Handler:     handler,
	}

	// 同时添加到当前视图的独立命令树
	if m.commandTree != nil {
		_ = m.commandTree.AddCommand(name, description, handler)
	}
}

// AddSubMode 添加子模式
func (m *CommandMode) AddSubMode(subMode *CommandMode) {
	subMode.Parent = m
	m.Children[subMode.Name] = subMode
}

// GetFullPath 获取完整模式路径
func (c *CommandContext) GetFullPath() string {
	if len(c.Path) == 0 {
		return ""
	}
	return strings.Join(c.Path, " ")
}

// ChangeMode 切换命令模式
func (c *CommandContext) ChangeMode(mode *CommandMode) {
	// 检查视图层级限制（最多两级）
	if mode.Parent != nil && mode.Parent.Parent != nil {
		// 如果尝试进入第三级视图，拒绝并返回上一级
		mode = mode.Parent
	}

	c.CurrentMode = mode

	// 更新路径（严格限制为两级）
	if mode.Parent == nil {
		// 根视图
		c.Path = []string{}
	} else {
		// 子视图（只保留当前视图名称）
		c.Path = []string{mode.Name}
	}
	// 打印命令树结构
	if c.CurrentMode.commandTree != nil {
		fmt.Printf("\n=== Command Tree Structure ===\n")
		fmt.Printf("%s\n", c.CurrentMode.commandTree.PrintTree())
		fmt.Printf("=== End of Command Tree ===\n\n")
	}
}

// GetAvailableCommands 获取当前模式下可用的命令
func (c *CommandContext) GetAvailableCommands() map[string]CommandInfo {
	commands := make(map[string]CommandInfo)

	// 添加内置命令（在所有模式下都可用）
	commands["help"] = CommandInfo{
		Name:        "help",
		Description: "Show this help message",
		Handler:     c.createHelpHandler(),
	}
	commands["?"] = CommandInfo{
		Name:        "?",
		Description: "Show this help message",
		Handler:     c.createHelpHandler(),
	}

	// 添加当前模式的命令
	for name, cmd := range c.CurrentMode.Commands {
		commands[name] = cmd
	}

	// 添加所有子模式切换命令（从任意视图都可以切换到其他视图）
	rootMode := c.getRootMode()
	for name, subMode := range rootMode.Children {
		// 如果当前不是该子模式，则显示切换命令
		if c.CurrentMode != subMode {
			commands[name] = CommandInfo{
				Name:        name,
				Description: fmt.Sprintf("Enter %s configuration mode", subMode.Description),
				Handler:     c.createModeChangeHandler(subMode),
			}
		}
	}

	// 添加退出命令
	if c.CurrentMode.Parent == nil {
		// 根视图：exit和quit都关闭连接
		commands["exit"] = CommandInfo{
			Name:        "exit",
			Description: "Exit and close connection",
			Handler:     c.createCloseConnectionHandler(),
		}
		commands["quit"] = CommandInfo{
			Name:        "quit",
			Description: "Exit and close connection",
			Handler:     c.createCloseConnectionHandler(),
		}
	} else {
		// 子视图：quit退出到根视图，exit关闭连接
		commands["quit"] = CommandInfo{
			Name:        "quit",
			Description: "Exit to privileged EXEC mode",
			Handler:     c.createExitToRootHandler(),
		}
		commands["exit"] = CommandInfo{
			Name:        "exit",
			Description: "Exit and close connection",
			Handler:     c.createCloseConnectionHandler(),
		}
	}

	return commands
}

// createModeChangeHandler 创建模式切换处理函数
func (c *CommandContext) createModeChangeHandler(mode *CommandMode) CommandHandler {
	return func(args []string, writer io.Writer) error {
		c.ChangeMode(mode)
		writer.Write([]byte(fmt.Sprintf("Entering %s mode\r\n", mode.Description)))
		return nil
	}
}

// getRootMode 获取根模式
func (c *CommandContext) getRootMode() *CommandMode {
	root := c.CurrentMode
	for root.Parent != nil {
		root = root.Parent
	}
	return root
}

// createExitToRootHandler 创建退出到根模式处理函数
func (c *CommandContext) createExitToRootHandler() CommandHandler {
	return func(args []string, writer io.Writer) error {
		// 找到根模式
		root := c.getRootMode()
		c.ChangeMode(root)
		writer.Write([]byte("Exiting to privileged EXEC mode\r\n"))
		return nil
	}
}

// createCloseConnectionHandler 创建关闭连接处理函数
func (c *CommandContext) createCloseConnectionHandler() CommandHandler {
	return func(args []string, writer io.Writer) error {
		writer.Write([]byte("Connection closed\r\n"))
		return io.EOF
	}
}

// createHelpHandler 创建帮助命令处理函数
func (c *CommandContext) createHelpHandler() CommandHandler {
	return func(args []string, writer io.Writer) error {
		commands := c.GetAvailableCommands()

		// 显示当前模式信息
		writer.Write([]byte(fmt.Sprintf("Current mode: %s\r\n", c.CurrentMode.Description)))
		writer.Write([]byte("Available commands:\r\n"))

		// 显示所有可用命令
		for name, cmd := range commands {
			// 跳过内置命令的重复显示
			if name == "help" || name == "?" {
				continue
			}
			writer.Write([]byte(fmt.Sprintf("  %-15s %s\r\n", name, cmd.Description)))
		}

		// 显示内置命令
		writer.Write([]byte("  help/?          Show this help message\r\n"))

		return nil
	}
}
