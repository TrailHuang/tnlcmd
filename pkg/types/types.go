// Package types 定义 TNLCMD 库的公共类型
package types

import (
	"context"
	"errors"
	"time"
)

// CommandHandler 命令处理函数类型（旧签名，保持向后兼容）
type CommandHandler func(args []string) string

// ContextHandler 支持上下文的命令处理函数类型。
// handler 可通过 ctx 感知连接关闭/服务停止并提前退出（Go 无法强杀 goroutine，
// 不检查 ctx 的 handler 仍由服务端 Stop 超时兜底）。
type ContextHandler func(ctx context.Context, args []string) string

// Adapt 将旧签名 CommandHandler 包装为 ContextHandler，忽略 ctx。
func Adapt(h CommandHandler) ContextHandler {
	return func(_ context.Context, args []string) string {
		return h(args)
	}
}

// 命令查找 sentinel error，调用方应使用 errors.Is 判断而非字符串匹配
var (
	// ErrIncompleteCommand 命令不完整（缺少后续必需的子命令或参数）
	ErrIncompleteCommand = errors.New("incomplete command")
	// ErrUnknownCommand 未知命令
	ErrUnknownCommand = errors.New("unknown command")
)

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
)

// Config 命令行配置
type Config struct {
	Prompt     string
	Port       int
	WelcomeMsg string
	MaxHistory int
	PageSize   int         // 分页大小（行数）；<=0 表示禁用分页，一次性输出全部内容
	RootMode   interface{} // 使用 interface{} 避免循环导入

	// MaxConnections 最大并发连接数上限；0（默认）表示不限制。
	// 超限时新连接收到拒绝提示后立即关闭。
	MaxConnections int

	// IdleTimeout 空闲会话回收阈值；0（默认）表示禁用回收。
	// 会话空闲（无输入）超过该时长后由服务端主动关闭连接。
	IdleTimeout time.Duration

	// Password 可选密码认证口令；空（默认）表示关闭认证，
	// 连接建立后直接进入 CLI，行为与未开启认证时完全一致。
	// 开启后连接先进入密码输入流程（不回显），验证通过方可进入 CLI。
	Password string

	// MaxAuthAttempts 密码错误次数上限，超过后断开连接；
	// 0 或负数（默认）使用内置默认值 3。
	MaxAuthAttempts int
}
