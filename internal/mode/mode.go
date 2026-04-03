package mode

import (
	"fmt"
	"strings"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandMode 命令模式
type CommandMode struct {
	Name        string
	Prompt      string
	Description string
	Children    map[string]*CommandMode
	Parent      *CommandMode
	CommandTree *commandtree.CommandTree // 每个视图的独立命令树
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
		Children:    make(map[string]*CommandMode),
		CommandTree: commandtree.NewCommandTree(), // 为每个视图创建独立的命令树
	}
}

func (m *CommandMode) SetPrompt(prompt string) {
	if !strings.HasSuffix(prompt, ">") {
		prompt = prompt + "> "
	}
	m.Prompt = prompt
}

// AddCommand 添加命令到模式
func (m *CommandMode) AddCommand(name, description string, handler types.CommandHandler, detailedDescription ...string) {
	// 添加到当前视图的独立命令树
	if m.CommandTree != nil {
		_ = m.CommandTree.AddCommand(name, description, handler, detailedDescription...)
	}
}

// AddSubMode 添加子模式
func (m *CommandMode) AddSubMode(subMode *CommandMode) {
	subMode.Parent = m
	m.Children[subMode.Name] = subMode
}

// CommandContext 命令上下文
type CommandContext struct {
	CurrentMode *CommandMode
	Path        []string
	CommandTree *commandtree.CommandTree
}

// ChangeMode 切换模式
func (c *CommandContext) ChangeMode(newMode *CommandMode) {
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

// GetRootMode 获取根模式
func (m *CommandContext) GetRootMode() *CommandMode {
	current := m.CurrentMode
	for current.Parent != nil {
		current = current.Parent
	}
	return current
}

// createModeChangeHandler 创建模式切换处理函数
func (c *CommandContext) createModeChangeHandler(mode *CommandMode) types.CommandHandler {
	return func(args []string) string {
		c.ChangeMode(mode)
		return fmt.Sprintf("Entering %s mode\r\n", mode.Description)
	}
}
