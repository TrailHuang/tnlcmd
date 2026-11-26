package auditlog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestWriter 在临时目录创建基础路径为 <dir>/audit.log 的写入器。
func newTestWriter(t *testing.T) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter(%q): %v", path, err)
	}
	if w == nil {
		t.Fatal("NewWriter returned nil for non-empty path")
	}
	t.Cleanup(func() {
		_ = w.Close()
		w.Wait()
	})
	return w, path
}

// TestWriter_BasicWrite 验证写入内容落到配置的基础路径文件。
func TestWriter_BasicWrite(t *testing.T) {
	w, path := newTestWriter(t)

	if _, err := w.Write([]byte("line one\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.Write([]byte("line two\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != "line one\nline two\n" {
		t.Errorf("file content = %q, want %q", got, "line one\nline two\n")
	}
}

// TestWriter_EmptyPathReturnsNil 验证空路径返回 nil writer（未启用文件写入）。
func TestWriter_EmptyPathReturnsNil(t *testing.T) {
	for _, p := range []string{"", "   "} {
		w, err := NewWriter(p)
		if err != nil {
			t.Errorf("NewWriter(%q) error: %v", p, err)
		}
		if w != nil {
			t.Errorf("NewWriter(%q) = non-nil, want nil", p)
		}
	}
}

// TestWriter_WriteAfterClose 验证关闭后写入返回错误而非 panic。
func TestWriter_WriteAfterClose(t *testing.T) {
	w, _ := newTestWriter(t)
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("write after close should return error")
	}
	// 重复 Close 应安全（幂等）
	if err := w.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// TestWriter_ConcurrentWrites 验证并发写入互不干扰（-race 下运行）。
func TestWriter_ConcurrentWrites(t *testing.T) {
	w, path := newTestWriter(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := w.Write([]byte("w\n")); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if lines := strings.Count(string(got), "w\n"); lines != 160 {
		t.Errorf("line count = %d, want 160", lines)
	}
}

// TestWriter_DatedPath 验证 datedPath 的路径转换规则。
func TestWriter_DatedPath(t *testing.T) {
	for _, tc := range []struct {
		base, day, want string
	}{
		{"/var/log/tnlcmd/audit.log", "20261125", "/var/log/tnlcmd/audit_20261125.log"},
		{"audit.log", "20261125", "audit_20261125.log"},
		{"/tmp/tnlcmd", "20261125", "/tmp/tnlcmd_20261125"}, // 无扩展名
	} {
		if got := datedPath(tc.base, tc.day); got != tc.want {
			t.Errorf("datedPath(%q, %q) = %q, want %q", tc.base, tc.day, got, tc.want)
		}
	}
}

// TestWriter_CleanupExpired 验证清理逻辑：过期（>30 天）的历史日志被删除，
// 未过期的保留，活跃文件永不删除。
func TestWriter_CleanupExpired(t *testing.T) {
	w, path := newTestWriter(t)
	dir := filepath.Dir(path)

	old := filepath.Join(dir, "audit_20260101.log") // 远超 30 天前
	recent := filepath.Join(dir, "audit_20261231.log")
	other := filepath.Join(dir, "other_20260101.log") // 非同名前缀，不应清理

	for _, f := range []string{old, recent, other} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("setup %s: %v", f, err)
		}
	}

	// 直接单次调用触发清理（不等待 1 小时 ticker）
	w.cleanupOnce()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expired log %s should be removed (err=%v)", old, err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("future-dated log %s should be kept: %v", recent, err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("different-prefix log %s should be kept: %v", other, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("active file %s must never be removed: %v", path, err)
	}
}

// TestWriter_CleanupSkipsActiveFileEvenIfExpired 验证活跃文件即使日期过期也不被删除
// （极端场景：进程跨天长时间运行，当前打开的文件已带日期后缀）。
func TestWriter_CleanupSkipsActiveFileEvenIfExpired(t *testing.T) {
	dir := t.TempDir()
	// 构造一个带过期日期后缀的路径作为"基础路径"，NewWriter 会先按 datedPath
	// 逻辑打开它——此处绕过 NewWriter 直接手工构造，模拟活跃文件为历史日期。
	path := filepath.Join(dir, "audit_20260101.log")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	w := &Writer{path: path, stop: make(chan struct{})}
	w.mu.Lock()
	// 模拟 rotateLocked 已按该路径打开文件
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	w.file = f
	w.curDay = "20260101"
	w.mu.Unlock()
	t.Cleanup(func() {
		_ = w.Close()
		w.Wait()
	})

	w.cleanupOnce()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("active file %s must never be removed even if expired: %v", path, err)
	}
}

// TestWriter_AppendMode 验证重复启动追加写而非覆盖。
func TestWriter_AppendMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	w1, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w1.Write([]byte("first\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w1.Close()
	w1.Wait()

	w2, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter #2: %v", err)
	}
	if _, err := w2.Write([]byte("second\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w2.Close()
	w2.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("content = %q, want %q", got, "first\nsecond\n")
	}
}

// TestWriter_CleanupLoopStops 验证 Close 后清理 goroutine 在合理时间内退出，无泄露。
func TestWriter_CleanupLoopStops(t *testing.T) {
	w, _ := newTestWriter(t)
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	_ = w.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup goroutine did not exit after Close (goroutine leak)")
	}
}
