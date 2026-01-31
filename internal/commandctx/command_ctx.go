// Package context 定义命令上下文相关的类型
package commandctx

import (
	"fmt"
	"strings"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandContext 命令上下文
type CommandContext struct {
	CurrentMode *mode.CommandMode
	Path        []string
	CommandTree *commandtree.CommandTree
}

// GetRootMode 获取根模式
func (c *CommandContext) GetRootMode() *mode.CommandMode {
	current := c.CurrentMode
	for current.Parent != nil {
		current = current.Parent
	}
	return current
}

// ChangeMode 切换模式
func (c *CommandContext) ChangeMode(newMode *mode.CommandMode) {
	c.CurrentMode = newMode

	// 更新路径
	var path []string
	current := newMode
	for current != nil {
		path = append([]string{current.Name}, path...)
		current = current.Parent
	}
	c.Path = path
}

// GetAvailableCommands 获取当前模式下可用的命令
func (c *CommandContext) GetAvailableCommands() map[string]types.CommandInfo {
	commands := make(map[string]types.CommandInfo)

	// 添加内置命令（在所有模式下都可用）
	commands["help"] = types.CommandInfo{
		Name:        "help",
		Description: "Show this help message",
		Handler:     c.createHelpHandler(),
	}
	commands["?"] = types.CommandInfo{
		Name:        "?",
		Description: "Show this help message",
		Handler:     c.createHelpHandler(),
	}

	// 添加当前模式的命令
	for name, cmd := range c.CurrentMode.Commands {
		commands[name] = cmd
	}

	// 添加所有子模式切换命令（从任意视图都可以切换到其他视图）
	rootMode := c.GetRootMode()
	for name, subMode := range rootMode.Children {
		// 如果当前不是该子模式，则显示切换命令
		if c.CurrentMode != subMode {
			commands[name] = types.CommandInfo{
				Name:        name,
				Description: fmt.Sprintf("Enter %s configuration mode", subMode.Description),
				Handler:     c.createModeChangeHandler(subMode),
			}
		}
	}

	// 添加退出命令
	if c.CurrentMode.Parent == nil {
		// 根视图：exit和quit都关闭连接
		commands["exit"] = types.CommandInfo{
			Name:        "exit",
			Description: "Exit and close connection",
			Handler:     c.createCloseConnectionHandler(),
		}
		commands["quit"] = types.CommandInfo{
			Name:        "quit",
			Description: "Exit and close connection",
			Handler:     c.createCloseConnectionHandler(),
		}
	} else {
		// 子视图：exit返回上级视图
		commands["exit"] = types.CommandInfo{
			Name:        "exit",
			Description: "Exit to previous mode",
			Handler:     c.createExitModeHandler(),
		}
	}

	return commands
}

// createModeChangeHandler 创建模式切换处理函数
func (c *CommandContext) createModeChangeHandler(mode *mode.CommandMode) types.CommandHandler {
	return func(args []string) string {
		c.ChangeMode(mode)
		return fmt.Sprintf("Entering %s mode\r\n", mode.Description)
	}
}

// createExitToRootHandler 创建退出到根模式处理函数
func (c *CommandContext) createExitToRootHandler() types.CommandHandler {
	return func(args []string) string {
		// 找到根模式
		root := c.GetRootMode()
		c.ChangeMode(root)
		return "Exiting to privileged EXEC mode\r\n"
	}
}

// createCloseConnectionHandler 创建关闭连接处理函数
func (c *CommandContext) createCloseConnectionHandler() types.CommandHandler {
	return func(args []string) string {
		return "Connection closed\r\n"
	}
}

// createHelpHandler 创建帮助命令处理函数
func (c *CommandContext) createHelpHandler() types.CommandHandler {
	return func(args []string) string {
		var result strings.Builder
		commands := c.GetAvailableCommands()

		// 显示当前模式信息
		result.WriteString(fmt.Sprintf("Current mode: %s\r\n", c.CurrentMode.Description))
		result.WriteString("Available commands:\r\n")

		// 显示所有可用命令
		for name, cmd := range commands {
			result.WriteString(fmt.Sprintf("  %-20s %s\r\n", name, cmd.Description))
		}

		return result.String()
	}
}

// createExitModeHandler 创建退出模式处理函数
func (c *CommandContext) createExitModeHandler() types.CommandHandler {
	return func(args []string) string {
		if c.CurrentMode.Parent != nil {
			c.ChangeMode(c.CurrentMode.Parent)
			return fmt.Sprintf("Exiting to %s mode\r\n", c.CurrentMode.Description)
		}
		return ""
	}
}
