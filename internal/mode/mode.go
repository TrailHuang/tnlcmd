package mode

import (
	"strings"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandMode 命令模式
type CommandMode struct {
	Name        string
	Prompt      string
	Description string
	Commands    map[string]types.CommandInfo
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
		Commands:    make(map[string]types.CommandInfo),
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
	m.Commands[name] = types.CommandInfo{
		Name:        name,
		Description: description,
		Handler:     handler,
	}

	// 同时添加到当前视图的独立命令树
	if m.CommandTree != nil {
		_ = m.CommandTree.AddCommand(name, description, handler, detailedDescription...)
	}
}

// AddSubMode 添加子模式
func (m *CommandMode) AddSubMode(subMode *CommandMode) {
	subMode.Parent = m
	m.Children[subMode.Name] = subMode
}
