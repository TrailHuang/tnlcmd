// Package context 定义命令上下文相关的类型
package commandctx

import (
	"fmt"

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
				Description: fmt.Sprintf("Enter %s configuration mode A", subMode.Description),
				Handler:     c.createModeChangeHandler(subMode),
			}
		}
	}

	//	c.addExitCommands(commands)

	return commands
}

// GetRootMode 获取根模式
func (m *CommandContext) GetRootMode() *mode.CommandMode {
	current := m.CurrentMode
	for current.Parent != nil {
		current = current.Parent
	}
	return current
}

// createModeChangeHandler 创建模式切换处理函数
func (c *CommandContext) createModeChangeHandler(mode *mode.CommandMode) types.CommandHandler {
	return func(args []string) string {
		c.ChangeMode(mode)
		return fmt.Sprintf("Entering %s mode\r\n", mode.Description)
	}
}
