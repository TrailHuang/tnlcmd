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
	// ErrTooFewArguments 参数数量不足
	ErrTooFewArguments = errors.New("too few arguments")
	// ErrTooManyArguments 参数数量过多
	ErrTooManyArguments = errors.New("too many arguments")
	// ErrInvalidParameterValue 参数取值非法
	ErrInvalidParameterValue = errors.New("invalid parameter value")
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
	NodeTypeIPV4                              // IPv4 地址节点 A.B.C.D
	NodeTypeIPV4Prefix                        // IPv4 前缀节点 A.B.C.D/M
	NodeTypeIPV6                              // IPv6 地址节点 X:X::X:X
	NodeTypeIPV6Prefix                        // IPv6 前缀节点 X:X::X:X/M
	NodeTypeMAC                               // MAC 地址节点 M:A:C
	NodeTypeMACPrefix                         // MAC 前缀节点 M:A:C/M
)

// Config 命令行配置
type Config struct {
	Prompt     string
	Port       int
	WelcomeMsg string
	MaxHistory int
	PageSize   int // 分页大小（行数）；<=0 表示禁用分页，一次性输出全部内容

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

	// AuditLog 命令审计日志开关；false（默认）表示关闭，行为与未开启时一致。
	// 开启后每条命令执行完成时经 slog（Info 级）输出结构化审计记录：
	// 远端地址、命令原文、耗时、成败；密码认证失败事件同样纳入审计。
	// 审计输出目标由 AuditLogFile 决定：为空时写 stderr，非空时写按日期滚动的文件。
	AuditLog bool

	// AuditLogFile 审计日志文件路径；空（默认）表示审计输出到 stderr。
	// 非空时（需同时 AuditLog=true）审计日志写入该文件：
	// 当天日志使用配置路径原样（如 /var/log/tnlcmd/audit.log），
	// 历史日志自动重命名为 <stem>_YYYYMMDD.log（如 audit_20261125.log），
	// 超过 30 天的历史日志由后台 goroutine 自动清理。
	// 该字段需在 Start 之前设置；Start 时初始化文件与清理任务，Stop 时关闭。
	AuditLogFile string
}
