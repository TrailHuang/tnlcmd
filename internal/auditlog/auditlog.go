// Package auditlog 提供按日期滚动的审计日志文件写入器。
//
// 写入路径规则：AuditLogFile 配置的当天日志直接写入该路径（如 /var/log/tnlcmd/audit.log），
// 跨天后当天日志切换为新路径（如 audit_20261126.log），历史日志保留为
// <name>_<YYYYMMDD>.log 形式。过期日志（默认 30 天）由后台 goroutine 自动清理。
package auditlog

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// dateSuffixRegex 匹配滚动日志文件名中的日期后缀（audit_20261126.log）
var dateSuffixRegex = regexp.MustCompile(`^(.*)_(\d{8})\.log$`)

// retentionDays 历史日志保留天数，超过后由清理 goroutine 删除
const retentionDays = 30

// cleanupInterval 历史日志清理的扫描周期
const cleanupInterval = 1 * time.Hour

// filePermission 新建日志文件的权限位
const filePermission = 0o644

// Writer 按日期滚动的日志写入器，io.Writer + io.Closer 语义。
// Write 可被多个会话 goroutine 并发调用，内部以互斥锁串行化。
type Writer struct {
	mu     sync.Mutex
	path   string // 用户配置的基础路径，如 /var/log/tnlcmd/audit.log
	file   *os.File
	curDay string // 当前打开文件对应的日期（YYYYMMDD），与 path 一致时为空
	stop   chan struct{}
	stopped sync.WaitGroup
}

// NewWriter 创建写入器并打开当天的日志文件。
// path 为空时返回 nil（调用方视为未启用文件写入）。
// 同时启动后台清理 goroutine，按 cleanupInterval 周期删除超过
// retentionDays 的历史日志；Close 时停止。
func NewWriter(path string) (*Writer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	w := &Writer{
		path: path,
		stop: make(chan struct{}),
	}
	if err := w.rotate(time.Now().Format("20060102")); err != nil {
		return nil, err
	}
	w.stopped.Add(1)
	go w.cleanupLoop()
	return w, nil
}

// Write 实现 io.Writer。写入前检查日期是否跨天，必要时滚动到新文件。
// 任何写入错误都向上传播，由 slog Handler 自行容错（slog 写失败仅被忽略，
// 不会影响 CLI 命令处理主流程）。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, fmt.Errorf("auditlog: writer is closed")
	}

	day := time.Now().Format("20060102")
	if day != w.curDay && w.curDay != "" {
		if err := w.rotateLocked(day); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

// Close 停止清理 goroutine 并关闭当前日志文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stop != nil {
		close(w.stop)
		w.stop = nil
	}
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Wait 等待后台清理 goroutine 退出，供测试与优雅关闭使用。
func (w *Writer) Wait() { w.stopped.Wait() }

// rotateLocked 打开指定日期的日志文件并关闭旧文件。
// 调用方必须持有 w.mu。
// 当天（path 不带日期后缀）保持用户配置的原路径；历史日期追加 _YYYYMMDD 后缀。
func (w *Writer) rotateLocked(day string) error {
	target := w.path
	if day != time.Now().Format("20060102") {
		target = datedPath(w.path, day)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePermission)
	if err != nil {
		return fmt.Errorf("auditlog: open %s: %w", target, err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file = f
	w.curDay = day
	return nil
}

// rotate 与 rotateLocked 相同，用于初始化（尚未持锁）。
func (w *Writer) rotate(day string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked(day)
}

// datedPath 将基础路径转换为带日期后缀的路径：
// /var/log/tnlcmd/audit.log → /var/log/tnlcmd/audit_20261125.log
func datedPath(base, day string) string {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return stem + "_" + day + ext
}

// cleanupLoop 周期性扫描日志目录，删除超过保留期的历史日志。
// 启动时将 stop channel 快照到局部变量：Close 持锁置 nil 不会与本 goroutine
// 的无锁 select 读发生数据竞争。
func (w *Writer) cleanupLoop() {
	defer w.stopped.Done()
	w.mu.Lock()
	stop := w.stop
	w.mu.Unlock()
	if stop == nil {
		return
	}

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			w.cleanupOnce()
		}
	}
}

// cleanupOnce 单次清理：遍历基础路径所在目录，删除日期早于保留期的滚动日志。
// 当前活跃文件（无论是否带日期后缀）永不在清理路径中删除。
func (w *Writer) cleanupOnce() {
	w.mu.Lock()
	dir := filepath.Dir(w.path)
	active := ""
	if w.file != nil {
		active = w.file.Name()
	}
	w.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("auditlog: cleanup scan failed", "dir", dir, "error", err)
		return
	}

	base := filepath.Base(w.path)
	// 仅匹配与配置文件同名（去掉日期后缀）的滚动日志：
	// audit.log → audit_20261125.log 命中；audit_20261126.log → audit_20261125.log 命中
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Format("20060102")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := dateSuffixRegex.FindStringSubmatch(e.Name())
		if m == nil || m[1] != stem {
			continue
		}
		day := m[2]
		if day >= cutoff {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == active {
			continue
		}
		if err := os.Remove(full); err != nil {
			slog.Warn("auditlog: failed to remove expired log", "file", full, "error", err)
		} else {
			slog.Info("auditlog: removed expired log", "file", full)
		}
	}
}
