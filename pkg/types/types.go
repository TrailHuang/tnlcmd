// Package types 定义 TNLCMD 库的公共类型
package types

import "log/slog"

// CommandHandler 命令处理函数类型
type CommandHandler func(args []string) string

// 命令处理函数特殊返回值（sentinel values）
// 用于向会话层传达特殊操作意图
const (
	// SentinelExit 表示关闭当前连接
	SentinelExit = "__EXIT__"
	// SentinelExitToRoot 表示退出到根模式
	SentinelExitToRoot = "__EXIT_TO_ROOT__"
)

// CommandInfo 命令信息
type CommandInfo struct {
	Name        string
	Description string
	Handler     CommandHandler
}

// CommandNodeType 命令节点类型
type CommandNodeType int

const (
	NodeTypeCommand    CommandNodeType = iota // 命令节点
	NodeTypeOptional                          // 可选参数节点 []
	NodeTypeEnum                              // 枚举参数节点 ()
	NodeTypeNum                               // 数值范围节点 <>
	NodeTypeString                            // 字符串参数节点（大写字母）
	NodeTypeModeSwitch                        // 视图切换节点
	NodeTypeExit                              // 退出节点
)

// Config 命令行配置
type Config struct {
	Prompt     string
	Port       int
	WelcomeMsg string
	MaxHistory int
	logger     *slog.Logger // 可选：自定义日志器，nil 时使用 slog.Default()
	RootMode   interface{}  // 使用 interface{} 避免循环导入
}

// SetLogger 设置自定义日志器
func (c *Config) SetLogger(l *slog.Logger) {
	c.logger = l
}

// Logger 返回有效的 slog.Logger，如果未设置则返回默认 logger
func (c *Config) Logger() *slog.Logger {
	if c != nil && c.logger != nil {
		return c.logger
	}
	return slog.Default()
}
