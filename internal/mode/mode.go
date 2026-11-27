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
	if !strings.HasSuffix(prompt, ">") && !strings.HasSuffix(prompt, "#") {
		if m.Name == "root" {
			prompt = prompt + "> "
		} else {
			prompt = strings.TrimSpace(prompt) + "# "
		}
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

// CommandContext 命令上下文
type CommandContext struct {
	CurrentMode *CommandMode
	Path        []string
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

// Clone 创建当前上下文的独立副本。
// Path 切片会被深拷贝，确保各会话对路径的修改互不影响；
// CurrentMode 保持共享指针——命令树在 Start 后为只读，
// 模式切换仅重新赋值本字段，不会并发 mutate 模式对象本身，因此共享是安全的。
func (c *CommandContext) Clone() *CommandContext {
	if c == nil {
		return nil
	}
	path := make([]string, len(c.Path))
	copy(path, c.Path)
	return &CommandContext{
		CurrentMode: c.CurrentMode,
		Path:        path,
	}
}

// GetRootMode 获取根模式
func (m *CommandContext) GetRootMode() *CommandMode {
	current := m.CurrentMode
	for current.Parent != nil {
		current = current.Parent
	}
	return current
}

// EnterMode 切换到指定模式并返回进入提示消息。
// 这是模式切换语义的唯一实现路径：processCommand 的 ModeSwitch 树节点分支、
// 无 handler 中间节点回退分支均委托本方法，
// 确保 ChangeMode 时机与提示消息格式全链路一致。
func (c *CommandContext) EnterMode(newMode *CommandMode) string {
	c.ChangeMode(newMode)
	return fmt.Sprintf("Entering %s mode\r\n", newMode.Description)
}
