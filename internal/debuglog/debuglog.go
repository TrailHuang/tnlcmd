// Package debuglog 提供集中式的调试日志能力，用于追踪"重启后用户输入无响应"
// 等难以静态复现的故障。默认保持关闭，对运行时零开销；排查问题时可通过
// 环境变量开启，将结构化日志输出到指定文件或控制台。
//
// 开启方式（环境变量）：
//
//	TNLCMD_DEBUG      级别：off/trace/debug（默认 off）
//	                    trace = Debug + Trace 全量输出
//	                    debug = 仅 Debug 输出
//	                    info/warn/error 不是本包支持的级别，视为 off
//	                    （本包只产生 Debug/Trace 级别事件，配置更高级别只会
//	                     导致"开了开关却看不到日志"，因此显式视为关闭）
//	TNLCMD_DEBUG_FILE 日志文件路径；为空时输出到标准错误 stderr
//
// 所有日志均带时间戳、日志级别、事件类型（msg）、组件名称（component）以及
// 由调用方提供的键值对字段（如 n=…、state=…），便于精确定位故障。
package debuglog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	mu      sync.Mutex
	logger  *slog.Logger
	enabled bool
)

func init() {
	configure(os.Getenv("TNLCMD_DEBUG"), os.Getenv("TNLCMD_DEBUG_FILE"))
}

// configure 按级别与输出文件构建 logger。
// init 与测试（同包白盒）都会调用；调用方负责串行化。
func configure(raw, file string) {
	configureWithWriter(raw, ioWriter(file))
}

// configureWithWriter 按级别与写出目标构建 logger（测试注入缓冲用）。
func configureWithWriter(raw string, w io.Writer) {
	mu.Lock()
	defer mu.Unlock()

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "trace":
		enabled = true
		logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: slog.LevelDebug - 4, // Trace 与 Debug 全量输出
		}))
	case "debug":
		enabled = true
		logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	default: // off / 空值 / 不支持的级别（info/warn/error 等）→ 关闭
		enabled = false
		logger = nil
	}
}

// ioWriter 返回日志输出目标：优先使用指定文件（追加模式），否则为标准错误。
func ioWriter(path string) *os.File {
	if path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			return f
		}
	}
	return os.Stderr
}

// Enabled 报告调试日志是否开启，供调用方在工作负载较高的热路径上提前短路，
// 避免无谓地构造日志参数字符串。
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Debug 记录一条调试事件。component 为组件名称（如 "session"），event 为事件名
// （如 "read_line"），kv 为可选的键值对字段。
func Debug(component, event string, kv ...any) {
	mu.Lock()
	l := logger
	ok := enabled
	mu.Unlock()
	if !ok {
		return
	}
	args := make([]any, 0, len(kv)+2)
	args = append(args, "component", component)
	args = append(args, kv...)
	l.Log(context.Background(), slog.LevelDebug, event, args...)
}

// Trace 记录一条更细致的跟踪事件，级别低于 Debug，仅在 TNLCMD_DEBUG=trace 时生效。
func Trace(component, event string, kv ...any) {
	mu.Lock()
	l := logger
	ok := enabled
	mu.Unlock()
	if !ok {
		return
	}
	args := make([]any, 0, len(kv)+2)
	args = append(args, "component", component)
	args = append(args, kv...)
	l.Log(context.Background(), slog.LevelDebug-4, event, args...)
}
