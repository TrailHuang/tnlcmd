// Package debuglog 提供集中式的调试日志能力，用于追踪"重启后用户输入无响应"
// 等难以静态复现的故障。默认保持关闭，对运行时零开销；排查问题时可通过
// 环境变量开启，将结构化日志输出到指定文件或控制台。
//
// 开启方式（环境变量）：
//
//	TNLCMD_DEBUG      级别：off/trace/debug（默认 off，开启后即记录 Debug/Trace）
//	TNLCMD_DEBUG_FILE 日志文件路径；为空时输出到标准错误 stderr
//
// 所有日志均带时间戳、日志级别、事件类型（msg）、组件名称（component）以及
// 由调用方提供的键值对字段（如 n=…、state=…），便于精确定位故障。
package debuglog

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	once    sync.Once
	logger  *slog.Logger
	enabled bool
)

// parseLevel 解析环境变量中的日志级别字符串。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return slog.LevelDebug - 4
	case "debug", "":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}

func init() {
	// 惰性初始化：在首次调用时按环境变量构建 logger，避免全量开启产生额外开销。
	once.Do(func() {
		raw := strings.TrimSpace(os.Getenv("TNLCMD_DEBUG"))
		var lvl slog.Level
		switch strings.ToLower(raw) {
		case "off", "":
			// 默认关闭 debug/trace，正常日志信息级别不受影响
			enabled = false
			lvl = slog.LevelInfo
		default:
			enabled = true
			lvl = parseLevel(raw)
		}
		w := ioWriter(os.Getenv("TNLCMD_DEBUG_FILE"))
		logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl}))
	})
}

// ioWriter 返回日志输出目标：优先使用指定文件（追加模式），否则为标准错误。
func ioWriter(path string) *os.File {
	if path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			return f
		}
	}
	return os.Stderr
}

// Enabled 报告调试日志是否开启，供调用方在工作负载较高的热路径上提前短路，
// 避免无谓地构造日志参数字符串。
func Enabled() bool {
	once.Do(func() {})
	return enabled
}

// Debug 记录一条调试事件。component 为组件名称（如 "session"），event 为事件名
// （如 "read_line"），kv 为可选的键值对字段。
func Debug(component, event string, kv ...any) {
	once.Do(func() {})
	if !enabled {
		return
	}
	args := make([]any, 0, len(kv)+2)
	args = append(args, "component", component)
	args = append(args, kv...)
	logger.Log(context.Background(), slog.LevelDebug, event, args...)
}

// Trace 记录一条更细致的跟踪事件，级别低于 Debug，仅在 TNLCMD_DEBUG=trace 时生效。
func Trace(component, event string, kv ...any) {
	once.Do(func() {})
	if !enabled {
		return
	}
	args := make([]any, 0, len(kv)+2)
	args = append(args, "component", component)
	args = append(args, kv...)
	logger.Log(context.Background(), slog.LevelDebug-4, event, args...)
}
